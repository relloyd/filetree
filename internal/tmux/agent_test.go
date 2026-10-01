package tmux

import (
	"testing"
	"time"
)

func TestAgentRoundTrip(t *testing.T) {
	at := time.Unix(1700000000, 0)
	for _, state := range []string{AgentWorking, AgentWaiting, AgentDone} {
		if got := ParseAgent(FormatAgent(state, at)); got != (Agent{State: state, Since: at}) {
			t.Errorf("%s: round trip = %+v", state, got)
		}
	}
}

// Only what a hook could have written is read back; anything else is nothing
// rather than a guess.
func TestParseAgent(t *testing.T) {
	cases := []struct {
		in   string
		want Agent
	}{
		{"", Agent{}},
		{"waiting 1700000000", Agent{State: AgentWaiting, Since: time.Unix(1700000000, 0)}},
		{"done", Agent{State: AgentDone}},      // no time: still a state
		{"done soon", Agent{State: AgentDone}}, // unreadable time
		{" working 1700000000\n", Agent{State: AgentWorking, Since: time.Unix(1700000000, 0)}},
		{"asleep 1700000000", Agent{}},  // not a state ft knows
		{"WAITING 1700000000", Agent{}}, // states are lower case
	}
	for _, c := range cases {
		if got := ParseAgent(c.in); got != c.want {
			t.Errorf("ParseAgent(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestNeedsYou(t *testing.T) {
	waiting := Agent{State: AgentWaiting}
	done := Agent{State: AgentDone}
	cases := []struct {
		name string
		s    Session
		want bool
	}{
		{"nothing reported", Session{Command: "claude"}, false},
		{"working", Session{Command: "claude", Agent: Agent{State: AgentWorking}}, false},
		// Working again after a bell nobody saw: the flag is left over.
		{"working, stale bell", Session{Command: "claude", Agent: Agent{State: AgentWorking}, Alert: true}, false},
		// Blocked on you counts whether or not you have looked.
		{"waiting", Session{Command: "claude", Agent: waiting}, true},
		// Done counts only until you look, which is what the bell flag says.
		{"done, unseen", Session{Command: "claude", Agent: done, Alert: true}, true},
		{"done, seen", Session{Command: "claude", Agent: done}, false},
		// A bell from anything else still counts: it is what tmux has.
		{"bell, no agent", Session{Command: "make", Alert: true}, true},
		// The agent exited and left its state behind: back at a shell, so
		// the state is stale and is not shown.
		{"stale waiting", Session{Command: "zsh", Agent: waiting}, false},
		{"stale, login shell", Session{Command: "-zsh", Agent: waiting}, false},
	}
	for _, c := range cases {
		if got := c.s.NeedsYou(); got != c.want {
			t.Errorf("%s: NeedsYou = %v, want %v", c.name, got, c.want)
		}
	}
}
