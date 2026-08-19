# AGENTS.md

`ft` is a terminal file-tree explorer (a JetBrains-style project pane for a
tmux split) written in Go with Bubble Tea v2. macOS is the supported OS;
the code is structured for Linux to be added behind build tags.

## Build, test, verify

Use go-task (`Taskfile.yml`; there is no default task):

```sh
task vet       # go vet ./...
task test      # go test ./...
task build     # builds ./ft (gitignored)
task install   # go install ./cmd/ft  -> ~/go/bin/ft
```

All code must be `gofmt`-clean and `go vet`-clean, and tests must pass
before any change is considered done.

## Critical gotcha: charm.land import paths

The Charm v2 modules use vanity import paths. `go get
github.com/charmbracelet/bubbletea/v2` FAILS with a module-path mismatch.
Use:

- `charm.land/bubbletea/v2`
- `charm.land/lipgloss/v2`
- `charm.land/bubbles/v2`

Verify v2 APIs with `go doc charm.land/bubbletea/v2 <Symbol>` rather than
assuming v1 shapes: `View()` returns `tea.View` (AltScreen/MouseMode are set
on it per render), mouse events are typed messages (`tea.MouseClickMsg`,
`tea.MouseWheelMsg`), and `Key.String()` returns text for printable keys
("Y") and names otherwise ("ctrl+c", "up").

## Package map

```
cmd/ft/             entrypoint, flag parsing
internal/app/       Bubble Tea model — the heart of the tool
  app.go            model, Update dispatch, keybinding table (buildBindings)
  actions.go        navigation, toggles, clipboard, file ops, command exec
  view.go           rendering: header (clickable toggles), tree, status bar
  mouse.go          wheel/click/double-click, header hit-testing
  fuzzy.go          candidate walk (BFS, streamed) + screen-aware ranking
  grep.go           content-search mode: debounce, hit list, Find narrowing
  git.go            per-repo status cache plumbing
internal/tree/      pure tree model: lazy nodes, expand/collapse, flatten
internal/fsops/     directory listing + fsnotify watcher (debounced batches)
internal/gitx/      repo discovery, `git status --porcelain -z --ignored` parsing
internal/search/    finder file-type filter (pure) + the ripgrep integration
internal/config/    TOML config, command templates + shell quoting, starter
internal/state/     per-root JSON persistence (expansion, selection, toggles)
internal/icons/     Nerd Font glyphs; table.go is GENERATED — see below
internal/platform/  OS interface; darwin impl (pbcopy, open -R, Finder trash)
internal/tmux/      every tmux invocation: the self-relaunch decision, and
                    the named agent sessions (name building, list parsing, kill)
```

Design rules that keep this maintainable:

- `internal/tree` stays free of filesystem and UI dependencies; directory
  listing is injected via `tree.Lister`.
- All git information comes from one parsed `git status` per repo, cached in
  the app model and refreshed via async `tea.Cmd`s. Never shell out to git
  per file.
- OS-specific behaviour goes behind `platform.Platform` in
  `platform_<goos>.go` files with build tags — never inline `exec.Command`
  for OS integrations elsewhere.
- Each external *tool* gets one package that owns every invocation of it:
  `internal/gitx` for `git`, `internal/search` for `rg`. Argument building
  and output parsing stay pure and table-tested (`search/args.go`,
  `search/parse.go`); the process spawn lives alone (`search/exec.go`). The
  app only sequences them in async `tea.Cmd`s.
- The finder's file-type filter is compiled once, in `internal/search`, and
  used by both the walk (`Filter.Match`) and ripgrep (`Filter.Globs` becomes
  `-g` args) — so a filtered file list and a filtered content search can never
  disagree about which files are in scope.
- Web links (`u`/`U`): `internal/gitx/link.go` owns remote-URL
  normalisation and URL building (pure, table-tested); `platform.OpenURL`
  owns the browser.
- **Commands ship in `internal/config/catalogue.go`, never in the starter.**
  `starterTOML` is written to `~/.filetree/config.toml` on first run and never
  again, and `Load` merges the file *over* the catalogue rather than replacing
  it — so a command added to the starter reaches nobody who has already run ft
  once. That is a real bug that shipped once already;
  `TestStarterDefinesNoCommands` guards it. `[keys]` covers commands as well as
  actions, resolved together by `resolveActionKeys`, and the commented `[keys]`
  reference in the starter is generated from both tables so it cannot go stale.
- `internal/tmux` owns every tmux call, the way `gitx` owns git: the pure half
  (`session.go` — name building, `list-sessions` parsing) is table-tested, and
  the process spawns live alone in `run.go` and `exec.go`. The self-relaunch
  keeps its decision pure the same way: `tmux.ShouldWrap` takes an `Env` struct
  so it is table-tested, and the `syscall.Exec` lives alone in
  `internal/tmux/exec.go`. It runs in `main` *after* root validation and config
  load, so startup errors print in the user's terminal instead of dying with
  the session they would have created.
- New keybindings are wired in `buildBindings` (internal/app/app.go); make
  them remappable via the `[keys]` action map and list them in the `?` help
  overlay (view.go) and README.
- ctrl+c is the only guaranteed way out, and `handleKey` rewrites it to `"esc"`
  before the mode switch whenever the mode is not `modeNormal`. So a new mode
  gets "the first press backs out, the next one quits" for free by handling
  `esc`, and needs no ctrl+c case of its own. `quit` itself ships unbound (like
  `reload`, which `F5` covers) — the action is still in `DefaultActionKeys` so
  `[keys]` can give it a letter, but nothing does by default, which is what
  keeps `q` free for a command.

