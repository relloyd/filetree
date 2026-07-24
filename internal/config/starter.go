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
# Unknown {tokens} are left alone, so tmux formats like "{last}" still work.

[general]
show_hidden = false
show_ignored = true        # gitignored entries are shown greyed-out; "i" toggles
icons = "nerd"             # "nerd" needs a Nerd Font; use "plain" otherwise
watch_debounce_ms = 150

[commands]
default = "edit"           # the command Enter runs

# Open the selection in helix, taking over this pane until you quit.
# Enter runs this for files; the "e" key runs it for anything, including
# directories (helix opens its file picker on a directory).
[commands.edit]
run = "hx {path}"
mode = "interactive"
key = "e"

# Smart hand-off to the previously-active tmux pane ("{last}"): if helix is
# running there, open the file in that session (:open); if a shell is
# waiting, type the hx command; otherwise (including no last pane) create a
# split. send-keys types into whatever runs in the pane, so blindly sending
# "hx ..." a second time would land inside helix as editor keystrokes.
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
    tmux send-keys -t "{last}" C-u "hx {path}" Enter
    ;;
  *)
    tmux split-window -fh -c {root} "hx {path}"
    ;;
esac
'''
mode = "background"
key = "t"

# Always open the selection in helix in a new full-height split at the
# right edge of the window ("vertical split"). Repeatable: each press adds
# another pane; quitting helix (:q) closes its pane again.
[commands.vsplit]
run = 'tmux split-window -fh -c {root} "hx {path}"'
mode = "background"
key = "v"

# Prime a ripgrep at the selection's directory in the other tmux pane:
# the search path is filled in and the cursor waits where the pattern goes.
# Falls back to a fresh shell split at that directory.
[commands.grep-here]
run = 'tmux send-keys -t "{last}" "rg -n {dir} -e " 2>/dev/null || tmux split-window -h -c {dir}'
mode = "background"
key = "s"

# More examples — uncomment what you use:
#
# Open lazygit for the repo containing the selection (finds the repo by
# walking up from {dir}, so it works with a file selected too).
# [commands.lazygit]
# run = "cd {dir} && lazygit"
# mode = "interactive"
# key = "L"

# Keybinding overrides. Defaults shown; uncomment to change.
# [keys]
# quit = "q"
# toggle-hidden = "."
# toggle-ignored = "i"
# reload = "R"
# reveal = "o"
# copy-abs = "y"
# copy-rel = "Y"
# fuzzy = "/"
# new-file = "a"
# new-dir = "A"
# rename = "r"
# delete = "d"
# collapse-all = "H"
# edit-config = "C"
# help = "?"
`
