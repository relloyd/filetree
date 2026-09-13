package config

// This file is the command set ft ships with — the single source of truth for
// what every key does out of the box.
//
// It used to live in starterTOML, which was written to ~/.filetree/config.toml
// on first run and never again, while Load replaced the command map wholesale
// from that file. Between them those two facts meant a command added to a new
// release could never reach anyone who had already run ft once. Keeping the
// set here instead means it ships with the binary: rebuild and you have it.
//
// config.toml still has the last word — it can move a key, disable one of
// these, or add commands of its own — but it no longer has to *carry* them.
// See mergeCommands in config.go for how the two are combined.

// Builtin is every command ft ships with, in the order the "?" help lists
// them: editing, then hand-off to panes, then the agent sessions, then the
// tmux pane controls, then git.
//
// Adding one here is all that is needed. Do not add commands to starterTOML —
// TestStarterDefinesNoCommands exists to keep that true.
var Builtin = []Command{
	// Open the selection in helix, taking over this pane until you quit.
	// Enter runs this for files; the "e" key runs it for anything, including
	// directories (helix opens its file picker on a directory). "ctrl+e" runs
	// it from inside the "/" finder against the highlighted row, landing on
	// the matched line of a Grep hit; quitting helix returns you to the finder
	// with the results still up.
	//
	// {paths} is what makes marks work here: space-mark several files and one
	// press of "e" opens them all as buffers in a single helix, in mark order.
	{
		Name: "edit",
		Desc: "open selection (or marks) in helix",
		Run:  `hx {paths}`,
		Mode: ModeInteractive,
		Key:  "e", FinderKey: "ctrl+e",
	},

	// Smart hand-off to a pane beside ft: if helix is running in one, open the
	// file there (:open); if a shell is waiting, type the hx command;
	// otherwise create a split. send-keys types into whatever runs in the
	// pane, so blindly sending "hx ..." a second time would land inside helix
	// as editor keystrokes.
	//
	// The target is chosen by scanning the window rather than by asking tmux
	// for "{last}". A pane opened by the branch below is created with -d and
	// never becomes active, so it never becomes the *last active* pane either
	// — leaving "{last}" pointing back at ft's own pane, whose command is
	// "ft", matching no branch and splitting again. That is one new helix per
	// press until you visit one by hand, which is what made the pane "last"
	// and what made the next press finally work.
	//
	// Helix beats a shell: sending to an editor already open reuses it, where
	// typing "hx" at a shell starts a second one. Being the last-active pane
	// only breaks the tie within a class, so going to a shell and back does
	// not stop the open helix from winning.
	//
	// "ctrl+t" runs it from inside the "/" finder without closing it, so
	// several results can be pushed into panes in one visit. All three
	// branches take {paths}, so marking several files sends the lot: helix's
	// ":open" accepts a list, and both it and the hx binary accept a
	// "path:line" argument, so the same expansion works whichever branch runs.
	{
		Name: "tmux-handoff",
		Desc: "hand off to a pane beside ft",
		Run: `[ -n "$TMUX" ] || { echo "not running inside tmux"; exit 1; }
pick=$(tmux list-panes -F "#{pane_last} #{pane_id} #{pane_current_command}" |
  awk -v self="$TMUX_PANE" '
    $2 == self { next }
    {
      p = 0
      if ($3 == "hx") p = 3
      else if ($3 ~ /^(sh|dash|bash|zsh|fish|ksh|nu)$/) p = 1
      if (p == 0) next
      if ($1 == 1) p++
      if (p > best) { best = p; sel = $2 " " $3 }
    }
    END { if (best) print sel }')
case "${pick##* }" in
  hx)
    # Escape must arrive in its own read: coalesced with ":" it parses as
    # Alt+: and the command text gets typed into the buffer instead.
    tmux send-keys -t "${pick%% *}" Escape
    sleep 0.15
    tmux send-keys -t "${pick%% *}" ":open {paths}" Enter
    ;;
  sh|dash|bash|zsh|fish|ksh|nu)
    tmux send-keys -t "${pick%% *}" C-u "hx {paths}" Enter
    ;;
  *)
    tmux split-window -fdh -l 70% -c {root} "hx {paths}"
    ;;
esac
`,
		Mode: ModeBackground,
		Key:  "t", FinderKey: "ctrl+t",
	},

	// Always open the selection — or everything marked — in helix in a new
	// full-height split at the right edge of the window ("vertical split").
	// Repeatable: each press adds another pane; quitting helix (:q) closes its
	// pane again.
	{
		Name: "helix-vsplit",
		Desc: "helix in a split at the right edge",
		Run:  `tmux split-window -fh -c {root} "hx {paths}"`,
		Mode: ModeBackground,
		Key:  "v",
	},

	// Open a shell in a new pane immediately to the right of the filetree
	// pane, splitting ft's own space. Any existing pane to the right is pushed
	// further right — use this when you want to insert a pane next to ft
	// without disturbing the existing layout.
	{
		Name: "shell-vsplit-adjacent",
		Desc: "shell in a split beside the tree",
		Run:  `tmux split-window -h -c {dir}`,
		Mode: ModeBackground,
		Key:  "N",
	},

	// Open a shell in a new full-height split at the right edge of the window,
	// regardless of what panes already exist there.
	{
		Name: "shell-vsplit",
		Desc: "shell in a split at the right edge",
		Run:  `tmux split-window -fh -c {dir}`,
		Mode: ModeBackground,
		Key:  "n",
	},

	// The same shell, in a popup over the whole window instead of a split —
	// for a command you want to run and dismiss rather than keep beside the
	// tree. The popup runs its own tmux session, so it has its own panes and
	// its own scrollback, and closing that session (exit / ctrl+d) closes the
	// popup.
	//
	// Named after the directory, and "-A", so it is one shell per directory:
	// detach from it (the tmux prefix then "d") and the next press comes back
	// to it with its scrollback and whatever is still running, rather than
	// opening a second one. "T" lists it in the meantime.
	{
		Name: "shell-popup",
		Desc: "shell in a popup over the window",
		Run:  `tmux display-popup -E -w 92% -h 92% "tmux new-session -A -s {prefix}shell/{dirkey} -c {dir}"`,
		Mode: ModeBackground,
		Key:  "alt+n",
	},

	// Agent sessions: a coding agent parked in a *named* tmux session, one per
	// repo and branch, so you can detach and come back to it hours later. "T"
	// lists them; these three keys create or return to one.
	//
	// The name is {session} with the tool on the end —
	// "ft/<repo>/<branch>/<tool>" — and it is what "T" filters on, so these
	// sessions are the only ones in the list. Everything else ft opens (the
	// tree itself, the splits above, the popups) stays unnamed and out of it.
	//
	// "-A" is what makes the key idempotent: it attaches to the session if it
	// is already there and creates it otherwise. Note that it *ignores the
	// command* when the session exists, so "claude" only ever runs on first
	// creation — press "c" again and you are back in the same conversation,
	// not a new one.
	//
	// "claude; exec ${SHELL:-sh}" is what keeps the session alive after the
	// tool stops. Without it, "/exit" or a crash takes the session and its
	// scrollback with it; with it you land in a shell in the same directory,
	// can read what happened, and press up-arrow to run it again.
	//
	// "sh -m" turns job control on, and it is what makes the "T" list
	// readable: a plain "sh -c" keeps itself in the foreground process group,
	// so tmux reports the *shell* as the session's current command whatever is
	// running inside it, and every row would say "sh". With -m the tool gets
	// its own foreground group, so the list shows "claude" while it is working
	// and your shell once it has stopped.
	//
	// {gitroot} rather than {dir}: an agent wants the repository, not
	// whichever subdirectory the cursor happens to be in. It is the repo — or
	// linked worktree — containing the selection, so this does the right thing
	// in the worktrees view, where {root} is the worktrees directory itself.
	//
	// They are interactive so that quitting the popup re-reads the tree and
	// refreshes git status, which is what you want after an agent has spent
	// ten minutes editing files.
	{
		Name: "claude-popup",
		Desc: "Claude Code in a named session",
		Run:  `tmux display-popup -E -d {gitroot} -w 92% -h 92% "tmux new-session -A -s {session}/claude -c {gitroot} sh -mc \"claude; exec \${SHELL:-sh}\""`,
		Mode: ModeInteractive,
		Key:  "c",
	},
	{
		Name: "copilot-popup",
		Desc: "Copilot CLI in a named session",
		Run:  `tmux display-popup -E -d {gitroot} -w 92% -h 92% "tmux new-session -A -s {session}/copilot -c {gitroot} sh -mc \"copilot; exec \${SHELL:-sh}\""`,
		Mode: ModeInteractive,
		Key:  "x",
	},

	// The same scheme without a tool: a named shell for the long-running
	// things that are not agents — a dev server, a test watcher — reachable
	// from the same "T" list rather than lost among anonymous panes.
	{
		Name: "agent-shell",
		Desc: "a named shell session for this repo",
		Run:  `tmux display-popup -E -d {gitroot} -w 92% -h 92% "tmux new-session -A -s {session}/shell -c {gitroot}"`,
		Mode: ModeInteractive,
		Key:  "alt+s",
	},

	// The first command that drives herdr rather than tmux: a shell for the
	// place under the cursor, focused if one is already open and created if not.
	// It sits beside "n" rather than replacing it, so the two can be pressed in
	// turn and compared before anything else moves.
	//
	// Reuse is scoped to the *checkout*, not the repository, which is what makes
	// it worktree-aware: a linked worktree has a root of its own, so a shell in
	// the main checkout never answers for a branch worktree or the other way
	// round. Outside a checkout the selection's own directory is the boundary
	// instead. herdr.Scope and herdr.Pick own the whole rule.
	//
	// {dir} alone, and deliberately not {gitroot}: a template mentioning
	// {gitroot} is refused outside a repository by NeedsRepo, and this has to
	// work in a loose directory too. The checkout is worked out from {dir} in
	// the subcommand, by the same gitx.FindRepoRoot that would have filled
	// {gitroot} in, so nothing is lost by not asking for it here.
	//
	// A subcommand rather than a template of its own: choosing between the open
	// shells needs a list, a process check each and a ranking, which in shell
	// would mean jq and no tests.
	{
		Name: "herdr-shell",
		Desc: "shell for this place, in herdr",
		Run:  `ft herdr-shell {dir}`,
		Mode: ModeBackground,
		Key:  "J",
	},

	// The herdr counterpart of "t": open the selection — or everything marked —
	// in the helix belonging to the place under the cursor, and go to it.
	//
	// Where "t" looks for an editor in the pane beside ft, this looks for the
	// workspace that holds the code and for an editor inside that, so it reaches
	// a helix a whole workspace away. It also moves you, which "t" does not:
	// "t" can stay in the tree because the pane it types into is already on
	// screen, and this one cannot make that promise.
	//
	// {paths} rather than {path}, so marks work here exactly as they do for "e"
	// and "t": mark three files and one helix opens all three. A Grep hit in the
	// finder carries its ":42" through the same expansion, and both "hx" and
	// helix's own ":open" take a path in that form.
	//
	// {dir} comes first and separately because the *place* is what decides which
	// workspace answers, and that is not the same question as which files to
	// open — on a finder row the two can be in different checkouts entirely.
	{
		Name: "herdr-edit",
		Desc: "helix for this place, in herdr",
		Run:  `ft herdr-edit {dir} {paths}`,
		Mode: ModeBackground,
		Key:  "K", FinderKey: "ctrl+r",
	},

	// The herdr counterpart of "L": lazygit for the checkout under the cursor,
	// in a tab of its own rather than a popup.
	//
	// The tmux version keeps one named session per checkout and reattaches it in
	// a popup; this keeps one tab per checkout and goes to it. Same idea in
	// herdr's shape — there is no popup to reattach into, and none is needed
	// when a tab is a keystroke away.
	//
	// {dir} alone. lazygit walks up to find the repository itself, so the
	// selection's directory is enough, and the checkout is what decides which
	// workspace answers. The refusal outside a repository happens in the
	// subcommand rather than through NeedsRepo here, so that it agrees with the
	// scoping about what counts as a checkout.
	{
		Name: "herdr-lazygit",
		Desc: "lazygit for this checkout, in herdr",
		Run:  `ft herdr-lazygit {dir}`,
		Mode: ModeBackground,
		Key:  "alt+g",
	},

	// The herdr counterpart of "M": lazygit filtered to the selected file's
	// history, in a tab of its own.
	//
	// One tab per file, which is what the tmux version does with its per-file
	// session name. Pressing it twice on the same file returns to the view you
	// had; pressing it on another gives that file its own. That falls out of
	// matching on the arguments the pane is running with rather than on the
	// binary alone — "lazygit" and "lazygit -f <file>" are the same program
	// otherwise, and each key would keep answering for the other.
	//
	// {dir} and {path} both, because they answer different questions: the
	// directory decides which workspace this belongs to, and the file decides
	// what to show. On a finder row the two can be in different checkouts.
	{
		Name: "herdr-blame",
		Desc: "this file's history in herdr",
		Run:  `ft herdr-blame {dir} {path}`,
		Mode: ModeBackground,
		Key:  "ctrl+g",
	},

	// Focus the tmux pane to the right of ft, so "ctrl+l" replaces tmux's own
	// "ctrl+b →" for getting back to a split opened above. Silent when there
	// is no pane to the right: select-pane -R simply exits 0 in a single-pane
	// window. The $TMUX guard matters — outside a pane, tmux resolves the
	// command against the most recently used session and would move the focus
	// in an unrelated window.
	//
	// It takes the same chord in the finder, so a search can be left running
	// in one pane while you go and look at something in another.
	{
		Name: "focus-right",
		Desc: "focus the tmux pane to the right",
		Run:  `[ -z "$TMUX" ] || tmux select-pane -R`,
		Mode: ModeBackground,
		Key:  "ctrl+l", FinderKey: "ctrl+l",
	},

	// Narrow ft to a sidebar, or widen it to read by. Both resize the *active*
	// pane, which is ft's whenever you are pressing its keys, and both take
	// the same $TMUX guard as focus-right above: a size change in an unrelated
	// window is just as silent as a focus change, and just as unwelcome.
	{
		Name: "resize-pane-30",
		Desc: "narrow ft's pane to 30%",
		Run:  `[ -z "$TMUX" ] || tmux resize-pane -x 30%`,
		Mode: ModeBackground,
		Key:  "ctrl+j", FinderKey: "ctrl+j",
	},
	{
		Name: "resize-pane-70",
		Desc: "widen ft's pane to 70%",
		Run:  `[ -z "$TMUX" ] || tmux resize-pane -x 70%`,
		Mode: ModeBackground,
		Key:  "ctrl+k", FinderKey: "ctrl+k",
	},

	// Give every pane in the window the same width, side by side — tmux's own
	// "even-horizontal" preset layout, and the undo for a window that has
	// drifted out of shape after a few splits and resizes. Same $TMUX guard as
	// the two above, for the same reason: run outside a pane and tmux would
	// rearrange a window you are not even looking at.
	{
		Name: "tmux-even-panes",
		Desc: "even out the pane widths",
		Run:  `[ -z "$TMUX" ] || tmux select-layout even-horizontal`,
		Mode: ModeBackground,
		Key:  "alt+h", FinderKey: "alt+h",
	},

	// Open lazygit for the repo containing the selection, in a popup. lazygit
	// finds the repo by walking up from its working directory, so tmux has to
	// be told which directory that is: a popup starts in the *session's*
	// working directory — where the session was created — not in the cwd of
	// the shell that asked for it, so a "cd" in front of this would never
	// reach lazygit. "-d" places the popup, "-c" the session inside it ({dir}
	// = the selection itself if it's a dir). The inner command stays in double
	// quotes because {dir} arrives shell-quoted, and single quotes do not
	// nest.
	//
	// It is interactive so that quitting lazygit re-reads the tree and
	// refreshes git status, which is the whole point of having just used it.
	{
		Name: "lazygit-popup",
		Desc: "lazygit for this repo, in a popup",
		Run:  `tmux display-popup -E -d {dir} -w 92% -h 92% "tmux new-session -A -s {prefix}lazygit/{repokey} -c {dir} lazygit"`,
		Mode: ModeInteractive,
		Key:  "L",
	},

	// Blame view for the selected file in lazygit: the same popup as
	// lazygit-popup but with "-f {path}" so lazygit jumps straight into the
	// file's blame / log view. Quitting refreshes git status in the tree, just
	// as the plain popup does.
	{
		Name: "lazygit-blame-popup",
		Desc: "lazygit blame/log for this file",
		Run:  `tmux display-popup -E -d {dir} -w 92% -h 92% "tmux new-session -A -s {prefix}blame/{pathkey} -c {dir} lazygit -f {path}"`,
		Mode: ModeInteractive,
		Key:  "M",
	},

	// Diff the selection — or everything marked — in a popup, working tree
	// against HEAD so staged changes still show after using lazygit above.
	// {paths} is what makes marks work: it is the marked set, oldest first, or
	// the selection when nothing is marked, and "git diff --" takes a list.
	//
	// Like the two popups above it runs its own tmux session, so the diff has
	// real scrollback. That is not a nicety: git pages its output only when a
	// pager is configured, and a plain "git diff" with none simply prints, so
	// without a session the top of a long diff would scroll away with no way
	// back to it.
	//
	// The pause exists only for the cases where nothing else holds the screen
	// — nothing to show, git failing, or a pager of "cat", which is how "no
	// pager at all" reports itself. When a pager does run, quitting it closes
	// the popup, so a diff costs one "q" and no more. The pause takes any key
	// rather than enter, since "q" is already in your fingers from the pager.
	//
	// An untracked file shows nothing: git has no version of it to compare
	// against.
	{
		Name: "git-diff-popup",
		Desc: "diff selection (or marks) vs HEAD",
		Run: `tmux display-popup -E -d {dir} -w 92% -h 92% "tmux new-session -A -s {prefix}diff/{pathkey} -c {dir} \"
hold() {
  echo
  echo '-- press any key to close --'
  stty raw -echo 2>/dev/null
  dd bs=1 count=1 >/dev/null 2>&1
  stty sane 2>/dev/null
}
if git -C {dir} diff --quiet HEAD -- {paths}; then
  echo 'No changes vs HEAD'
  hold
elif ! git -C {dir} diff HEAD -- {paths}; then
  hold
elif git -C {dir} var GIT_PAGER | grep -qx cat; then
  hold
fi
\""
`,
		Mode: ModeInteractive,
		Key:  "alt+d",
	},

	// Diff the two most recently space-marked files in a split (older on the
	// left); the trailing read keeps the pane open until you press Enter.
	{
		Name: "diff",
		Desc: "delta-diff the last two marked files",
		Run:  `tmux split-window -fh "delta {marked1} {marked2}; read x"`,
		Mode: ModeBackground,
		Key:  "D",
	},
}

// DefaultBuiltinCommand is what Enter runs before any config says otherwise.
const DefaultBuiltinCommand = "tmux-handoff"

// builtinCommands is a fresh copy of the catalogue, keyed by name, plus the
// order it was declared in. Copied rather than shared because Load merges the
// user's overrides into it, and a package-level map would carry one process's
// config into the next test.
func builtinCommands() (map[string]Command, []string) {
	byName := make(map[string]Command, len(Builtin))
	order := make([]string, 0, len(Builtin))
	for _, c := range Builtin {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	return byName, order
}
