# ft — terminal file tree explorer

A JetBrains-style project pane for a terminal split. Built with Go and
Bubble Tea for macOS (Linux-ready via `internal/platform` build tags).

- Remembers expanded/collapsed dirs, selection, and scroll **per root
  directory** across restarts (`~/.filetree/state/`).
- Sticky parents: scroll into a deep subtree and the directories it sits in
  stay pinned above the tree, the way an editor pins the enclosing scopes.
  They track the top of the pane, so they always describe what is on screen;
  the block takes its own space rather than covering a row, is capped at a
  third of the pane (a `…` marks parents dropped to fit), and a click on a
  pinned directory jumps to it. `sticky_parents = false` under `[general]`
  turns it off.
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
  and forget (tmux hand-off, `open`). `{paths}` is every space-marked path, or
  the selection when nothing is marked — so `e` opens one file or five in a
  single helix. Marked paths are also available as `{marked}` (all, in mark
  order) and `{marked1}`/`{marked2}` (the two most recent — ready-made for a
  diff command). A command can also declare a `finder_key` to run from inside
  the fuzzy finder, against the highlighted result and its `{line}`.
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
- Agent sessions: `c` opens **Claude Code** — `x` Copilot, `alt+s` a plain
  shell — in a *named* tmux session for the repo and branch of the selection
  (`ft/agent/<repo>/<branch>/<tool>`), in a popup over the tree. Detach and it
  keeps running; press the same key again and you are back in it. `T` lists
  them across all your repos and worktrees, newest first with the ones ringing
  a bell on top — `enter` reattaches in a popup, `ctrl+w` opens one in a pane
  beside the tree without leaving the tree, and `ctrl+x` kills it.

  A session opened in a pane keeps its name and stays in the list, so `X` in the
  tree detaches it again — whatever is in it carries on running, and the tree
  gets its space back.

