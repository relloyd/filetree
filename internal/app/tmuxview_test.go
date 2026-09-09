package app

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/tmux"
)

// tmuxPickerModel is a finder sitting in the session list, with the rows
// supplied rather than read from a tmux server — every test that is about the
// list itself rather than about talking to tmux uses this.
func tmuxPickerModel(t *testing.T, sessions ...tmux.Session) *Model {
	t.Helper()
	// A real tree root, because the picker's actions run through execCommand,
	// which runs its child in the root directory.
	m := rootedModel(t, t.TempDir())
	m.width = 80
	m.mode = modeFuzzy
	m.finderSrc = srcTmux
	m.input.Blur()
	m.tmuxInput.Focus()
	m.tmuxAll = sessions
	m.sortTmuxSessions()
	return m
}

func session(name string, opts ...func(*tmux.Session)) tmux.Session {
	s := tmux.Session{Name: name, Command: "bash", Activity: time.Unix(1700000000, 0)}
	p, _ := tmux.ParseName(tmux.DefaultPrefix, name)
	s.Kind, s.Repo, s.Branch, s.Tool, s.Slug = p.Kind, p.Repo, p.Branch, p.Tool, p.Slug
	for _, o := range opts {
		o(&s)
	}
	return s
}

func attached(s *tmux.Session) { s.Attached = 1 }
func alerting(s *tmux.Session) { s.Alert = true }
func idleLonger(s *tmux.Session) {
	s.Activity = time.Unix(1600000000, 0)
}

// A session with a bell pending is the one the list exists to surface: Claude
// Code rings it when it is waiting for input, so it sorts above everything
// regardless of how recently it was touched.
func TestTmuxSessionOrder(t *testing.T) {
	m := tmuxPickerModel(t,
		session("ft/agent/a/main/claude", idleLonger),
		session("ft/agent/b/main/claude"),
		session("ft/agent/c/main/copilot", alerting, idleLonger),
	)
	var got []string
	for _, i := range m.tmuxRows {
		got = append(got, m.tmuxAll[i].Name)
	}
	want := []string{"ft/agent/c/main/copilot", "ft/agent/b/main/claude", "ft/agent/a/main/claude"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestTmuxRowsFilter(t *testing.T) {
	m := tmuxPickerModel(t,
		session("ft/agent/filetree/main/claude"),
		session("ft/agent/filetree/feat-x/copilot"),
		session("ft/agent/other/main/claude"),
	)
	if len(m.tmuxRows) != 3 {
		t.Fatalf("empty query should show everything, got %d", len(m.tmuxRows))
	}
	m.tmuxInput.SetValue("copilot")
	m.rebuildTmuxRows()
	if len(m.tmuxRows) != 1 {
		t.Fatalf("got %d rows, want 1", len(m.tmuxRows))
	}
	if s, _ := m.tmuxRow(0); s.Name != "ft/agent/filetree/feat-x/copilot" {
		t.Errorf("row 0 = %q", s.Name)
	}
	// The prefix is on every row and so says nothing; matching on it would
	// make every query match everything.
	m.tmuxInput.SetValue("zzz")
	m.rebuildTmuxRows()
	if len(m.tmuxRows) != 0 {
		t.Errorf("got %d rows for a query that matches nothing", len(m.tmuxRows))
	}

	m.tmuxInput.SetValue("!copilot")
	m.rebuildTmuxRows()
	if len(m.tmuxRows) != 2 {
		t.Fatalf("!copilot = %d rows, want 2", len(m.tmuxRows))
	}
	if s, _ := m.tmuxRow(0); !strings.Contains(s.Name, "claude") {
		t.Errorf("row 0 = %q, want a claude session", s.Name)
	}
}

// finderLen/finderPath/finderAbs are what the shared finder machinery reads;
// a source that does not answer all three scrolls and highlights wrongly.
func TestTmuxFinderRowAccessors(t *testing.T) {
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/claude", func(s *tmux.Session) {
		s.Dir = "/home/u/filetree"
	}))
	if got := m.finderLen(); got != 1 {
		t.Errorf("finderLen = %d, want 1", got)
	}
	if got := m.finderPath(0); got != "ft/agent/filetree/main/claude" {
		t.Errorf("finderPath = %q", got)
	}
	// A session's "path" is where it runs, so a finder_key command against a
	// session row acts on a real directory instead of a name joined to the root.
	if got := m.finderAbs(0); got != "/home/u/filetree" {
		t.Errorf("finderAbs = %q", got)
	}
	if got := m.finderAbs(5); got != "" {
		t.Errorf("out-of-range finderAbs = %q, want empty", got)
	}
}

