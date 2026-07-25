# ft — terminal file tree explorer

A JetBrains-style project pane for a terminal split. Built with Go and
Bubble Tea for macOS (Linux-ready via `internal/platform` build tags).

- Remembers expanded/collapsed dirs, selection, and scroll **per root
  directory** across restarts (`~/.filetree/state/`).
- Git aware: status colours (modified, staged, untracked, conflict), greyed
  gitignored entries, and a `•` marker on dirs containing changes.
- Hot reload: expanded directories are watched (fsnotify); external changes
  appear automatically. `R`/F5 forces a full reload.
- Copy the selection's path as **absolute** (`y`) or **relative to the
  closest parent git repo** (`Y`) — ready to paste into helix, rg, or fd.
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
- Mouse: wheel scrolls, click selects, chevron-click/double-click expands,
  double-click on a file runs the default command, header toggles are
  clickable.
- Nerd Font file-type icons (set `icons = "plain"` in the config otherwise).

## Install

```sh
go install ./cmd/ft
ft [dir]   # defaults to the current directory
```

First run writes a commented starter config to `~/.filetree/config.toml`.

## Keys

| Key | Action |
|---|---|
| `↑`/`k` `↓`/`j` | move selection |
| `←`/`h` | collapse, or jump to parent |
| `→`/`l` | expand, or step into first child |
| `enter` | file: run default command · dir: toggle |
| `space` | mark/unmark the selection (and move down) |
| `esc` | clear all marks (without collapsing) |
| `p` / `m` | copy / move marked items into the selected dir (or the selected file's parent); conflicts prompt overwrite-to-Trash vs keep-both |
| `y` / `Y` | copy absolute / git-relative path |
| `.` | toggle hidden files |
| `i` | toggle gitignored files |
| `R` / `F5` | reload from disk |
| `o` | reveal in Finder |
| `/` | fuzzy find (esc cancels, enter jumps) |
| `a` / `A` | new file / new directory |
| `r` | rename |
| `d` | delete marked items — or the selection if none — to Trash (confirm names what's deleted) |
| `g` / `G`, `ctrl+u`/`ctrl+d` | top / bottom, half-page |
| `H` | collapse all (also clears marks) |
| `C` | edit `~/.filetree/config.toml` in the default editor; config reloads on exit |
| `?` | help |
| `q` | quit |

Keys are remappable in the `[keys]` section of the config. Commands may bind
their own keys; the starter config binds:

| Key | Command |
|---|---|
| `e` | open the selection in helix — works on directories too (helix shows its file picker) |
| `t` | smart hand-off to the previously-active tmux pane: opens the file in the helix already running there (`:open`), types the `hx` command if a shell is waiting, or creates a split otherwise |
| `v` | open the selection in helix in a new full-height split at the right edge — repeatable, one pane per press |
| `s` | prime an `rg` in the other tmux pane at the selection's directory |
| `L` | open lazygit for the repo containing the selection (commented example in the starter) |

Fuzzy find (`/`) matches fuzzy subsequences, not regexps; include `/` in
the query to constrain by path segments. Ranking is screen-aware: entries
currently visible in the tree outrank everything else, then shallow paths
and basename matches beat equally-fuzzy deep ones. With an empty query the
list shows exactly the visible tree entries in order, so `/` + cursor keys
doubles as a quick jump list. Candidates come from a breadth-first walk, so
top-level entries are always indexed even in huge roots.

## Config

See `~/.filetree/config.toml` (created on first run) for command templates,
toggle defaults, and keybinding overrides. Template placeholders are
shell-quoted on substitution; unknown `{tokens}` pass through untouched so
tmux formats like `"{last}"` work.
