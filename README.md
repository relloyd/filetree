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
  and forget (tmux hand-off, `open`).
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
| `y` / `Y` | copy absolute / git-relative path |
| `.` | toggle hidden files |
| `i` | toggle gitignored files |
| `R` / `F5` | reload from disk |
| `o` | reveal in Finder |
| `/` | fuzzy find (esc cancels, enter jumps) |
| `a` / `A` | new file / new directory |
| `r` | rename |
| `d` | delete to Trash (confirms) |
| `g` / `G`, `ctrl+u`/`ctrl+d` | top / bottom, half-page |
| `H` | collapse all |
| `C` | edit `~/.filetree/config.toml` in the default editor; config reloads on exit |
| `?` | help |
| `q` | quit |

Keys are remappable in the `[keys]` section of the config; commands may bind
their own keys. The starter binds `e` = open the selection in helix (works
on directories — helix shows its picker), `t` = open in the other tmux pane
via helix, and `s` = prime an `rg` there at the selection's directory. The
tmux commands target the previously-active pane (`{last}`) and fall back to
opening a fresh split when the window doesn't have one yet.

Fuzzy find (`/`) walks the root breadth-first (top-level entries are always
indexed, even in huge roots) and ranks shallow paths and basename matches
above equally-fuzzy deep ones. It matches fuzzy subsequences, not regexps;
include `/` in the query to constrain by path segments.

## Config

See `~/.filetree/config.toml` (created on first run) for command templates,
toggle defaults, and keybinding overrides. Template placeholders are
shell-quoted on substitution; unknown `{tokens}` pass through untouched so
tmux formats like `"{last}"` work.