## Generated code

`internal/icons/table.go` is generated from nvim-web-devicons' lua tables —
do not hand-edit. Glyphs are written as `\uXXXX`/`\UXXXXXXXX` escapes
because literal PUA glyphs do not survive copy/paste reliably. Devops
entries missing upstream (hcl, terragrunt, helm, …) are added in
`icons.go`'s init by reusing generated glyphs.

## Runtime files

- Config: `~/.filetree/config.toml` (TOML; commented starter written on
  first run from `internal/config/starter.go` — keep starter and README in
  sync with behaviour changes).
- State: `~/.filetree/state/<basename>-<hash8>.json`, one per tree root.
- Command templates: `{path} {relpath} {dir} {root} {name}` plus mark vars
  `{marked} {marked1} {marked2}` are substituted shell-quoted; unknown
  `{tokens}` pass through untouched (tmux formats like `"{last}"` depend on
  this; also why `{marked1}` must be replaced before `{marked}` — the
  Replacer tries patterns in argument order). Commands run via `/bin/sh -c`
  with mode "interactive" (tea.ExecProcess suspends the TUI) or
  "background".
- The scratch view (`s`/`S`) is plain re-rooting: `loadRoot` (internal/app)
  swaps tree+state; per-root state files make each root remember its own
  expansion. `prevRoot` (session-only) powers the Esc/toggle-key return; Esc
  is layered — marks clear first, then the view returns.
- Worktrees (`w`/`W`) reuse the same re-rooting: `<[worktrees] dir>/<repo
  basename>/<branch or pr-N>`. `internal/gitx/worktree.go` owns every git
  invocation (add/remove/fetch, linked-worktree detection, input parsing);
  the app only sequences them in async `tea.Cmd`s. `d` on a worktree root
  becomes `git worktree remove` (pendingOp kind `opWorktree`, with an `f`
  force re-prompt when git refuses a dirty tree).
- Agent tmux sessions (`c`/`x`/`alt+s` to create, `T` to list) are named
  `<[sessions] prefix><repo>/<branch>/<tool>`, built by `tmux.SessionName` from
  `m.repoIdentFor` (internal/app/sessions.go) — the *main* repo of a linked
  worktree, so every worktree files under one name. The prefix is the only
  filter, and everything ft opens for itself stays unnamed. "." and ":" are
  rewritten to "_" in a name because tmux does that silently otherwise, which
  would stop `new-session -A` finding what it created. The picker is a fourth
  `finderSource` (`srcTmux`), not a new mode — see the bookmark view for the
  pattern; its ctrl+w/ctrl+x/alt+n are hardcoded in the modeFuzzy switch and
  listed in `finderReservedKeys`.
- The status-bar branch (`⎇ …`) is cached per repo in `m.branches`, filled
  by the same async cmds that read git status — so it refreshes wherever
  status does, and nowhere else.
- Marks (space bar) live only in the app model (`marked`/`markOrder`,
  path-keyed, session-only). Copy/move of marked items goes through
  `internal/fsops/transfer.go` — destructive steps (overwrite, cross-device
  move source) always go via the injected Trash, never unlink. Delete (`d`)
  is mark-aware (falls back to the selection) and ancestor-dedupes the list
  first so a marked dir plus its marked children can't race.

## Testing a TUI change for real

Unit tests cover tree/gitx/config/state/search/fuzzy logic. Tests that need
ripgrep skip when it is absent (`search.Available`). For end-to-end checks,
drive the real binary:

- Plain pty: `script -q /tmp/out sh -c 'stty rows 30 cols 90; ./ft <dir>'`
  with keystrokes piped on stdin. A `script` pty starts 0x0 — without the
  `stty` the app renders nothing.
- tmux integration (hand-off commands, splits): create a detached session
  (`tmux new-session -d -s t -x 200 -y 50 './ft <dir>'`), drive with
  `tmux send-keys`, assert with `tmux list-panes` / `tmux capture-pane -p`.
- When asserting "file opened in helix", grep the statusline (`NOR` +
  filename), not just the filename — pasted-garbage bugs also contain the
  filename in the buffer.
- A disposable fixture repo with modified/untracked/ignored files is the
  standard way to exercise git colouring; create it under /tmp, not in this
  repo.

Known input gotcha (documented in the starter config): keys sent to helix
via `tmux send-keys` must deliver `Escape` in its own send, then sleep
~150ms, or ESC coalesces with the next byte and parses as an Alt-chord.

**Anything that builds a shell string for tmux has to be exercised on macOS.**
tmux runs `display-popup` and `send-keys` commands through `default-shell`,
which is `/bin/zsh` on a stock macOS and `/bin/sh` or bash in a Linux
container — so a whole class of bug is invisible off the supported OS. The one
that shipped: `ShellQuote` treated `=` as safe, the session picker's `=`
exact-match target reached zsh bare, zsh applied *equals expansion* (a word
starting with `=` becomes the path of the command it names), and
`attach-session` failed with the popup closing too fast to read the error.
Every other shell leaves `=foo` alone. Prefer passing tmux targets as direct
argv (`exec.Command("tmux", "kill-session", "-t", target)`) where there is no
template to honour — that path was never affected.

Two things worth measuring rather than reasoning about, both of which were
guessed wrong first: `display-popup` **blocks** its caller until the popup
closes (`display-popup -E "sleep 4"` takes four seconds), and a popup is not a
pane, so attaching from inside one passes tmux's nested-session check even
though `$TMUX` is set.
