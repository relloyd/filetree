package app

import (
	"os"

	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/tmux"
)

// The picker's own commands are given names so that a failure reads as
// "claude-popup: ..." would — execCommand and handleCmdDone label their
// messages with whatever they are handed.
const (
	cmdAttachSession = "attach-session"
)

// startTmuxSessions opens the finder over the tmux sessions ft owns. It is the
// same finder — same query field, ranking, scrolling and keys — over a
// different supply of rows, exactly as the recent and bookmark views are.
func (m *Model) startTmuxSessions() (tea.Model, tea.Cmd) {
	m.finderSrc = srcTmux
	m.finderField = fieldQuery
	m.resumeWant = finderPick{}
	m.scopeDir = ""
	m.loadTmuxSessions()
	return m.enterFuzzy()
}

// loadTmuxSessions re-reads the session list. Re-reading rather than caching
// is the only honest option: the sessions belong to the tmux server, and every
// other ft on the machine — and the user, at a shell — can add and remove them
// while this one is running.
func (m *Model) loadTmuxSessions() {
	m.tmuxAll, m.tmuxRows, m.tmuxMatched = nil, nil, nil
	m.tmuxErr = ""
	sessions, err := tmux.List(m.sessionPrefix())
	if err != nil {
		m.tmuxErr = err.Error()
	}
	m.tmuxAll = sessions
	// Read alongside the list rather than remembered from startup: it is one
	// more call on a key press that already makes one, and it stays right
	// through a rename. Empty outside tmux, where no row can be us.
	m.tmuxSelf = tmux.SelfSession(m.selfPane)
	m.sortTmuxSessions()
}

// isSelf reports whether a row is the session this tree is running in. Every
// key that would act on a session asks first: attaching to ourselves shows the
// tree inside its own popup, a pane does the same beside it, and killing it
// takes ft with it.
func (m *Model) isSelf(s tmux.Session) bool {
	return m.tmuxSelf != "" && s.Name == m.tmuxSelf
}

// noteSelf is the one answer all three of those keys give.
func (m *Model) noteSelf() tea.Cmd {
	return m.note("that session is this tree", true)
}

// sortTmuxSessions puts the sessions in the order they are wanted in: the ones
// asking for attention first, this tree last, and everything else by how
// recently it did anything.
//
// A bell is what Claude Code rings when it wants input, so a session with one
// pending is the one the list exists to surface. Attachment is not part of the
// ordering — a session you already have open somewhere is the one you least
// need to be shown — and this tree is the extreme of that: you are typing in
// it, so by activity it would sit at the top of the list for ever, and it is
// the one row no key here will act on.
func (m *Model) sortTmuxSessions() {
	sort.SliceStable(m.tmuxAll, func(i, j int) bool {
		a, b := m.tmuxAll[i], m.tmuxAll[j]
		if a.Alert != b.Alert {
			return a.Alert
		}
		if sa, sb := m.isSelf(a), m.isSelf(b); sa != sb {
			return sb
		}
		if !a.Activity.Equal(b.Activity) {
			return a.Activity.After(b.Activity)
		}
		return a.Name < b.Name
	})
	m.rebuildTmuxRows()
}

// rebuildTmuxRows applies the query over the same text the row is drawn from,
// so the highlight offsets fuzzy.Find reports land on the right characters.
func (m *Model) rebuildTmuxRows() {
	hay := make([]string, len(m.tmuxAll))
	for i, s := range m.tmuxAll {
		hay[i] = m.tmuxSearchText(s)
	}
	m.tmuxRows, m.tmuxMatched = applyFindQuery(parseFindQuery(m.tmuxInput.Value()), hay)
	m.fuzzySel = clamp(m.fuzzySel, 0, max(0, len(m.tmuxRows)-1))
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, len(m.tmuxRows)-m.fuzzyVisibleRows()))
}

