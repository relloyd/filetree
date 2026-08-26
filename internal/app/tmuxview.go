package app

import (
	"os"

	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/tmux"
)

// The picker's own commands are given names so that a failure reads as
// "claude-popup: ..." would — execCommand and handleCmdDone label their
// messages with whatever they are handed.
const (
	cmdAttachSession = "attach-session"
)

// startTmuxSessions opens the finder over the named tmux sessions. It is the
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
	m.sortTmuxSessions()
}

// sortTmuxSessions puts the sessions in the order they are wanted in: the ones
// asking for attention first, then by how recently they did anything.
//
// A bell is what Claude Code rings when it wants input, so a session with one
// pending is the one the list exists to surface. Attachment is not part of the
// ordering — a session you already have open somewhere is the one you least
// need to be shown.
func (m *Model) sortTmuxSessions() {
	sort.SliceStable(m.tmuxAll, func(i, j int) bool {
		a, b := m.tmuxAll[i], m.tmuxAll[j]
		if a.Alert != b.Alert {
			return a.Alert
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
	m.tmuxRows, m.tmuxMatched = m.tmuxRows[:0], m.tmuxMatched[:0]
	if q := m.tmuxInput.Value(); q == "" {
		for i := range m.tmuxAll {
			m.tmuxRows = append(m.tmuxRows, i)
			m.tmuxMatched = append(m.tmuxMatched, nil)
		}
	} else {
		hay := make([]string, len(m.tmuxAll))
		for i, s := range m.tmuxAll {
			hay[i] = m.tmuxSearchText(s)
		}
		for _, mt := range fuzzy.Find(q, hay) {
			m.tmuxRows = append(m.tmuxRows, mt.Index)
			m.tmuxMatched = append(m.tmuxMatched, mt.MatchedIndexes)
		}
	}
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
	before, window, err := tmux.PaneWidths(m.selfPane)
	if err != nil {
		before, window = 0, 0 // not fatal: the split is still worth doing
	}
	if err := tmux.SplitAttach(m.selfPane, tmux.SocketPath(os.Getenv("TMUX")), name, config.ShellQuote); err != nil {
		return err
	}
	if before > 0 && before < window/2 {
		_ = tmux.ResizePaneWidth(m.selfPane, before)
	}
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

// newSessionHere is "alt+n": start a session for the *tree's* selection
// without leaving the picker's train of thought. The rows are existing
// sessions, so there is nothing here to create one from — what is wanted is
// the one that is missing, which is the repo and branch you were browsing.
//
// "alt+n" rather than "ctrl+n" because the finder already spends ctrl+n on
// moving down, and an alt chord steps around the text input's own ctrl keys.
func (m *Model) newSessionHere() (tea.Model, tea.Cmd) {
	// [sessions] new_command names it. Note this is deliberately not
	// commands.default: that one is an editor in the shipped config, and
	// running an editor would be a surprising answer to "new session".
	name := m.cfg.Sessions.NewCommand
	if _, ok := m.cfg.Commands[name]; !ok {
		name = m.firstAgentCommand()
	}
	if name == "" {
		return m, m.note("set [sessions] new_command to the command this key should run", true)
	}
	m.mode = modeNormal
	return m.runCommand(name)
}

// firstAgentCommand is the alphabetically first configured command that builds
// a session name — the fallback when new_command is unset. Sorted rather than
// map order so the key does the same thing every time it is pressed.
func (m *Model) firstAgentCommand() string {
	var names []string
	for name, c := range m.cfg.Commands {
		if config.NeedsRepo(c.Run) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
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

// detachAgentPane is "X": send away whatever agent session is sharing this
// window, handing its space back to the tree. The agent keeps running and the
// picker will still list it — a detached session is exactly what "T" is for.
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
	_, name, found := m.paneShowing(func(s string) bool { return strings.HasPrefix(s, prefix) })
	if !found {
		return m, m.note("no agent session in this window", false)
	}
	before, window, err := tmux.PaneWidths(m.selfPane)
	if err != nil {
		before, window = 0, 0
	}
	if err := tmux.DetachClient(name); err != nil {
		return m, m.note(err.Error(), true)
	}
	if before > 0 && before < window/2 {
		_ = tmux.ResizePaneWidth(m.selfPane, before)
	}
	return m, m.note("detached "+name, false)
}