func TestRenderTmuxRow(t *testing.T) {
	now := time.Unix(1700003600, 0) // an hour after the fixture's activity
	m := tmuxPickerModel(t,
		session("ft/agent/filetree/main/claude", attached, func(s *tmux.Session) { s.Command = "claude" }),
		session("ft/agent/filetree/feat-x/copilot", alerting),
	)
	for i := range m.tmuxRows {
		s := m.tmuxAll[m.tmuxRows[i]]
		line := m.renderTmuxRow(s, nil, i == 0, now)
		if w := rowWidth(line); w > m.width {
			t.Errorf("row %d is %d cells wide, want <= %d: %q", i, w, m.width, rowText(line))
		}
		plain := rowText(line)
		if !strings.Contains(plain, s.Label(tmux.DefaultPrefix)) {
			t.Errorf("row %d = %q, want the label in it", i, plain)
		}
		if !strings.Contains(plain, "1h") {
			t.Errorf("row %d = %q, want the idle age", i, plain)
		}
	}
	// The markers are the whole point of the status column.
	if got := rowText(m.renderTmuxRow(m.tmuxAll[m.tmuxRows[0]], nil, false, now)); !strings.Contains(got, "!") {
		t.Errorf("an alerting session should be marked: %q", got)
	}
	rest := rowText(m.renderTmuxRow(m.tmuxAll[m.tmuxRows[1]], nil, false, now))
	if !strings.Contains(rest, "●") {
		t.Errorf("an attached session should be marked: %q", rest)
	}
}

// A narrow pane must not wrap. The session list keeps one line per row — its
// status block is short, and a second line to hold it would halve a list whose
// whole job is to be scanned — so the label gives up its head and the status
// gives up its tail, and both stay on screen.
func TestRenderTmuxRowNarrow(t *testing.T) {
	m := tmuxPickerModel(t, session("ft/agent/filetree/some-very-long-branch-name/claude"))
	m.width = 24
	line := m.renderTmuxRow(m.tmuxAll[0], nil, true, time.Unix(1700000060, 0))
	if len(line) != 1 {
		t.Fatalf("row is %d lines, want 1: %q", len(line), rowText(line))
	}
	if w := rowWidth(line); w > m.width {
		t.Errorf("width = %d, want <= %d: %q", w, m.width, rowText(line))
	}
	// The label loses its head, not its tail: the tool and the branch are what
	// tell two sessions apart.
	if got := rowText(line); !strings.Contains(got, "claude") {
		t.Errorf("row = %q, want the tool name kept", got)
	}
}

// This tree is a session like any other and shows in its own list, but every
// key that acts on a session refuses it: attaching puts the tree in a popup
// over itself, ctrl+w does the same in a pane beside it, and ctrl+x would kill
// ft where it stands.
func TestPickerWillNotActOnThisTree(t *testing.T) {
	const self = "ft/tree/filetree-1a2b3c4d"
	m := tmuxPickerModel(t, session(self))
	m.tmuxSelf = self

	for _, tc := range []struct {
		key string
		act func() (tea.Model, tea.Cmd)
	}{
		{"enter", m.attachSession},
		{"ctrl+w", m.paneSession},
		{"ctrl+x", m.killSession},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m.mode = modeFuzzy
			_, cmd := tc.act()
			if cmd == nil {
				t.Error("want a note saying why nothing happened")
			}
			if m.mode != modeFuzzy {
				t.Errorf("mode = %v, want the picker still open", m.mode)
			}
			if m.pending != nil {
				t.Errorf("pending = %+v, want nothing staged", m.pending)
			}
		})
	}
}

// Ordering: you are typing in this tree, so by activity alone it would sit at
// the top of its own list for ever — and it is the one row nothing here acts
// on. A bell still outranks it, since that is what the list exists to surface.
func TestThisTreeSortsLast(t *testing.T) {
	const self = "ft/tree/filetree-1a2b3c4d"
	m := tmuxPickerModel(t,
		session(self),
		session("ft/agent/filetree/main/claude", idleLonger),
		session("ft/shell/filetree-1a2b3c4d", idleLonger, alerting),
	)
	m.tmuxSelf = self
	m.sortTmuxSessions()

	var got []string
	for _, i := range m.tmuxRows {
		got = append(got, m.tmuxAll[i].Name)
	}
	want := []string{"ft/shell/filetree-1a2b3c4d", "ft/agent/filetree/main/claude", self}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	// And it says which row it is, in place of a status that would only
	// describe the pane you are looking at.
	line := rowText(m.renderTmuxRow(m.tmuxAll[m.tmuxRows[2]], nil, false, time.Unix(1700000000, 0)))
	if !strings.Contains(line, "this tree") {
		t.Errorf("row = %q, want it marked as this tree", line)
	}
}

