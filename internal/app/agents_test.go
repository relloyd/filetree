package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/relloyd/filetree/internal/tmux"
)

func agentState(state string, since int64) func(*tmux.Session) {
	return func(s *tmux.Session) {
		s.Command = "claude"
		s.Agent = tmux.Agent{State: state, Since: time.Unix(since, 0)}
	}
}

// An agent blocked on a question leads the list even without a bell — it needs
// an answer whether or not you have seen it — while one that is working has
// moved on from whatever its bell was about.
func TestTmuxSessionOrderByAgent(t *testing.T) {
	m := tmuxPickerModel(t,
		session("ft/agent/a/main/claude"),
		session("ft/agent/b/main/claude", agentState(tmux.AgentWorking, 1700000000), alerting),
		session("ft/agent/c/main/claude", agentState(tmux.AgentWaiting, 1600000000), idleLonger),
	)
	var got []string
	for _, i := range m.tmuxRows {
		got = append(got, m.tmuxAll[i].Name)
	}
	// c waits; a and b tie on activity and fall back to their names, b's
	// leftover bell notwithstanding.
	want := []string{"ft/agent/c/main/claude", "ft/agent/a/main/claude", "ft/agent/b/main/claude"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestTmuxRowShowsAgentState(t *testing.T) {
	now := time.Unix(1700000000+5*60, 0)
	cases := []struct {
		name      string
		s         tmux.Session
		want      []string
		wantNoBan bool // no "!" expected
	}{
		{"waiting", session("ft/agent/r/main/claude", agentState(tmux.AgentWaiting, 1700000000)),
			[]string{"waiting", "!", "5m"}, false},
		// Done and already looked at: said, but not flagged.
		{"done, seen", session("ft/agent/r/main/claude", agentState(tmux.AgentDone, 1700000000)),
			[]string{"done", "5m"}, true},
		{"done, unseen", session("ft/agent/r/main/claude", agentState(tmux.AgentDone, 1700000000), alerting),
			[]string{"done", "!"}, false},
		// Back at a shell: whatever the agent left behind is not shown.
		{"stale", session("ft/agent/r/main/claude", agentState(tmux.AgentWaiting, 1700000000),
			func(s *tmux.Session) { s.Command = "zsh" }),
			[]string{"zsh"}, true},
	}
	for _, c := range cases {
		plain, _ := tmuxRowStatus(c.s, false, now)
		for _, w := range c.want {
			if !strings.Contains(plain, w) {
				t.Errorf("%s: status %q, want %q in it", c.name, plain, w)
			}
		}
		if c.wantNoBan && strings.Contains(plain, "!") {
			t.Errorf("%s: status %q should not be flagged", c.name, plain)
		}
		if c.name == "stale" && strings.Contains(plain, "waiting") {
			t.Errorf("stale: status %q shows a state the agent left behind", plain)
		}
	}
}

func TestSessionPollCount(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.applySessionPoll(sessionsPolledMsg{
		self: "ft/tree/x-1a2b3c4d",
		sessions: []tmux.Session{
			session("ft/agent/a/main/claude", agentState(tmux.AgentWaiting, 1)),
			session("ft/agent/b/main/copilot", alerting),
			session("ft/agent/c/main/claude", agentState(tmux.AgentWorking, 1)),
			// This tree's own bell is on the screen already.
			session("ft/tree/x-1a2b3c4d", alerting),
		},
	})
	if m.agentsWaiting != 2 {
		t.Errorf("agentsWaiting = %d, want 2", m.agentsWaiting)
	}
	// A failed read keeps the last count rather than reporting all clear.
	m.applySessionPoll(sessionsPolledMsg{err: errors.New("server exited unexpectedly")})
	if m.agentsWaiting != 2 {
		t.Errorf("after a failed read agentsWaiting = %d, want it kept at 2", m.agentsWaiting)
	}
}

// The open list refreshes in place, and the cursor stays on the session it
// was on even when a newly waiting one jumps above it.
func TestSessionPollRefreshesOpenList(t *testing.T) {
	m := tmuxPickerModel(t,
		session("ft/agent/a/main/claude"),
		session("ft/agent/b/main/claude", idleLonger),
	)
	m.fuzzySel = 1 // b
	if s, _ := m.tmuxRow(m.fuzzySel); s.Name != "ft/agent/b/main/claude" {
		t.Fatalf("setup: selected %s", s.Name)
	}
	m.applySessionPoll(sessionsPolledMsg{sessions: []tmux.Session{
		session("ft/agent/a/main/claude"),
		session("ft/agent/b/main/claude", idleLonger),
		session("ft/agent/c/main/copilot", agentState(tmux.AgentWaiting, 1)),
	}})
	if len(m.tmuxRows) != 3 {
		t.Fatalf("rows = %d, want the new session listed", len(m.tmuxRows))
	}
	if s, _ := m.tmuxRow(0); s.Name != "ft/agent/c/main/copilot" {
		t.Errorf("first row = %s, want the waiting session", s.Name)
	}
	if s, _ := m.tmuxRow(m.fuzzySel); s.Name != "ft/agent/b/main/claude" {
		t.Errorf("cursor moved to %s, want it kept on b", s.Name)
	}
}

func TestStatusBarShowsWaiting(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.width = 80
	if got := rowText([]string{m.renderStatus()}); strings.Contains(got, "waiting") {
		t.Errorf("nothing waiting, but the bar says %q", got)
	}
	m.agentsWaiting = 2
	if got := rowText([]string{m.renderStatus()}); !strings.Contains(got, "! 2 waiting") {
		t.Errorf("status bar = %q, want the waiting count", got)
	}
}
