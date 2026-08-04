package config

// starterTOML is written to ~/.filetree/config.toml on first run.
const starterTOML = `# filetree configuration
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
# Unknown {tokens} are left alone, so tmux formats like "{last}" still work.

[general]
show_hidden = false
show_ignored = true        # gitignored entries are shown greyed-out; "i" toggles
icons = "nerd"             # "nerd" needs a Nerd Font; use "plain" otherwise
# link_ref = "commit"      # ref for u/U web links: "commit" (permanent) or "branch"
# tmux = "auto"            # "auto": when started outside tmux, ft relaunches itself
#                          # in a new tmux session (needs tmux on PATH) so the
#                          # split, popup, focus and resize commands below work.
#                          # "never": run as-is.
#                          # "ft --no-tmux" turns it off for one run.
# fuzzy_max_matches = 1000 # how many "/" results are ranked and kept; raising
#                          # it costs sort time on huge trees, not render time.
#                          # "ctrl+g" in the fuzzy finder doubles, triples, ... this
#                          # for the rest of the session when you need more
# fuzzy_max_candidates = 50000
#                          # how many paths the "/" walk indexes before it
#                          # stops. Rarely reached with a Type filter set,
#                          # since the filter is applied while walking.
# fuzzy_grep_max_per_file = 5
#                          # matches taken from any one file by the Grep
#                          # field, so a generated file can't fill the list;
#                          # 0 means no limit
# recent_max = 100         # how many opened files "b" remembers per tree.
#                          # Kept in <root>.recent.json beside the tree's
#                          # state file, so each tree has its own history.
# bookmark_max = 500       # how many line bookmarks a repo keeps ("B").
# bookmark_retention_days = 30
#                          # a bookmark whose file has not been seen for this
#                          # long is dropped. Long enough that a file hidden by
#                          # a branch switch is still there when you switch
#                          # back; 0 turns ageing out off entirely.
# clear_marks_after_command = false
#                          # drop the marked set once a command has acted on
#                          # it. Off by default — opening files is not
#                          # destructive, so the marks are there for the next
#                          # command ("e" to open them, then "t" to push them
#                          # to a pane). Turn it on to match d/p/m, which do
#                          # clear. Either way "esc" clears them.
watch_debounce_ms = 150

# The "/" finder has three input lines; "tab" cycles them in this order, so
# one "tab" from Find reaches Grep.
#   Find:  fuzzy subsequence over paths (include "/" to pin path segments)
#   Grep:  a regexp searched inside the files the Type filter below selects
#          (needs ripgrep). Rows become "path:line  matched text"; enter still
#          jumps to the file. Type + Grep together are the "fd ... | rg ..."
#          combination. With Type or Grep filled in, the status bar shows the
#          rg command the finder amounts to ("rg --files ..." when only Type
#          is set); "ctrl+y" copies it so you can run it yourself.
#   Type:  comma-separated file-type globs, applied while walking, so a
#          filtered search reaches the leaves of a tree too big to index whole
#          hcl              *.hcl, or a file named exactly "hcl"
#          .hcl             *.hcl
#          terragrunt.hcl   that basename, anywhere in the tree
#          *.tf             matched against the basename
#          infra/**/*.hcl   matched against the whole path ("**" spans dirs)
#          !vendor/**       a leading "!" excludes
# With a Type filter and an empty Find, the list is every file of that type.

# "B" opens the same finder over line bookmarks — a file and a line, captured
# from your editor. Bind a key in helix to record one:
#
#   [keys.normal.space]
#   b = ":sh ft bookmark %{buffer_name} %{cursor_line}"
#
# Do not add %{selection}: it works with a word selected and silently does
# nothing at all when the selection spans lines. ft reads the line's text
# itself, so the list still shows real content either way.
#
# Bookmarks belong to the repository, not to this tree, so every worktree of it
# shares one list. tab sorts by recency or path, ctrl+s widens to every
# project, ctrl+x forgets one, and enter opens the file at its line — following
# the line if it has moved since.

# "b" opens the same finder over the files you have opened from this tree,
# newest first, with how long ago beside each one. It has the one Find line —
# there is no directory for ripgrep to search across scattered files — and
# enter reveals the file in the tree and opens it. A command's finder_key
# works here too, so "ctrl+t" hands a remembered file straight to a pane.

# Scratch files: "S" creates an empty YYYYMMDDHH.<extension> file here and
# opens it in the default command; "s" toggles the scratch view; Esc (with
# no marks active) returns to the original root. Defaults shown.
# [scratch]
# dir = "~/.filetree/scratch"
# extension = "md"

# Git worktrees: "W" asks for a branch name or PR number and creates a
# worktree for the repo containing the selection, then jumps into it; "w"
# toggles the worktrees view; Esc (with no marks active) returns. "d" on a
# worktree root runs "git worktree remove" instead of trashing it. Layout is
# <dir>/<repo name>/<branch or pr-N>. Default shown.
# [worktrees]
# dir = "~/.filetree/worktrees"

[commands]
default = "edit"           # the command Enter runs

# Open the selection in helix, taking over this pane until you quit.
# Enter runs this for files; the "e" key runs it for anything, including
# directories (helix opens its file picker on a directory). "ctrl+e" runs it
# from inside the "/" finder against the highlighted row, landing on the
# matched line of a Grep hit; quitting helix returns you to the finder with
# the results still up.
# {paths} is what makes marks work here: space-mark several files and one
# press of "e" opens them all as buffers in a single helix, in mark order.
[commands.edit]
run = "hx {paths}"
mode = "interactive"
key = "e"
finder_key = "ctrl+e"

# Smart hand-off to the previously-active tmux pane ("{last}"): if helix is
# running there, open the file in that session (:open); if a shell is
# waiting, type the hx command; otherwise (including no last pane) create a
# split. send-keys types into whatever runs in the pane, so blindly sending
# "hx ..." a second time would land inside helix as editor keystrokes.
# "ctrl+t" runs it from inside the "/" finder without closing it, so several
# results can be pushed into panes in one visit. All three branches take
# {paths}, so marking several files sends the lot: helix's ":open" accepts a
# list, and both it and the hx binary accept a "path:line" argument, so the
# same expansion works whichever branch runs.
[commands.tmux-handoff]
run = '''
target=$(tmux display-message -p -t "{last}" "#{pane_current_command}" 2>/dev/null)
case "$target" in
  hx)
    # Escape must arrive in its own read: coalesced with ":" it parses as
    # Alt+: and the command text gets typed into the buffer instead.
    tmux send-keys -t "{last}" Escape
    sleep 0.15
    tmux send-keys -t "{last}" ":open {paths}" Enter
    ;;
  sh|dash|bash|zsh|fish|ksh|nu)
    tmux send-keys -t "{last}" C-u "hx {paths}" Enter
    ;;
  *)
    tmux split-window -fh -c {root} "hx {paths}"
    ;;
esac
'''
mode = "background"
key = "t"
finder_key = "ctrl+t"

# Always open the selection — or everything marked — in helix in a new
# full-height split at the right edge of the window ("vertical split").
# Repeatable: each press adds another pane; quitting helix (:q) closes its
# pane again.
[commands.helix-vsplit]
run = 'tmux split-window -fh -c {root} "hx {paths}"'
mode = "background"
key = "v"

# Open a shell in a new pane immediately to the right of the filetree pane,
# splitting ft's own space. Any existing pane to the right is pushed further
# right — use this when you want to insert a pane next to ft without
# disturbing the existing layout.
[commands.shell-vsplit-adjacent]
run = 'tmux split-window -h -c {dir}'
mode = "background"
key = "N"

# Open a shell in a new full-height split at the right edge of the window,
# regardless of what panes already exist there.
[commands.shell-vsplit]
run = 'tmux split-window -fh -c {dir}'
mode = "background"
key = "n"

# The same shell, in a popup over the whole window instead of a split — for
# a command you want to run and dismiss rather than keep beside the tree.
# The popup runs its own tmux session, so it has its own panes and its own
# scrollback, and closing that session (exit / ctrl+d) closes the popup.
[commands.shell-popup]
run = 'tmux display-popup -E -w 92% -h 92% "tmux new-session -c {dir}"'
mode = "background"
key = "P"

# Prime a ripgrep at the selection's directory in the other tmux pane:
# the search path is filled in and the cursor waits where the pattern goes.
# Falls back to a fresh shell split at that directory.
[commands.grep-here]
run = 'tmux send-keys -t "{last}" "rg -n {dir} -e " 2>/dev/null || tmux split-window -h -c {dir}'
mode = "background"
key = "r"

# Focus the tmux pane to the right of ft, so "ctrl+l" replaces tmux's own
# "ctrl+b →" for getting back to a split opened above. Silent when there is
# no pane to the right: select-pane -R simply exits 0 in a single-pane
# window. The $TMUX guard matters — outside a pane, tmux resolves the command
# against the most recently used session and would move the focus in an
# unrelated window.
# It takes the same chord in the finder, so a search can be left running in
# one pane while you go and look at something in another.
[commands.focus-right]
run = '[ -z "$TMUX" ] || tmux select-pane -R'
mode = "background"
key = "ctrl+l"
finder_key = "ctrl+l"

# Narrow ft to a sidebar, or widen it to read by. Both resize the *active*
# pane, which is ft's whenever you are pressing its keys, and both take the
# same $TMUX guard as focus-right above: a size change in an unrelated
# window is just as silent as a focus change, and just as unwelcome.
[commands.resize-pane-30]
run = '[ -z "$TMUX" ] || tmux resize-pane -x 30%'
mode = "background"
key = "ctrl+j"
finder_key = "ctrl+j"

[commands.resize-pane-80]
run = '[ -z "$TMUX" ] || tmux resize-pane -x 80%'
mode = "background"
key = "ctrl+k"
finder_key = "ctrl+k"

# Open lazygit for the repo containing the selection, in a popup. lazygit
# finds the repo by walking up from its working directory, so cd to the
# selection's directory ({dir} = the selection itself if it's a dir).
[commands.lazygit-popup]
run = "cd {dir} && tmux display-popup -E -w 92% -h 92% 'tmux new-session lazygit'"
mode = "background"
key = "L"

# Diff the two most recently space-marked files in a split (older on the
# left); the trailing read keeps the pane open until you press Enter.
[commands.diff]
run = 'tmux split-window -fh "delta {marked1} {marked2}; read x"'
mode = "background"
key = "D"

# Keybinding overrides. Defaults shown; uncomment to change.
# [keys]
# quit = "q"
# toggle-hidden = "."
# toggle-ignored = "i"
# reload = ""              # unbound: F5 always reloads; set a key to add one
# reveal = "o"
# copy-abs = "y"
# copy-rel = "Y"
# fuzzy = "/"
# fuzzy-here = "F"                 # the finder, confined to the selected dir
# finder-next-field = "tab"        # move between the "/" finder's input lines
# finder-prev-field = "shift+tab"
# finder-more = "ctrl+g"           # raise fuzzy_max_matches for this session
# finder-copy-command = "ctrl+y"   # copy the rg command behind Type/Grep
# finder-clear = "ctrl+o"          # empty the finder fields
# finder-resume = "f"              # reopen the finder where you left it
# recent = "b"                     # the finder over recently opened files
# bookmarks = "B"                  # the finder over this repo's line bookmarks
# new-file = "a"
# new-dir = "A"
# rename = "R"
# delete = "d"
# mark = "space"
# clear-marks = "esc"
# copy-here = "p"
# move-here = "m"
# copy-url = "u"
# open-url = "U"
# scratch = "s"
# scratch-new = "S"
# worktrees = "w"
# worktree-new = "W"
# collapse-all = "H"
# edit-config = "C"
# help = "?"
`