// The picker's keys must fire only in the picker: ctrl+w is textinput's
// delete-word-backward everywhere else, and taking it globally would break
// editing in the other finder views.
func TestTmuxPickerKeysAreScoped(t *testing.T) {
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/claude"))
	m.finderSrc = srcBookmark
	m.tmuxInput.Blur()
	m.bmInput.Focus()
	m.bmInput.SetValue("one two")
	m.bmInput.SetCursor(len("one two"))
	m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := m.bmInput.Value(); got != "one " {
		t.Errorf("bookmark query = %q, want ctrl+w to have deleted a word", got)
	}
	if m.mode != modeFuzzy {
		t.Errorf("mode = %v, want the bookmark view still open", m.mode)
	}

	// In the session list the same chord switches the client instead, and so
	// must not reach the query field.
	m.finderSrc = srcTmux
	m.bmInput.Blur()
	m.tmuxInput.Focus()
	m.tmuxInput.SetValue("one two")
	m.tmuxInput.SetCursor(len("one two"))
	m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := m.tmuxInput.Value(); got != "one two" {
		t.Errorf("session query = %q, want ctrl+w to have been taken by the picker", got)
	}
}

func TestKillDetachedSessionNeedsNoConfirmation(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux is not installed")
	}
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/claude"))
	if _, cmd := m.killSession(); cmd == nil {
		t.Error("killing a detached session should report what it did")
	}
	if m.mode == modeConfirm {
		t.Error("a detached session should not ask first")
	}
}

// A session someone else has open may be on another terminal entirely, so this
// one asks — and answering returns to the picker rather than to the tree,
// because pruning a list is rarely a single press.
func TestKillAttachedSessionConfirms(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux is not installed")
	}
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/claude", attached))
	m.killSession()
	if m.mode != modeConfirm || m.pending == nil || m.pending.kind != opKillSession {
		t.Fatalf("mode = %v, pending = %+v, want a kill confirmation", m.mode, m.pending)
	}
	if m.pending.session != "ft/agent/filetree/main/claude" {
		t.Errorf("pending session = %q", m.pending.session)
	}
	if got := plainText(m.renderConfirm()); !strings.Contains(got, "filetree/main/claude") {
		t.Errorf("confirm prompt = %q, want the session named", got)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.mode != modeFuzzy || m.pending != nil {
		t.Errorf("after n: mode = %v, pending = %+v, want back in the picker", m.mode, m.pending)
	}

	m.killSession()
	m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.mode != modeFuzzy || m.pending != nil {
		t.Errorf("after y: mode = %v, pending = %+v, want back in the picker", m.mode, m.pending)
	}
}

// Enter leaves the picker, because attaching takes over the screen; coming
// back from the popup should land in the tree, not in a stale list.
func TestAttachLeavesThePicker(t *testing.T) {
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/claude"))

	_, cmd := m.attachSession()
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the picker closed", m.mode)
	}
	if cmd == nil {
		t.Fatal("enter should have produced a command to run")
	}
	// The command it runs is the popup attach, exact-matched so a session name
	// cannot be treated as a prefix of a longer one.
	want := tmux.AttachPopup("ft/agent/filetree/main/claude", config.ShellQuote)
	if !strings.Contains(want, "attach-session -t '=ft/agent/filetree/main/claude'") &&
		!strings.Contains(want, "attach-session -t =ft/agent/filetree/main/claude") {
		t.Errorf("AttachPopup = %q, want an exact -t target", want)
	}
}

// display-popup blocks its caller until the popup closes — measured, not
// assumed: "display-popup -E 'sleep 4'" takes four seconds to return. So
// attaching runs interactively, suspending ft for as long as the agent session
// is on screen and re-reading the tree and git status when you detach, exactly
// as the lazygit popups do. That refresh is the point: the agent has usually
// been editing files.
//
// This briefly ran in the background, on the theory that display-popup returned
// immediately and Bubble Tea was redrawing over the popup. It does not, and it
// was not — the popup was closing because zsh mangled the "=" target, which
// ShellQuote now quotes.
func TestAttachRunsInteractively(t *testing.T) {
	m := tmuxPickerModel(t, session("ft/agent/filetree/main/copilot"))

	_, cmd := m.attachSession()
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	// tea.ExecProcess returns its own message type rather than running the
	// child inline, which is what suspending the TUI looks like from here.
	if _, ranInline := cmd().(cmdDoneMsg); ranInline {
		t.Error("attach ran as a background command; it must suspend the TUI")
	}
}

// An empty list must not act on a row that is not there.
func TestPickerActionsOnEmptyList(t *testing.T) {
	m := tmuxPickerModel(t)
	if _, cmd := m.attachSession(); cmd != nil {
		t.Error("enter on an empty list should do nothing")
	}
	if _, cmd := m.paneSession(); cmd != nil {
		t.Error("ctrl+w on an empty list should do nothing")
	}

	if _, cmd := m.killSession(); cmd != nil {
		t.Error("ctrl+x on an empty list should do nothing")
	}
	if m.mode != modeFuzzy {
		t.Errorf("mode = %v, want the picker still open", m.mode)
	}
}

