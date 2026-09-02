package config

// starterTOML is written to ~/.filetree/config.toml on first run.
//
// It is a starter, not a catalogue. The commands ft ships with live in
// catalogue.go and come with the binary, so this file must never define one:
// anything put here reaches only people whose first run is still ahead of
// them, which is how the agent-session keys went missing for every existing
// install. TestStarterDefinesNoCommands keeps that honest.
//
// What belongs here is what genuinely varies per machine — toggles, paths,
// limits — plus a pointer to the two escape hatches. "?" is the authoritative
// list of keys, since it is generated from the catalogue and cannot go stale.
const starterTOML = `# filetree configuration
#
# Everything here is optional: ft ships with a full set of commands and keys
# built in, and this file only says where you differ from them. Press "?" in ft
# for the current list of keys and what each one does.

[general]
show_hidden = false
show_ignored = true        # gitignored entries are shown greyed-out; "i" toggles
sticky_parents = true      # pin the parents of the top row above the tree, so a
#                          # deeply nested file still shows what it sits inside

# finder_width = "60%"     # while "/" is open, widen ft's pane to this much of
#                          # the window and put it back on the way out. The
#                          # tree reads fine in a sidebar; the finder needs the
#                          # room to show what each result matched. Takes a
#                          # percentage, a column count, or "off". ft only ever
#                          # widens, and never undoes a resize you made yourself

icons = "nerd"             # "nerd" needs a Nerd Font; use "plain" otherwise
watch_debounce_ms = 150
# link_ref = "commit"      # ref for u/U web links: "commit" (permanent) or "branch"
# tmux = "auto"            # "auto": when started outside tmux, ft relaunches itself
#                          # in a new tmux session (needs tmux on PATH) so the
#                          # split, popup, focus and resize commands work.
#                          # "never": run as-is.
#                          # "ft --no-tmux" turns it off for one run.
# fuzzy_max_matches = 1000 # how many "/" results are ranked and kept; raising
#                          # it costs sort time on huge trees, not render time.
#                          # "ctrl+g" in the finder doubles, triples, ... this
#                          # for the rest of the session when you need more
# fuzzy_max_candidates = 50000
#                          # how many paths the "/" walk indexes before it
#                          # stops. Rarely reached with a Type filter set,
#                          # since the filter is applied while walking.
# fuzzy_grep_max_per_file = 5
#                          # matches taken from any one file by the Grep
#                          # field, so a generated file can't fill the list;
#                          # 0 means no limit
# recent_max = 100         # how many opened files "b" remembers per tree
# bookmark_max = 500       # how many line bookmarks a repo keeps ("B")
# bookmark_retention_days = 30
#                          # a bookmark whose file has not been seen for this
#                          # long is dropped; 0 turns ageing out off entirely
# clear_marks_after_command = false
#                          # drop the marked set once a command has acted on
#                          # it. Off by default — opening files is not
#                          # destructive, so the marks are there for the next
#                          # command. Either way "esc" clears them.

# Scratch files: "S" creates an empty YYYYMMDDHH.<extension> here and opens it;
# "s" toggles the scratch view. Defaults shown.
# [scratch]
# dir = "~/.filetree/scratch"
# extension = "md"

# Git worktrees: "W" creates one for the repo containing the selection, laid
# out as <dir>/<repo name>/<branch or pr-N>; "w" toggles the view. Default
# shown.
# [worktrees]
# dir = "~/.filetree/worktrees"

# Agent tmux sessions ("T"), named "<prefix><repo>/<branch>/<tool>". The prefix
# is the only thing the list filters on, so everything else ft opens stays out
# of it; it cannot be empty, since that would match every session on the
# server. Default shown.
# [sessions]
# prefix = "ft/"

# Keys. Every action *and* every command can be moved by name — "?" lists the
# names — and one line is enough:
#
#   [keys]
#   claude-popup = "C"    # move a command off "c"
#   rename       = "f2"
#
# A key belongs to one thing, so moving something onto a key another thing
# already holds means saying where that one goes too. An override that would
# leave something with no key at all is refused rather than obeyed: ft starts
# as usual, says how many clashes it found in the status bar, and lists them at
# the top of "?". The keys the tree navigates with (arrows, hjkl, g/G, enter,
# ctrl+u/ctrl+d, ctrl+c, F5) cannot be taken, nor can shift+enter, which
# re-roots the tree the way ">" does where the terminal reports it (inside
# tmux that needs "set -s extended-keys on").
#
# This header has to be uncommented for anything under it to count: a
# "name = key" line with no [keys] above it belongs to whichever table came
# last in the file.
# [keys]

# Commands. The built-in set needs no configuration — this table is only for
# changing one, switching one off, or adding your own.
#
#   [commands]
#   default  = "tmux-handoff"          # what Enter runs
#   disabled = ["copilot-popup"]       # switch one off and free its key
#
#   [commands.claude-popup]            # override a built-in: only the fields
#   run = "..."                        # you name change, the rest is kept
#
#   [commands.notes]                   # or add one of your own
#   run  = "hx ~/notes/{name}.md"
#   mode = "interactive"               # or "background" (the default)
#   key  = "ctrl+b"
#   desc = "open this file's notes"    # shown in "?"
#
# Command templates may use these placeholders (values are shell-quoted):
#   {path}     absolute path of the selection
#   {paths}    what to act on: every space-marked path, oldest first, or the
#              selection when nothing is marked. Carries the position itself —
#              a lone target becomes "path:42" on a Grep hit, a list is plain —
#              so "hx {paths}" is right whether one file or five are in play
#   {relpath}  path relative to the closest parent git repo
#   {dir}      directory of the selection (the selection itself if a dir)
#   {root}     the tree root filetree was started in
#   {name}     base name of the selection
#   {line}     matched line of a "/" finder Grep hit (1 when there is none)
#   {marked}   all marked paths (space-marked), oldest first
#   {marked1}  second-most-recently marked path ('' if fewer than two)
#   {marked2}  most recently marked path       ('' if fewer than two)
#   {gitroot}  root of the repo — or linked worktree — holding the selection
#   {repo}     basename of the main repo, shared by all of its worktrees
#   {branch}   branch of that checkout, with "/" flattened to "-"
#   {session}  tmux session name for that repo and branch, without a tool on
#              the end: write "-s {session}/claude" and the shell joins them
#   {repokey}  that checkout named in one component: basename plus a hash
#              The five above are empty outside a git repository, and a
#              command that uses any of them is refused there rather than run
#   {prefix}   the [sessions] prefix every session filetree opens carries
#   {dirkey}   {dir} named in one component: basename plus a hash of the path
#   {pathkey}  the same for {path}, for a session that belongs to one file
#              These three work anywhere. They are how a command names a
#              session for a *place* rather than a repo: a popup written as
#              "-s {prefix}shell/{dirkey}" is one session per directory, and
#              "new-session -A" then makes the key that opened it the key
#              that returns to it
# Unknown {tokens} are left alone, so tmux formats like "{last}" still work.
`