- Every session `ft` opens is named, not just the agents: the shell popup, the
  lazygit and diff popups, and the tree you are looking at
  ([naming](#tmux-sessions)). So `T` is the way back into anything you have
  detached from — a shell popup with a server still running in it, say — and
  `tmux ls` says what each session is instead of numbering them. This tree is
  in that list too, marked as such and left alone by every key in it:
  reattaching would put the tree in a popup over itself, and `ctrl+x` would
  kill `ft` where it stands.

- The tree follows your editor: bind `:sh ft jump %{buffer_name}` in helix and
  the pane beside it moves its cursor to the buffer you are in. With several
  trees open the one that answers is the one whose root holds the file, and of
  those, the one in your own tmux window — see
  [Following the editor](#following-the-editor).
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
| `git` | status colours, `•` dirty markers, gitignore greying, `⎇ branch` in the status bar, `Y` git-relative paths, `u`/`U` web links, `w`/`W` worktrees, `alt+d` diffs | optional, but most of the git awareness is dark without it |
| `tmux` | the `t`/`v`/`n`/`N`/`r` split and hand-off commands, the `alt+n`/`L`/`alt+d` popups, the `c`/`x`/`alt+s` agent sessions and the `T` list of them, `ctrl+l` to focus the pane to the right, `ctrl+j`/`ctrl+k` to resize this one, `alt+h` to even the widths out, the `/` finder's `finder_width` widening, and the auto-relaunch above | optional |
| `hx` ([helix](https://helix-editor.com)) | the starter's default command — Enter, `e`, `S` scratch files and `C` edit-config all run `commands.default` | optional; point `commands.default` at any editor |
| `rg` ([ripgrep](https://github.com/BurntSushi/ripgrep)) | the `/` finder's `Grep` content search, and `r` grep-here | optional; without it the finder still searches file names |
| `lazygit` | `L` (repo view) and `M` (file blame/log view), in popups over the window | optional |
| `delta` | `D` diff the two most recently marked files | optional |
| `claude` / `copilot` | the `c` / `x` agent sessions, and the `T` list they populate | optional; any CLI works — the commands are ordinary config |

Clipboard, browser, Finder reveal, and Trash go through `pbcopy`, `open`, and
`osascript` — all macOS built-ins, nothing to install.

## Keys

| Key | Action |
|---|---|
| `↑`/`k` `↓`/`j` | move selection |
| `←`/`h` | collapse, or jump to parent |
| `→`/`l` | expand, or step into first child |
| `enter` | file: run default command · dir: toggle |
| `space` | mark/unmark the selection (and move down) — `e`, `t`, `v` and `enter` then act on the whole set |
| `esc` | clear all marks; with none, return from the scratch or worktrees view |
| `p` / `m` | copy / move marked items into the selected dir (or the selected file's parent); conflicts prompt overwrite-to-Trash vs keep-both |
| `y` / `Y` | copy absolute / git-relative path |
| `u` / `U` | copy the selection's web URL (GitHub-style, from the origin remote) / open it in the browser and copy |
| `.` | toggle hidden files |
| `i` | toggle gitignored files |
| `F5` | reload from disk |
| `o` | reveal in Finder |
| `/` | fuzzy finder (esc cancels, enter jumps) |
| `F` | fuzzy finder, confined to the selected directory (its parent for a file) |
| `tab` | in the fuzzy finder: cycle the `Find` / `Grep` / `Type` fields |
| `ctrl+g` | in the fuzzy finder: raise the match limit for the session (2×, 3×, …) |
| `ctrl+y` | in the fuzzy finder: copy the `rg` command behind the `Type`/`Grep` fields |
| `ctrl+o` | in the fuzzy finder: empty all three fields |
| `ctrl+e` | in the fuzzy finder: open the highlighted result in helix, at the matched line — quitting returns you to the results |
| `ctrl+t` | in the fuzzy finder: hand the highlighted result to the other tmux pane, without closing the finder |
| `f` | reopen the `/` finder with the last `Find`/`Grep`/`Type` still in place |
| `b` | recently opened files, newest first — fuzzy-filtered the same way; enter reveals and opens |
| `B` | line bookmarks captured from your editor — searchable by path *and* by the line's contents |
| `a` / `A` | new file / new directory |
| `R` | rename |
| `d` | delete marked items — or the selection if none — to Trash (confirm names what's deleted); on a worktree root, `git worktree remove` instead |
| `g` / `G`, `ctrl+u`/`ctrl+d` | top / bottom, half-page |
| `s` | toggle the scratch view (and back) |
| `S` | new scratch file (`YYYYMMDDHH.md`, pre-created empty) opened in the default editor |
| `w` | toggle the worktrees view (and back) |
| `W` | new git worktree for the repo containing the selection, from a branch name or PR number — lands in the worktrees view with it selected |
| `c` / `x` | Claude Code / Copilot in a named tmux session for the selection's repo and branch, in a popup; pressing it again reattaches to the same one |
| `alt+s` | a plain named shell in the same scheme — for a dev server or a test watcher you want to find again |
| `T` | list the tmux sessions `ft` owns — agents, shells, popups and trees: `enter` reattaches in a popup, `ctrl+w` opens one in a pane beside the tree and stays put (press it again to move into that pane), `ctrl+x` kills it (asking first if it is attached elsewhere). Type a kind (`agent`, `shell`) to narrow the list; this tree is marked and refuses all three |

| `X` | detach the agent session sharing this window, handing its space back to the tree |

| `>` | re-root the tree to the selection — the directory itself, or a file's parent — so the tree and every search start there; `esc` returns to the project. Rooting deeper replaces the root rather than stacking, so one `esc` always comes home |
| `H` | collapse all (also clears marks) |
| `C` | edit `~/.filetree/config.toml` in the default command; an editor that takes over this pane reloads the config when it exits |
| `alt+c` | re-read the config from disk — for when the editor is somewhere `ft` cannot see it finish, such as the `t` hand-off to another pane |
| `?` | help |
| `ctrl+c` | quit — from the finder, a prompt or a confirmation the first press backs out to the tree, and the next one quits |

Every key above — and every command below — is remappable by name in the
`[keys]` section of the config, one line each:

```toml
[keys]
claude-popup = "C"    # a command
rename       = "f2"   # an action
```

A key belongs to one thing, so moving something onto a key another thing
already holds means saying where that one goes too — `rename = "f2"` alongside
`worktree-new = "R"`, or a straight swap. An override that would leave
something with no key at all is refused rather than obeyed, so a typo cannot
hide `rename`: `ft` starts as usual, says how many conflicts it found in the
status bar, and lists them at the top of `?`. The navigation keys (arrows,
`hjkl`, `g`/`G`, `enter`, `shift+enter`, `ctrl+u`/`ctrl+d`, `ctrl+c`, `F5`) are
not remappable and cannot be taken; anything that tries is reported the same
way.

`shift+enter` does the same as `>`. It only reaches `ft` on terminals that
report modified keys, and inside tmux that needs `set -s extended-keys on` in
your `~/.tmux.conf` — it is off by default, and without it tmux hands the chord
over as a plain `enter`, which expands the directory instead. `>` always works,
which is why the action ships on it.

Two actions ship with no key of their own, because the navigation set already
covers them: `reload` (`F5`) and `quit` (`ctrl+c`). Both are still bindable, so
`quit = "q"` puts quitting back on a letter if you want it there — but leaving
it unbound is what keeps `q` free for something you chose, and stops a mistyped
key ending the session.

`?` is the list of names to use, and the commented `[keys]` block written into
your config on first run has the same list with the defaults beside it.

## Commands

Commands ship with `ft` — they are built into the binary, not written into
your config — so a new release brings its new commands with it. Most need
`tmux` (see [Dependencies](#dependencies)); `ft` puts itself in a session
automatically, so they work out of the box.

| Key | Finder key | Command |
|---|---|---|
| `e` | `ctrl+e` | open the selection — or everything marked — in helix; works on directories too (helix shows its file picker) |
| `t` | `ctrl+t` | smart hand-off to a pane beside `ft`: opens the files in a helix already running in the window (`:open`), types the `hx` command if a shell is waiting, or creates a split otherwise |
| `v` | | open the selection, or everything marked, in helix in a new full-height split at the right edge — repeatable, one pane per press |
| `n` | | open a shell in a new full-height split at the right edge, in the selection's directory |
| `N` | | the same shell in a split beside the tree, dividing the current pane rather than the window |
| `alt+n` | | the same shell in a popup over the window, for something to run and dismiss rather than keep beside the tree — in a session named after the directory, so detaching from it and pressing the key again comes back to it |
| `c` | | Claude Code in a named tmux session for the selection's repo and branch, in a popup — created on the first press, reattached on every one after |
| `x` | | the same for the Copilot CLI |
| `alt+s` | | the same for a plain shell, so long-running work is reachable from the `T` list too |
| `r` | | prime an `rg` at the selection's directory in a shell beside `ft`, or a new split if there is no shell to type into |
| `L` | | open lazygit for the repo containing the selection, in a popup — one session per checkout, whichever subdirectory you press it in |
| `M` | | open lazygit focused on the selected file's blame / log view, in a popup |
| `alt+d` | | diff the selection — or everything marked — against `HEAD` in a popup; through git's pager if one is configured, and readable without one |
| `D` | | diff the two most recently marked files in a split, with `delta` |
| `ctrl+l` | `ctrl+l` | focus the tmux pane to the right of `ft` — the keyboard equivalent of `ctrl+b` `→`; silent when there is no pane to the right, or when `ft` is not in tmux |
| `ctrl+j` | `ctrl+j` | narrow `ft`'s pane to 30% of the window |
| `ctrl+k` | `ctrl+k` | widen `ft`'s pane to 70% of the window |
| `alt+h` | `alt+h` | give every pane in the window the same width, side by side — tmux's `even-horizontal` layout, for a window that has drifted out of shape |

`t` and `r` choose their target by looking at what is running in the window
rather than by asking tmux for the previously-active pane. A pane `ft` opens is
created detached, so it never becomes active and never becomes the "last" pane
either — which left `{last}` pointing back at `ft` itself, and every press after
the first opening yet another helix until you visited one by hand. `t` prefers a
running helix over a waiting shell, since sending to an editor already open
reuses it where typing `hx` at a shell starts a second one. `r` only ever types
into a *shell*: aimed at whatever was last, it fed `rg -n … -e ` to a helix
sitting there as editor keystrokes and left the buffer modified.

A command that opens a pane — `t`'s fallback split, `v`, `n`, `N`, `D` — takes
its columns from *every* pane in the window, so a 30-column tree beside an
editor used to come back at 18. `ft` now measures its own width immediately
before the split and puts it back afterwards, the way `ctrl+w` always has, so
repeated splits leave the tree exactly where you had it. The columns come from
your other panes instead. It is skipped for an `ft` that fills the window,
where restoring the old width would crush the pane just opened.

The mirror case — a pane *closing* and its columns coming back — is only
handled for the ones `ft` closes itself (`X`). `ft` hears about someone else's
close only when its own pane happens to resize, and from the width alone that
is indistinguishable from you resizing it deliberately, so `ft` leaves it
alone rather than risk undoing a resize you meant.


### Customising

The built-in set needs no configuration at all — `config.toml` is only for
where you differ from it:

```toml
[keys]
claude-popup = "C"                 # move a command's key, one line

[commands]
disabled = ["copilot-popup"]       # switch one off and free its key

[commands.claude-popup]            # override a built-in: only the fields you
run = "..."                        # name change, the rest is kept

[commands.notes]                   # or add one of your own
run  = "hx ~/notes/{name}.md"
mode = "interactive"               # or "background" (the default)
key  = "ctrl+b"
desc = "open this file's notes"    # shown in "?"
```

Overriding a built-in is a *partial* override: `[commands.claude-popup] key =
"C"` changes the key and keeps everything else, so you never copy a command
into your config just to move it. And because the set lives in the binary
rather than in your file, a command added in a later version arrives when you
rebuild — an old config does not have to be edited to catch up.

### tmux sessions

Everything `ft` opens in a tmux session of its own is named after what it is,
so a session you detached from can be found again — in `T`, and by eye in
`tmux ls`. The kind comes first, which is also what a query in `T` narrows on:

```
ft/agent/<repo>/<branch>/<tool>   ft/agent/filetree/main/claude
                                  ft/agent/filetree/claude-tmux-nav/copilot
ft/tree/<dir>-<hash>              ft/tree/filetree-1a2b3c4d
ft/shell/<dir>-<hash>             ft/shell/filetree-1a2b3c4d
ft/lazygit/<checkout>-<hash>      ft/lazygit/filetree-1a2b3c4d
ft/blame/<file>-<hash>            ft/blame/main_go-9f8e7d6c
ft/diff/<file>-<hash>             ft/diff/main_go-9f8e7d6c
```

An agent is named after the git repository holding the selection. A branch's
`/` is flattened to `-`, and `.` and `:` become `_` because tmux rewrites those
itself. The repo is the **main** repository even when the selection is in a
linked worktree, so every worktree of a project files under one name and `T`
reads down its first column.

Everything else is named after a *place*: its basename, plus a hash of the full
path so two directories called `web` stay two sessions. Which place is the one
worth coming back to — a shell belongs to the directory it starts in, lazygit
to the checkout it shows whatever subdirectory you pressed `L` in, and a blame
or a diff to the file.

All of them are created with `new-session -A`, so the key that opened one is
the key that returns to it: detach (the tmux prefix, then `d`), and the next
press lands in the same session with its scrollback rather than starting a
second one. The tree is the exception — a second `ft` on the same directory is
a second tree, and takes a `-2` on the end instead.

The prefix is `[sessions] prefix` in the config, and it is the only thing `T`
filters on: rename a session out from under it and it drops off the list.

The session outlives the tool: the command is `claude; exec $SHELL`, so
quitting the agent leaves a shell in the same directory with the scrollback
still there. `T` shows what is running in each one (`claude` while it works,
your shell once it has stopped), how long since it last did anything, `●` for
one you have open somewhere, and `!` for one with a terminal bell pending —
which is what Claude Code rings when it is waiting for you.

These are ordinary `[commands]` entries: point them at any CLI, change the
keys, or add a fourth. `{session}`, `{repo}`, `{branch}`, `{gitroot}` and
`{repokey}` are the template variables that need a repository, and a command
using any of them is refused outside one rather than run with a half-built
name. `{prefix}` with `{dirkey}` or `{pathkey}` is how the rest name
themselves — `-s {prefix}shell/{dirkey}` — and those work anywhere, which is
the point: a shell popup is as useful outside a repository as in one.

The last four take the same chord in the tree and in the finder, so a search
can be left up while you go and look at something in the pane beside it. All
three popups run their own nested tmux session, which is what gives them their
own panes and scrollback; closing that session (`exit`, `ctrl+d`) closes the
popup. `alt+d` wants that scrollback in particular: git pages its output only
when a pager is configured, so a long diff on a machine without one still has
somewhere to scroll back to. It waits for a keypress only when nothing else is
holding the screen — an empty diff, a git error, or no pager at all — so with a
pager configured the `q` that quits it closes the popup too.

A popup does not inherit the working directory of the shell that asked for it:
tmux starts it in the session's own working directory, which is wherever `ft`
was started. So a command of your own that needs a popup somewhere in
particular has to say so with `display-popup -d {dir}` (and `new-session -c
{dir}` if it nests one) — putting `cd {dir} &&` in front of it does nothing.

A command's `key` fires it against the tree selection. Its optional
`finder_key` fires it from inside the fuzzy finder, against the highlighted
result — see [Running commands on a result](#running-commands-on-a-result).
Only chords work there, since a bare key would be typed into the finder's
input; the keys the finder handles itself are rejected at config load.

`ctrl+l`, `ctrl+j` and `ctrl+k` each begin with a `[ -z "$TMUX" ] ||` guard.
Outside a pane, `tmux` resolves a command against the most recently used
session, so an unguarded `select-pane` or `resize-pane` would move the focus or
change the size in a window you are not even looking at — and being silent by
design, these are exactly the commands where you would never notice.

### Acting on marked files

`space` marks files; `{paths}` is how a command acts on them. It expands to
**every marked path, oldest first, or the selection when nothing is marked** —
the same rule `d` uses to decide what to delete. Mark three files, press `e`,
and one helix opens with three buffers in mark order.

```toml
run = "hx {paths}"

# 3 marked  →  hx '/a/one.go' '/a/two.go' '/a/three.go'
# no marks  →  hx '/a/one.go'
# Grep hit  →  hx '/a/one.go:42'
```

`{paths}` carries the position itself, because a list cannot take one `:42`
between them: a lone target gets the line it was found on, a list is plain.
That is the opposite of `{line}`, which expands to `1` rather than nothing when
there is no match — a fallback that exists so `{path}:{line}` stays valid, and
which would be noise here. So `hx {paths}` is right in the tree, on a marked
set, and on a `Grep` row alike.

Worth knowing:

- **`enter` follows the same rule**, since it runs the default command — with
  marks set it opens all of them. Consistent with `d`, which deletes the marks
  rather than the cursor, but it reads as "this one", so it is worth saying.
- **The finder ignores tree marks.** `ctrl+e` and `ctrl+t` act on the row under
  the finder's own cursor; marks belong to the tree.
- **Marks survive the command** by default, so `e` to open them and then `t` to
  push them to a pane both work on the same set. `esc` clears them. Set
  `clear_marks_after_command = true` under `[general]` to have them consumed
  instead, matching `d`/`p`/`m` — that only fires for a command that named the
  marks *and* took them as its target, so `n` or a fresh scratch file leaves
  them alone.
- **A mark whose file has gone is dropped** and unmarked rather than opened as
  an empty buffer; if none survive, the command does not run.
- **Marked directories are passed through** — you marked them deliberately, and
  helix opens its file picker for one, the same as `e` on a directory does.

## Fuzzy Find

Fuzzy find (`/`) has three input lines — `Find`, `Grep`, `Type` — and `tab`
(`shift+tab`) cycles them in that order, so one `tab` from `Find` reaches
`Grep`. Above them, a `Dir` line shows where it is searching — see
[Scoping to a directory](#scoping-to-a-directory).

The tree reads happily in a sidebar; the finder does not, because every result
carries a second column — the line a `Grep` hit matched, a bookmark's text, a
session's status — that a narrow pane has nowhere to put. So opening the finder
**widens `ft`'s own tmux pane** to `finder_width` (`[general]`, default `60%`,
also a plain column count or `"off"`), and closing it puts the width back.

`ft` only ever widens: a pane already that wide, or wider, is left exactly as it
is. And it only restores a width it actually set — resize the pane yourself
while the finder is up, with `ctrl+j`/`ctrl+k` or by dragging the border, and
`ft` leaves your width alone on the way out rather than overruling it.

Where the width is not available — `finder_width = "off"`, outside tmux, or a
genuinely small terminal — results below 80 columns **stack** instead: the
location on one line, the text it matched indented underneath. Long paths lose
their head rather than their basename, marked with a `…`. The two sources with
no second column worth the space — the tree search and the recently-opened list
— stay one line per result at any width.

### Find

**`Find`** matches fuzzy subsequences, not regexps; include `/` in the query
to constrain by path segments. Whitespace tokens that start with `!` **exclude**
paths containing that substring (case-insensitive, not fuzzy — a fuzzy invert
of `nonprod` would drop almost everything). Several `!` tokens all apply, so
`!nonprod !sandbox` keeps rows containing neither; leftover tokens stay one
fuzzy include (`spanner !nonprod`). Navigate results with `↑`/`↓`
(`ctrl+p`/`ctrl+n`), half-page with `ctrl+u`/`ctrl+d`, or the mouse wheel;
the list scrolls with the selection and shows a `12/1000` position counter
(`…` while the walk is still running, `+` if it stopped at the candidate cap).
Ranking is screen-aware: entries currently visible in the tree outrank
everything else, then shallow paths and basename matches beat equally-fuzzy
deep ones. With an empty query the list shows exactly the visible tree entries
in order, so `/` + cursor keys doubles as a quick jump list.

### Grep

**`Grep`** searches *inside* the files the `Type` filter below selects, using
[ripgrep](https://github.com/BurntSushi/ripgrep) — this is the one part of
`ft` that needs `rg` installed. Together the two fields are the `fd … | rg …`
combination: `Type: hcl` + `Grep: dependency "` finds every `terragrunt.hcl`
containing a dependency block. Result rows become `path:line  matched text`,
and `Find` narrows them further by path, including `!term` excludes — Grep
`spanner-instance` then Find `!nonprod` hides the nonprod hits. Enter still
jumps to the **file** in the tree — the line number is there to help you
choose, not to open at. The
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

### Scoping to a directory

`/` searches the whole tree. **`F`** opens the finder confined to the selected
directory — its parent when a file is selected — and a `Dir` line above `Find`
names it. The walk starts there and `rg` is constrained to the same place, so a
`Grep` on a large repo costs what the subtree costs rather than what the repo
does, and the candidate cap stops being a factor. On the root there is nothing
to confine to, so `F` is simply `/`.

The scope belongs to a finder session and is fixed when it opens: `f` (resume)
keeps it, so you come back to the results you left, and `ctrl+o` empties the
three fields without touching it. To change it, leave and press `F` again
somewhere else — or `/` for the whole tree. The `Dir` line appears only when a
scope is in force, so it always states a fact about the current search rather
than a possibility.

Results are still listed relative to the tree root, not to the scope, so
jumping and the `{path}` a command receives are unaffected. If the scope
directory disappears while you are away, the finder says so and falls back to
the whole tree.

### Running commands on a result

`enter` reveals the highlighted result in the tree and closes the finder.
To act on it *without* losing the results, a command can declare a
`finder_key` — the starter binds two that act on the highlighted row:

- **`ctrl+e`** opens the result in helix in this pane. Quit helix and you are
  back in the finder: same row, same three fields, results not re-run. Nothing
  is saved and restored to achieve that — an interactive command blocks the
  whole event loop while it owns the terminal, so the finder is simply frozen
  and repainted when the child exits.
- **`ctrl+t`** hands the result to the other tmux pane and *stays* in the
  finder, so several results can be pushed into panes in one visit.

and three more that ignore the row entirely and drive the tmux panes, so that
moving between them or resizing this one does not mean leaving a search first:
**`ctrl+l`** focuses the pane to the right, **`ctrl+j`** and **`ctrl+k`** narrow
and widen `ft`'s own.

On a `Grep` row both open at the matched line: `{paths}` appends it to a lone
target, so `hx {paths}` serves the tree, a marked set and the finder alike (see
[Acting on marked files](#acting-on-marked-files)). `{line}` is still there for
a template that wants the number on its own, and is `1` when there is none.

Two things worth knowing. The tree cursor does not follow a file opened this
way — `enter` is still how you move it deliberately. And the result list is a
snapshot from the finder's walk, so a file *created* while you were in helix
will not appear until the walk restarts; edits to existing files are picked up
normally.

### Recently opened files

**`b`** opens the same finder over the files you have opened from this tree,
newest first, with how long ago beside each. Type to narrow it exactly as in
`/`; `enter` reveals the file in the tree *and* opens it in the default
command; a command's `finder_key` works here too, so `ctrl+t` hands a
remembered file straight to a pane.

The history is per tree, kept in `<root>.recent.json` beside that root's state
file, and holds the last `recent_max` files (100 by default, set under
`[general]`). It survives restarts, and two `ft` sessions on one tree merge
rather than overwrite each other's.

What gets recorded is decided by the command, not the key: a command counts as
opening a file when its template names it with `{paths}`, `{path}` or
`{relpath}` — a marked set records every file in it. So
`enter`, `e`, `t`, `v` and `alt+d` are remembered — reading a file's diff counts
as having had it open — while `n`/`N`/`alt+n` (a shell in `{dir}`), `r` (an `rg`
primed at `{dir}`), `L` (lazygit in `{dir}`), `D` (a diff of marked paths) and
the `ctrl+l`/`ctrl+j`/`ctrl+k`/`alt+h` pane commands are not.
Directories are never recorded, and neither is `C` — its file lives outside the
tree. Files deleted since are dropped from the list rather than offered and
then failing to open, but they stay in the history, since a branch switch can
bring them back.

This view has only the one input line. There is no `Type` filter — a hundred
paths do not need a second way to narrow — and no `Grep`, since the files are
scattered across the tree and there is no single directory to point `rg` at.

### Line bookmarks

`b` remembers files; **`B`** remembers *places*. A bookmark is a file and a
line, captured from your editor and listed in the same finder — searchable by
path and by the line's contents at once.

Bind a key in helix to record one:

```toml
# ~/.config/helix/config.toml
[keys.normal.space]
b = ":sh ft bookmark %{buffer_name} %{cursor_line}"
```

`ft bookmark` is a subcommand of `ft` itself, so nothing needs to know the
storage format and no daemon has to be running. A bad path exits non-zero and
helix shows *"Shell command failed"* — which is what happens on an unnamed
buffer, since helix expands that to the literal `[scratch]`.

> **Do not add `%{selection}`.** It works with a word selected and **silently
> does nothing at all** when the selection spans lines — helix still reports
> "Command run". `ft` reads the line's text from the file itself, so the list
> shows real content without it. The subcommand does take an optional third
> argument if you want a label anyway.
>
> Note also that `%{cursor_line}` is the *head* of a selection, so bookmarking
> with a block selected records the line the cursor ended on.

In the view: `tab` sorts by recency or path, `enter` opens the file at its
line, `ctrl+x` forgets one, and a command's `finder_key` still works — so
`ctrl+t` pushes a bookmark to the other pane, at its line. Typing highlights
what it matched, in the path or in the line's text, wherever the match landed.

`B` always comes back to where you left it — the query, the sort and the scope
last the session. It keeps that state separately from the `/` finder, so `B`
and `f` never overwrite each other's.

**Bookmarks belong to the repository, not to the tree.** They are stored
relative to the checkout and keyed by the repo's common git dir, so every
worktree shares one list and a bookmark taken on `main` resolves against the
worktree's copy of the file. A file in no repository goes to a global store.

One consequence worth knowing: a bookmark is filed under the project it points
*into*, so bookmarking a file from another project while sitting in this one
puts it in that project's list. The header says how many are hidden
(`+7 elsewhere`), and **`ctrl+s`** widens the list to every project.

**Lines move, and bookmarks follow them.** `ft` stores the bookmarked line plus
a couple either side, and re-anchors by matching that block — which is what
keeps a bookmark on `}` or `return nil` from following the wrong one of the
dozen identical lines in the file. Rows are marked when the anchor had to work
for it:

| | |
|---|---|
| *(none)* | still exactly where it was |
| `~` | the block moved; the line number followed it |
| `≈` | only the line itself matched, and only because it was unique — approximate |
| `?` | the file is here but the anchor is not |
| `✗` | the file is missing; the stored text is shown instead, so the row stays searchable |

A missing file is not forgotten straight away: it may be missing only on this
branch. `bookmark_retention_days` (30 by default, `0` to disable) drops one
whose file has not been *seen* for that long, so switching back to a branch
that has it simply restarts the clock. `ctrl+x` forgets one immediately.

Bookmarks live in `~/.filetree/bookmarks/`, capped at `bookmark_max` (500) per
repository. Note that the anchor means a few lines of your source are written
there.

### Following the editor

The tree hands files to your editor. `ft jump` sends them back the other way:
bind a key in helix and the tree pane beside it moves its cursor to whatever
buffer you are in, so the sidebar keeps telling you where you are even when you
navigated there with goto-definition or the file picker.

```toml
# ~/.config/helix/config.toml
[keys.normal.space]
e = ":sh ft jump %{buffer_name}"
```

Like `ft bookmark`, this is a subcommand of `ft` itself, and a failure exits
non-zero so helix shows *"Shell command failed"* with the reason behind it.
Nothing is focused: you stay in the editor, and the tree just follows.

**Several trees can be open, and the right one answers.** Each running `ft`
listens on its own socket in `~/.filetree/run/`; `ft jump` asks all of them
where they are rooted and then chooses:

1. Trees whose root **contains the file** — the rest cannot show it, so they are
   out regardless of where they are on screen.
2. Of those, the one in **your own tmux window**, then your own session. helix
   runs `:sh` with `$TMUX_PANE` set, so the request knows which pane it came
   from; the pane next to your editor is the one you are looking at.
3. Still tied — two trees on the same project — the **deepest root** wins, then
   the oldest.

If no tree covers the file, nothing moves and the command fails. That is the
deliberate choice: a jump never re-roots a pane out from under you, so the
`esc`-comes-home behaviour of `>`, `s` and `w` keeps meaning what it meant.

Outside tmux there is no layout to reason about and the root decides alone.
With no tmux, no sockets, or nothing running, the command fails and the tree is
untouched — like every other integration here, it is optional.

Two refusals are worth recognising. *"`.cache/` is hidden — press `.` to show"*
means the file is there but filtered out of the rows, and names the key that
would reveal it; the toggles are per-root and persisted, so a jump will not
flip them for you. *"filetree is mid-prompt"* means that tree has a rename or
new-file prompt open, and moving its cursor would retarget it.

### Picking up where you left off

`/` always opens empty; **`f`** reopens
the finder with all three fields exactly as you left them, and puts the
selection back on the row you jumped from — so the search → open a file →
look at it → back to the same results loop costs one keystroke instead of
retyping a glob and a regexp. A content search is re-run rather than restored,
since its results went stale while you were away. Restoring the row is best
effort: if the file is gone, or no longer matches, the selection simply starts
at the top. **`ctrl+o`** empties all three fields without leaving the finder.

Each entry key owns its own view and its own memory. `f` always resumes the
*tree* search, whatever you had open last, and `B` always comes back to the
bookmarks you were filtering — so using one never decides where the other takes
you. `ctrl+o` likewise clears only the fields of the view you are in.

### Editing the fields

The finder's inputs take the usual readline keys, with
two exceptions where list navigation gets there first: `ctrl+u` is half-page up
rather than delete-to-start, and `ctrl+d` is half-page down *unless the cursor
has a character to its right*, in which case it deletes forward. That makes
`ctrl+d` — and the macOS Fn+Backspace that many terminals send as `ctrl+d` —
work as a delete key while you are editing, and as a scroll key while you are
browsing results, which is where the cursor sits once a query is typed. Word
deletion (`ctrl+w`, `alt+backspace`) is untouched.

Two readline keys are gone, taken by commands rather than by the finder:
`ctrl+e` is not end-of-line but "open this result", and `ctrl+k` is not
delete-to-end but "widen the pane". Both are `finder_key` bindings, and a
`finder_key` is matched before the field gets the keypress, so rebinding those
two commands in `[commands]` gives the readline keys back. For clearing rather
than editing, `ctrl+o` empties the fields outright.

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
toggle defaults, and keybinding overrides. `alt+c` re-reads it without a
restart.
 The placeholders are `{path}`,
`{paths}`, `{relpath}`, `{dir}`, `{root}`, `{name}`, `{line}`, `{marked}`,
`{marked1}` and `{marked2}`. They are shell-quoted on substitution; unknown
`{tokens}` pass through untouched so tmux formats like `"{last}"` work.

`{paths}` is the multi-file counterpart of `{path}` — see
[Acting on marked files](#acting-on-marked-files).

A setting `ft` does not recognise is reported rather than obeyed or ignored: the
status bar says how many there are at startup and `?` names them. A misspelling
is one way to get one, but the usual cause is a line written under a `[keys]`
header that is still commented out — TOML attaches it to whichever table came
last instead, normally a command, as a field that command has no place for. It
reads like a working line and does nothing at all, which is exactly why `ft`
now says so. Everything else in the file still loads, so `C` can fix it from
inside `ft`.
