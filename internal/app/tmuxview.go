package app

import (
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
	cmdSwitchSession = "switch-session"
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

// switchSession is "ctrl+w": hand the whole window to the session rather than
// a popup, for a transcript worth reading at full width. tmux's own "prefix L"
// comes back.
func (m *Model) switchSession() (tea.Model, tea.Cmd) {
	s, ok := m.tmuxRow(m.fuzzySel)
	if !ok {
		return m, nil
	}
	m.mode = modeNormal
	return m.runSessionCommand(cmdSwitchSession,
		tmux.SwitchClient(s.Name, config.ShellQuote), config.ModeBackground)
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