// The session variables come off the repo containing the selection, and the
// repo component is the *main* repo so every worktree of a project files under
// one name.
func TestRepoIdentAndSessionName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	mustGit(t, "", "init", "-b", "main", repo)
	mustGit(t, repo, "commit", "--allow-empty", "-m", "one")

	m := finderModel()
	m.cfg.Sessions.Prefix = tmux.DefaultPrefix
	id := m.repoIdentFor(repo)
	if !id.ok() {
		t.Fatalf("repoIdentFor(%q) = %+v, want a resolved identity", repo, id)
	}
	if id.Repo != "proj" || id.Branch != "main" || id.GitRoot != repo {
		t.Errorf("ident = %+v", id)
	}
	if got := m.sessionNameFor(repo, "claude"); got != "ft/agent/proj/main/claude" {
		t.Errorf("sessionNameFor = %q", got)
	}

	// A branch with a slash flattens, keeping the name at four components.
	mustGit(t, repo, "checkout", "-q", "-b", "claude/tmux-nav")
	m.repoRoots = map[string]string{} // drop the memoised lookup
	m.branches = map[string]string{}
	if got := m.sessionNameFor(repo, "claude"); got != "ft/agent/proj/claude-tmux-nav/claude" {
		t.Errorf("slashed branch = %q", got)
	}

	// Outside a repository there is no name to build.
	if got := m.sessionNameFor(root, "claude"); got != "" {
		t.Errorf("outside a repo = %q, want empty", got)
	}
}

// A linked worktree files under the main repo's name, not under its own
// directory name — which is the branch, and would scatter one project across
// as many "repos" as it has worktrees.
func TestRepoIdentUsesMainRepoForWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	mustGit(t, "", "init", "-b", "main", repo)
	mustGit(t, repo, "commit", "--allow-empty", "-m", "one")
	wt := filepath.Join(root, "feat-x")
	mustGit(t, repo, "worktree", "add", "-b", "feat-x", wt)

	m := finderModel()
	id := m.repoIdentFor(wt)
	if id.Repo != "proj" {
		t.Errorf("repo = %q, want proj (the main repo, not the worktree dir)", id.Repo)
	}
	if id.GitRoot != wt {
		t.Errorf("gitroot = %q, want the worktree %q — that is where the agent runs", id.GitRoot, wt)
	}
	if id.Branch != "feat-x" {
		t.Errorf("branch = %q", id.Branch)
	}
}

// A command naming {session} outside a repository is refused rather than run:
// expanding it there would make a session the picker could never find.
func TestRepoCommandRefusedOutsideRepo(t *testing.T) {
	root := t.TempDir()
	m := rootedModel(t, root)
	m.mode = modeNormal
	m.cfg.Commands = map[string]config.Command{
		"claude-popup": {Run: "tmux new-session -A -s {session}/claude -c {gitroot}", Mode: config.ModeBackground},
	}
	_, cmd := m.runCommand("claude-popup")
	if !m.statusErr || !strings.Contains(m.statusMsg, "git repo") {
		t.Errorf("status = %q (err=%v), want a refusal naming the missing repo", m.statusMsg, m.statusErr)
	}
	// Nothing should have been executed: the only command back is the status
	// timer, never a cmdDoneMsg.
	if msg, ok := runCmd(cmd, 500*time.Millisecond); ok {
		if _, ran := msg.(cmdDoneMsg); ran {
			t.Error("the command should not have run")
		}
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	if dir != "" {
		c.Dir = dir
	}
	c.Env = append(c.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
	)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// Outside tmux there is no window to look in, so both keys have to say so
// rather than shell out and fail. ft runs outside tmux whenever "tmux = never"
// is set or "--no-tmux" was passed, so this is an ordinary state, not an edge.
func TestPaneKeysRefuseOutsideTmux(t *testing.T) {
	m := tmuxPickerModel(t)
	m.selfPane = ""

	m.mode = modeNormal
	if _, cmd := m.detachAgentPane(); cmd == nil {
		t.Error("X outside tmux should report, not stay silent")
	}
	// It must not have gone looking for panes either: with no self there is
	// nothing to compare against, and PaneShowing says so without calling tmux.
	if _, _, ok := m.paneShowing(func(string) bool { return true }); ok {
		t.Error("found a pane while outside tmux")
	}
}

// A tree with no agent beside it is the normal case, not a failure: X should
// note it and change nothing.
func TestDetachWithNoAgentPaneIsQuiet(t *testing.T) {
	m := tmuxPickerModel(t)
	m.mode = modeNormal
	m.selfPane = "%11" // a pane tmux will not know about on this server
	if _, cmd := m.detachAgentPane(); cmd == nil {
		t.Error("X with nothing attached should still say so")
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
}
