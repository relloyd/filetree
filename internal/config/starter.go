package config

// starterTOML is written to ~/.filetree/config.toml on first run.
const starterTOML = `# filetree configuration
#
# Command templates may use these placeholders (values are shell-quoted):
#   {path}     absolute path of the selection
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
#                          # t/v/n/r split commands below work. "never": run as-is.
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
watch_debounce_ms = 150

# The "/" finder has three input lines; "tab" cycles them.
#   Find:  fuzzy subsequence over paths (include "/" to pin path segments)
#   Type:  comma-separated file-type globs, applied while walking, so a
#          filtered search reaches the leaves of a tree too big to index whole
#          hcl              *.hcl, or a file named exactly "hcl"
#          .hcl             *.hcl
#          terragrunt.hcl   that basename, anywhere in the tree
#          *.tf             matched against the basename
#          infra/**/*.hcl   matched against the whole path ("**" spans dirs)
#          !vendor/**       a leading "!" excludes
#   Grep:  a regexp searched inside the files Type selected (needs ripgrep).
#          Rows become "path:line  matched text"; enter still jumps to the
#          file. Type + Grep together are the "fd ... | rg ..." combination.
#          With Type or Grep filled in, the status bar shows the rg command
#          the finder amounts to ("rg --files ..." when only Type is set);
#          "ctrl+y" copies it so you can run it yourself.
# With a Type filter and an empty Find, the list is every file of that type.

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
[commands.edit]
run = "hx {path}:{line}"
mode = "interactive"
key = "e"
finder_key = "ctrl+e"

# Smart hand-off to the previously-active tmux pane ("{last}"): if helix is
# running there, open the file in that session (:open); if a shell is
# waiting, type the hx command; otherwise (including no last pane) create a
# split. send-keys types into whatever runs in the pane, so blindly sending
# "hx ..." a second time would land inside helix as editor keystrokes.
# "ctrl+t" runs it from inside the "/" finder without closing it, so several
# results can be pushed into panes in one visit. Only the two branches that
# invoke the hx binary take ":{line}" — the ":open" branch is a helix command,
# not a shell one, and does not document the path:line form.
[commands.handoff]
run = '''
target=$(tmux display-message -p -t "{last}" "#{pane_current_command}" 2>/dev/null)
case "$target" in
  hx)
    # Escape must arrive in its own read: coalesced with ":" it parses as
    # Alt+: and the command text gets typed into the buffer instead.
    tmux send-keys -t "{last}" Escape
    sleep 0.15
    tmux send-keys -t "{last}" ":open {path}" Enter
    ;;
  sh|dash|bash|zsh|fish|ksh|nu)
    tmux send-keys -t "{last}" C-u "hx {path}:{line}" Enter
    ;;
  *)
    tmux split-window -fh -c {root} "hx {path}:{line}"
    ;;
esac
'''
mode = "background"
key = "t"
finder_key = "ctrl+t"

# Always open the selection in helix in a new full-height split at the
# right edge of the window ("vertical split"). Repeatable: each press adds
# another pane; quitting helix (:q) closes its pane again.
[commands.vsplit]
run = 'tmux split-window -fh -c {root} "hx {path}"'
mode = "background"
key = "v"

# Open a shell in a new full-height split at the right edge, in the
# selection's directory ({dir} = the selection itself if it's a dir).
[commands.shell-here]
run = 'tmux split-window -fh -c {dir}'
mode = "background"
key = "n"

# Prime a ripgrep at the selection's directory in the other tmux pane:
# the search path is filled in and the cursor waits where the pattern goes.
# Falls back to a fresh shell split at that directory.
[commands.grep-here]
run = 'tmux send-keys -t "{last}" "rg -n {dir} -e " 2>/dev/null || tmux split-window -h -c {dir}'
mode = "background"
key = "r"

# More examples — uncomment what you use:
#
# Open lazygit for the repo containing the selection (finds the repo by
# walking up from {dir}, so it works with a file selected too).
# [commands.lazygit]
# run = "cd {dir} && lazygit"
# mode = "interactive"
# key = "L"
#
# Diff the two most recently space-marked files in a split (older on the
# left); the trailing read keeps the pane open until you press Enter.
# [commands.diff]
# run = 'tmux split-window -fh "delta {marked1} {marked2}; read x"'
# mode = "background"
# key = "D"

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
# finder-clear = "ctrl+l"          # empty all three finder fields
# finder-resume = "f"              # reopen the finder where you left it
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
