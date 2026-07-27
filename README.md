# ft — terminal file tree explorer

A JetBrains-style project pane for a terminal split. Built with Go and
Bubble Tea for macOS (Linux-ready via `internal/platform` build tags).

- Remembers expanded/collapsed dirs, selection, and scroll **per root
  directory** across restarts (`~/.filetree/state/`).
- Git aware: status colours (modified, staged, untracked, conflict), greyed
  gitignored entries, and a `•` marker on dirs containing changes.
- Hot reload: expanded directories are watched (fsnotify); external changes
  appear automatically. F5 forces a full reload.
- Copy the selection's path as **absolute** (`y`) or **relative to the
  closest parent git repo** (`Y`) — ready to paste into helix, rg, or fd.
- Copy (`u`) or open-and-copy (`U`) the selection's **web URL** built from
  the repo's origin remote: `/blob/` for files, `/tree/` for directories,
  pinned to the HEAD commit (permanent; set `link_ref = "branch"` for
  branch links). Handles ssh/scp/https remote forms and escapes paths.
  Caveat: links to unpushed commits or untracked files 404 until pushed —
  untracked selections are flagged in the status bar.
- Configurable commands with `{path}`/`{relpath}`/`{dir}`/… templates:
  interactive commands suspend the TUI (editors); background commands fire
  and forget (tmux hand-off, `open`). Marked paths are available as
  `{marked}` (all, in mark order) and `{marked1}`/`{marked2}` (the two most
  recent — ready-made for a diff command).
- Mark files/dirs with `space` (yazi-style `▍` bar + tinted name, live
  count in the status bar), then `p`/`m` copies or moves them to the
  selection, and `d` trashes them. Overwrites go via the Trash; name
  clashes can keep both with `-1` suffixes. Moves across filesystems fall
  back to copy + Trash.
- Scratch notes: `S` touches an empty `YYYYMMDDHH.md` in the scratch dir
  (`[scratch]` in the config, default `~/.filetree/scratch`) and opens it
  in the default editor — quit and you're in the scratch tree with the
  file selected; `s`/Esc flips between scratch and your project. Marks are
  absolute paths, so you can mark scratch files and paste them into the
  project (or vice versa).
- Git worktrees: `W` asks for a **branch name or PR number**, creates a
  worktree for the repo containing the selection under
  `~/.filetree/worktrees/<repo>/<branch>` (`[worktrees] dir` in the config)
  then opens the worktrees view with the new worktree selected; `w` toggles
  that view too, Esc comes back. A known branch is checked out, a remote-only one is fetched first,
  and an unknown name becomes a new branch off HEAD. `d` on a worktree root
  runs `git worktree remove` (offering `--force` when it has local changes)
  instead of trashing the directory.
- The status bar shows `⎇ <branch>` for the repo containing the highlighted
  item (short commit hash when detached). It refreshes with git status, so a
  bare `git checkout` elsewhere that touches no watched file needs F5.
- Mouse: wheel scrolls, click selects, chevron-click/double-click expands,
  double-click on a file runs the default command, header toggles are
  clickable.
- Nerd Font file-type icons (set `icons = "plain"` in the config otherwise).

## Install

```sh
go install ./cmd/ft
ft [dir]                  # defaults to the current directory
```

First run writes a commented starter config to `~/.filetree/config.toml`.

When started outside tmux, `ft` relaunches itself in a new tmux session so the
split and hand-off commands work — no alias needed, and starting it inside an
existing session (over ssh, say) just runs it there. Opt out per run with
`ft --no-tmux`, or permanently with `tmux = "never"` under `[general]`.

## Dependencies

Nothing here is mandatory: `ft` browses, marks, copies, moves, and trashes with
none of it installed. Each tool switches on a specific feature, and a missing
one surfaces as an error in the status bar rather than a failure to start.