// tmuxSearchText is what the query matches against: the label the row leads
// with, which is repo/branch/tool. The status columns are not searchable —
// they are numbers that change on their own, and matching them would make the
// same query return different rows a minute later.
func (m *Model) tmuxSearchText(s tmux.Session) string { return s.Label(m.sessionPrefix()) }

// tmuxRow is the highlighted session, or false when the list is empty.
func (m *Model) tmuxRow(i int) (tmux.Session, bool) {
	if i < 0 || i >= len(m.tmuxRows) {
		return tmux.Session{}, false
	}
	return m.tmuxAll[m.tmuxRows[i]], true
}

// runSessionCommand runs one of the picker's built-in tmux commands through
// the ordinary command path, so mode handling, the status bar and the
// post-command tree refresh all behave as they do for a configured command.
func (m *Model) runSessionCommand(name, run, mode string) (tea.Model, tea.Cmd) {
	return m.execCommand(name, config.Command{Run: run, Mode: mode}, config.Vars{
		Root: m.tr.Root.Path,
	}, false)
}

// attachSession is enter in this view: reattach in a popup over the tree.
//
// Interactive rather than background, for the same reason the lazygit popup
// is: display-popup blocks until the popup closes, so ft is suspended for as
// long as the session is on screen and re-reads the tree and git status when
// you detach — which is exactly when the agent has been editing files.
func (m *Model) attachSession() (tea.Model, tea.Cmd) {
	s, ok := m.tmuxRow(m.fuzzySel)
	if !ok {
		return m, nil // an empty list is not an error
	}
	if m.isSelf(s) {
		return m, m.noteSelf()
	}
	m.mode = modeNormal
	return m.runSessionCommand(cmdAttachSession,
		tmux.AttachPopup(s.Name, config.ShellQuote), config.ModeInteractive)
}

// paneSession is "ctrl+w": put the session in a pane beside the tree, rather
// than a popup over it, so the agent and the files it is editing are on screen
// together. "X" detaches it again.
//
// It replaced switch-client, which handed this client to the session outright:
// that left no way back to ft short of detaching and re-attaching by hand,
// which is not a thing a key in a picker should do to you.
//
// A session already showing beside us is focused rather than opened twice.
// Attaching a second client would work — tmux allows it — but it would sit
// there as a duplicate of the pane you already had, and "window-size latest"
// would then reflow the agent between the two.
func (m *Model) paneSession() (tea.Model, tea.Cmd) {
	s, ok := m.tmuxRow(m.fuzzySel)
	if !ok {
		return m, nil
	}
	if m.isSelf(s) {
		return m, m.noteSelf()
	}
	m.mode = modeNormal
	if m.selfPane == "" {
		return m, m.note("not running inside tmux", true)
	}
	if p, _, found := m.paneShowing(func(name string) bool { return name == s.Name }); found {
		if err := tmux.SelectPane(p.ID); err != nil {
			return m, m.note(err.Error(), true)
		}
		return m, nil
	}
	if err := m.openSessionPane(s.Name); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, nil
}

// paneShowing finds the pane beside ft displaying a session match accepts.
// Nothing is remembered between calls: the answer is read from tmux each time,
// so it survives an ft restart and is right even for a pane opened by hand.
func (m *Model) paneShowing(match func(string) bool) (tmux.Pane, string, bool) {
	if m.selfPane == "" {
		return tmux.Pane{}, "", false
	}
	panes, err := tmux.ListPanes()
	if err != nil {
		return tmux.Pane{}, "", false
	}
	clients, err := tmux.ListClients()
	if err != nil {
		return tmux.Pane{}, "", false
	}
	return tmux.PaneShowing(panes, clients, m.selfPane, match)
}

