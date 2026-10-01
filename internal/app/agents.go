package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/tmux"
)

// agentPollInterval is how often ft re-reads the session list in the
// background. It is one list-sessions call — a few milliseconds — and it is
// what makes an agent that starts waiting show up in the status bar, and in an
// open "T", without a key press. Two seconds is quick enough that a waiting
// agent is not left waiting on ft as well.
const agentPollInterval = 2 * time.Second

type (
	// agentPollMsg is the tick that starts the next background read.
	agentPollMsg struct{}

	// sessionsPolledMsg is one background read of the session list. self is
	// read alongside it, as loadTmuxSessions does, so a renamed tree is still
	// recognised and left out of its own count.
	sessionsPolledMsg struct {
		sessions []tmux.Session
		self     string
		err      error
	}
)

// pollSessions reads the session list off the event loop. The tick and the
// read form one chain — each result schedules the next tick — so there is only
// ever one read in flight, however long tmux takes to answer.
//
// The prefix and the pane are captured here, on the loop, rather than read
// inside the goroutine from a model that Update may be changing at the time.
func (m *Model) pollSessions() tea.Cmd {
	prefix, pane := m.sessionPrefix(), m.selfPane
	return func() tea.Msg {
		sessions, err := tmux.List(prefix)
		return sessionsPolledMsg{sessions: sessions, self: tmux.SelfSession(pane), err: err}
	}
}

// nextAgentPoll schedules the next read.
func nextAgentPoll() tea.Cmd {
	return tea.Tick(agentPollInterval, func(time.Time) tea.Msg { return agentPollMsg{} })
}

// applySessionPoll takes a background read: the count for the status bar,
// and the rows of "T" when it is open.
//
// A read that failed changes nothing. It is most likely a tmux server in the
// middle of going away, and a count that dropped to zero for one tick would
// look like every agent had been answered.
func (m *Model) applySessionPoll(msg sessionsPolledMsg) {
	if msg.err != nil {
		return
	}
	m.agentsWaiting = countNeedsYou(msg.sessions, msg.self)
	if m.mode != modeFuzzy || m.finderSrc != srcTmux {
		return
	}
	// Re-sorting moves rows about — a session that has just started waiting
	// jumps to the top — so the cursor follows the session it was on rather
	// than staying on a row number. enter then attaches what you were looking
	// at, not whatever slid underneath it.
	keep, _ := m.tmuxRow(m.fuzzySel)
	m.tmuxAll, m.tmuxSelf, m.tmuxErr = msg.sessions, msg.self, ""
	m.sortTmuxSessions()
	for i, idx := range m.tmuxRows {
		if m.tmuxAll[idx].Name == keep.Name {
			m.fuzzySel = i
			break
		}
	}
	m.ensureFuzzyVisible()
}

// countNeedsYou is how many sessions are waiting on you, not counting this
// tree's own: anything ringing in it is on the screen you are looking at.
func countNeedsYou(sessions []tmux.Session, self string) int {
	n := 0
	for _, s := range sessions {
		if s.Name != self && s.NeedsYou() {
			n++
		}
	}
	return n
}