| Tool | Enables | Optional? |
|---|---|---|
| `git` | status colours, `•` dirty markers, gitignore greying, `⎇ branch` in the status bar, `Y` git-relative paths, `u`/`U` web links, `w`/`W` worktrees | optional, but most of the git awareness is dark without it |
| `tmux` | the `t`/`v`/`n`/`r` split and hand-off commands, and the auto-relaunch above | optional |
| `hx` ([helix](https://helix-editor.com)) | the starter's default command — Enter, `e`, `S` scratch files and `C` edit-config all run `commands.default` | optional; point `commands.default` at any editor |
| `rg` ([ripgrep](https://github.com/BurntSushi/ripgrep)) | the `/` finder's `Grep` content search, and `r` grep-here | optional; without it the finder still searches file names |
| `lazygit` | `L` (commented example in the starter) | optional |
| `delta` | `D` diff the two most recently marked files (commented example in the starter) | optional |

Clipboard, browser, Finder reveal, and Trash go through `pbcopy`, `open`, and
`osascript` — all macOS built-ins, nothing to install.

## Keys

| Key | Action |
|---|---|
| `↑`/`k` `↓`/`j` | move selection |
| `←`/`h` | collapse, or jump to parent |
| `→`/`l` | expand, or step into first child |
| `enter` | file: run default command · dir: toggle |
| `space` | mark/unmark the selection (and move down) |
| `esc` | clear all marks; with none, return from the scratch or worktrees view |
| `p` / `m` | copy / move marked items into the selected dir (or the selected file's parent); conflicts prompt overwrite-to-Trash vs keep-both |
| `y` / `Y` | copy absolute / git-relative path |
| `u` / `U` | copy the selection's web URL (GitHub-style, from the origin remote) / open it in the browser and copy |
| `.` | toggle hidden files |
| `i` | toggle gitignored files |
| `F5` | reload from disk |
| `o` | reveal in Finder |
| `/` | fuzzy finder (esc cancels, enter jumps) |
| `tab` | in the fuzzy finder: cycle the `Find` / `Type` / `Grep` fields |
| `ctrl+g` | in the fuzzy finder: raise the match limit for the session (2×, 3×, …) |
| `ctrl+y` | in the fuzzy finder: copy the `rg` command behind the `Type`/`Grep` fields |
| `ctrl+l` | in the fuzzy finder: empty all three fields |
| `f` | reopen the fuzzy finder with the last `Find`/`Type`/`Grep` still in place |
| `a` / `A` | new file / new directory |
| `R` | rename |
| `d` | delete marked items — or the selection if none — to Trash (confirm names what's deleted); on a worktree root, `git worktree remove` instead |
| `g` / `G`, `ctrl+u`/`ctrl+d` | top / bottom, half-page |
| `s` | toggle the scratch view (and back) |
| `S` | new scratch file (`YYYYMMDDHH.md`, pre-created empty) opened in the default editor |
| `w` | toggle the worktrees view (and back) |
| `W` | new git worktree for the repo containing the selection, from a branch name or PR number — lands in the worktrees view with it selected |
| `H` | collapse all (also clears marks) |
| `C` | edit `~/.filetree/config.toml` in the default editor; config reloads on exit |
| `?` | help |
| `q` | quit |

Keys are remappable in the `[keys]` section of the config.

## Commands

Commands may bind their own keys; the starter config binds the following set.
Most need `tmux` (see [Dependencies](#dependencies)); `ft` puts itself in a
session automatically, so they work out of the box.

| Key | Command |
|---|---|
| `e` | open the selection in helix — works on directories too (helix shows its file picker) |
| `t` | smart hand-off to the previously-active tmux pane: opens the file in the helix already running there (`:open`), types the `hx` command if a shell is waiting, or creates a split otherwise |
| `v` | open the selection in helix in a new full-height split at the right edge — repeatable, one pane per press |
| `n` | open a shell in a new full-height split at the right edge, in the selection's directory |
| `r` | prime an `rg` in the other tmux pane at the selection's directory |
| `L` | open lazygit for the repo containing the selection (commented example in the starter) |

## Fuzzy Find

Fuzzy find (`/`) has three input lines; `tab` (`shift+tab`) cycles them.

### Find

**`Find`** matches fuzzy subsequences, not regexps; include `/` in the query
to constrain by path segments. Navigate results with `↑`/`↓`
(`ctrl+p`/`ctrl+n`), half-page with `ctrl+u`/`ctrl+d`, or the mouse wheel;
the list scrolls with the selection and shows a `12/1000` position counter
(`…` while the walk is still running, `+` if it stopped at the candidate cap).
Ranking is screen-aware: entries currently visible in the tree outrank
everything else, then shallow paths and basename matches beat equally-fuzzy
deep ones. With an empty query the list shows exactly the visible tree entries
in order, so `/` + cursor keys doubles as a quick jump list.

### Type

**`Type`** narrows by file type, as a comma-separated list of globs:

| Typed | Matches |
|---|---|
| `hcl` | `*.hcl`, or a file named exactly `hcl` |
| `.hcl` | `*.hcl` |
| `terragrunt.hcl` | that basename, anywhere in the tree |
| `*.tf` | matched against the basename |
| `infra/**/*.hcl` | matched against the whole path (`**` spans directories) |
| `!vendor/**` | a leading `!` excludes |

The filter is applied **while walking**, not to the results, which is what
makes it useful on a large root: `Type: terragrunt.hcl` with an empty `Find`
lists every one of them in a monorepo far too big to index whole. Candidates
come from a breadth-first walk, so top-level entries are always indexed even
in huge roots, and the walk stops as soon as you leave the finder. At most 1000
matches are kept (`fuzzy_max_matches` under `[general]`) out of at most 50,000
indexed paths (`fuzzy_max_candidates`).

When results are being dropped at that cap the counter says so — `12/1000 max`
in amber — and **`ctrl+g`** multiplies the limit for the rest of the ft session:
once for 2×, again for 3×, and so on. The counter turns green and gains a `×3`
once raised, so a session running on a raised limit is never a surprise. In name
mode this is instant, since the candidates are already walked; in content mode
it re-runs the ripgrep search. The raise survives closing and reopening the
finder, and resets when you quit.

The part of each filename that the `Type` filter accounts for is highlighted in
gold — the `.hcl` of `*.hcl`, the whole basename of `terragrunt.hcl` — while
`Find` matches stay blue. Where they overlap, `Find` wins.

### Grep

**`Grep`** searches *inside* the files `Type` selected, using
[ripgrep](https://github.com/BurntSushi/ripgrep) — this is the one part of
`ft` that needs `rg` installed. Together the two fields are the `fd … | rg …`
combination: `Type: hcl` + `Grep: dependency "` finds every `terragrunt.hcl`
containing a dependency block. Result rows become `path:line  matched text`,
and `Find` narrows them further by path. Enter still jumps to the **file** in
the tree — the line number is there to help you choose, not to open at. The
search respects the hidden and gitignored toggles, is debounced so a
half-typed regexp is never run, and takes at most 5 matches from any one file
(`fuzzy_grep_max_per_file`, passed straight through as ripgrep's
`--max-count`, so ripgrep enforces it while reading). ripgrep's own errors — a
malformed regexp, most often — appear beside the field.

Whenever `Type` or `Grep` has anything in it, the **status bar shows the
ripgrep command** the finder amounts to, and **`ctrl+y`** copies the whole thing
to the clipboard so you can run it yourself and check the finder against it.
With a pattern typed that is a content search; with only a type filter it is
`rg --files`, listing the files the filter selects — handy for confirming a
glob does what you meant before typing a pattern.

The copied command is self-contained — the tree root is the search path, so it
works from any directory. It differs from what `ft` actually runs in two ways
that cannot change which files or lines match: the output flags are
human-readable instead of `--json`, and paths print absolute rather than
root-relative. One caveat for the `--files` form: the finder's own file list
comes from its walk, which applies the same globs but takes gitignore state
from the cached `git status` and stops at `fuzzy_max_candidates`. So it answers
"does my filter select what I think it does", not "is the walk complete".

Results arrive in whatever order ripgrep finds them — it searches files in
parallel, and sorting would mean waiting for the whole search to finish. That
matters in one case: when the **total** cap (`fuzzy_max_matches`) is reached,
*which* files made it in is down to timing. `ctrl+g` raises the cap; ripgrep's
own `--sort path` would make the order stable at the cost of running
single-threaded.

### Picking up where you left off

`/` always opens empty; **`f`** reopens
the finder with all three fields exactly as you left them, and puts the
selection back on the row you jumped from — so the search → open a file →
look at it → back to the same results loop costs one keystroke instead of
retyping a glob and a regexp. A content search is re-run rather than restored,
since its results went stale while you were away. Restoring the row is best
effort: if the file is gone, or no longer matches, the selection simply starts
at the top. **`ctrl+l`** empties all three fields without leaving the finder.

### Editing the fields

The finder's inputs take the usual readline keys, with
two exceptions where list navigation gets there first: `ctrl+u` is half-page up
rather than delete-to-start, and `ctrl+d` is half-page down *unless the cursor
has a character to its right*, in which case it deletes forward. That makes
`ctrl+d` — and the macOS Fn+Backspace that many terminals send as `ctrl+d` —
work as a delete key while you are editing, and as a scroll key while you are
browsing results, which is where the cursor sits once a query is typed. Word
deletion (`ctrl+w`, `alt+backspace`) and delete-to-end (`ctrl+k`) are untouched.

### Limits

Several caps are in play, at different layers, and they do not all apply to
both modes:

| Limit | Default | Config key | Enforced by | Mode | What you see |
|---|---|---|---|---|---|
| Candidate cap | 50,000 | `fuzzy_max_candidates` | the walk, between directories | name only | `+` on the counter; deep files absent |
| Match cap | 1,000 (× `ctrl+g`) | `fuzzy_max_matches` | `ft`, as results arrive | both | ` max` on the counter; the search stops there |
| Matches per file | 5 | `fuzzy_grep_max_per_file` | ripgrep `--max-count` | content only | at most 5 rows from one file |
| Line length | 1 MiB | — | `ft`, while parsing | content only | `3 skipped (line too long)` beside the pattern |
| Debounce | 150 ms | — | `ft` | content only | the pause before ripgrep runs |

The parts that surprise people:

- **The candidate cap does not constrain a content search.** ripgrep does its
  own traversal, so `Grep` reaches files the name list had truncated away —
  the two modes genuinely see different sets.
- **5 per file × 1000 total means at most 200 distinct files** in a content
  search before the cap bites.
- **The line-length cap exists because ripgrep has no way to bound it.**
  `--max-columns` is ignored in the `--json` mode `ft` parses, and a 2.5 MB
  minified line becomes an 11 MB JSON event. Matches on such lines are dropped
  and counted rather than buffered, so a search over a tree full of bundles and
  sourcemaps stays responsive — and tells you what it left out.

## Config

See `~/.filetree/config.toml` (created on first run) for command templates,
toggle defaults, and keybinding overrides. Template placeholders are
shell-quoted on substitution; unknown `{tokens}` pass through untouched so
tmux formats like `"{last}"` work.