// openSessionPane splits the window and attaches the session in the new pane,
// then puts ft back to the width it had.
//
// The restore is what makes this usable on a sidebar: a full-width split takes
// its space from *every* pane in the window, so a 30-column ft beside an
// editor comes out at 18 and the tree stops being readable. Reading the width
// immediately beforehand rather than remembering one keeps it honest when the
// user has resized ft themselves.
//
// It is skipped for an ft that filled the window, where there was no sidebar
// to preserve and restoring the old width would crush the pane just opened
// down to a single column.
func (m *Model) openSessionPane(name string) error {
	before, window := m.paneWidths() // zeroes are not fatal: the split is still worth doing
	if err := tmux.SplitAttach(m.selfPane, tmux.SocketPath(os.Getenv("TMUX")), name, config.ShellQuote); err != nil {
		return err
	}
	m.keepSidebarWidth(before, window)
	return nil
}

// killSession is "ctrl+x". A detached session goes straight away — it is the
// tidy-up half of a list you are meant to prune. One that is attached
// somewhere is another matter: something is looking at it, possibly another
// terminal on another desk, so that one asks first.
func (m *Model) killSession() (tea.Model, tea.Cmd) {
	s, ok := m.tmuxRow(m.fuzzySel)
	if !ok {
		return m, nil
	}
	if m.isSelf(s) {
		return m, m.noteSelf()
	}
	if s.Attached > 0 {
		m.pending = &pendingOp{kind: opKillSession, session: s.Name}
		m.mode = modeConfirm
		return m, nil
	}
	return m, m.killSessionNamed(s.Name)
}

// killSessionNamed does the deed and puts the list back together, keeping the
// cursor where it was: pruning several in a row is the normal way to use this.
func (m *Model) killSessionNamed(name string) tea.Cmd {
	if err := tmux.Kill(name); err != nil {
		return m.note(err.Error(), true)
	}
	sel := m.fuzzySel
	m.loadTmuxSessions()
	m.fuzzySel = clamp(sel, 0, max(0, len(m.tmuxRows)-1))
	m.ensureFuzzyVisible()
	return m.note("Killed "+strings.TrimPrefix(name, m.sessionPrefix()), false)
}

// tmuxStatusNote is the trailing note on the picker's input line: what went
// wrong, or how to read an empty list. An empty list is the ordinary state
// before the first agent session exists, and saying nothing at all there looks
// like a failure.
func (m *Model) tmuxStatusNote() string {
	if m.tmuxErr != "" {
		return styleError.Render("  " + m.tmuxErr)
	}
	if len(m.tmuxAll) == 0 {
		// Short on purpose: this sits after the query field and the counter on
		// one line, and a sidebar-width pane has very little room left by then.
		return styleDim.Render("  none yet")
	}
	return ""
}

// detachAgentPane is "X": send away whatever ft session is sharing this
// window, handing its space back to the tree. Whatever is in it keeps running
// and the picker will still list it — a detached session is exactly what "T"
// is for. Any kind, not agents alone: a shell opened with "ctrl+w" sits in
// the window the same way and goes away the same way.
//
// Detaching from outside is what spares the nested prefix. A session attached
// inside a pane owns the prefix key there, so detaching from within is "prefix
// prefix d"; ft is next to it and can just say detach-client instead.
//
// The width is read *before* detaching, while the sidebar still has the shape
// the user gave it, and re-applied afterwards: tmux hands a closing pane's
// columns to its neighbour, so an untouched ft would balloon to fill them.
func (m *Model) detachAgentPane() (tea.Model, tea.Cmd) {
	if m.selfPane == "" {
		return m, m.note("not running inside tmux", true)
	}
	prefix := m.sessionPrefix()
	// Never this tree: the panes beside ft carry the same prefix now that
	// everything ft opens is named, and detach-client on our own session would
	// send away the terminal you are reading this in.
	m.tmuxSelf = tmux.SelfSession(m.selfPane)
	self := m.tmuxSelf
	_, name, found := m.paneShowing(func(s string) bool {
		return strings.HasPrefix(s, prefix) && s != self
	})
	if !found {
		return m, m.note("no ft session in this window", false)
	}
	before, window := m.paneWidths()
	if err := tmux.DetachClient(name); err != nil {
		return m, m.note(err.Error(), true)
	}
	m.keepSidebarWidth(before, window)
	return m, m.note("detached "+name, false)
}
