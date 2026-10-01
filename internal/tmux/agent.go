package tmux

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AgentOption is the tmux user option an agent's state is kept in. It is set
// on the *session* the agent runs in, by the agent's own hooks running
// "ft agent-hook" (cmd/ft/agenthook.go), and read back by List alongside
// everything else it reports, so showing it costs no extra call.
//
// A session option rather than a pane option because a session is what ft
// names, lists and attaches: one agent per session is the convention every
// agent key in the catalogue follows. Two agents sharing one session would
// overwrite each other's state, and the last to speak wins.
const AgentOption = "@ft_agent"

// The states an agent reports. Empty is "nothing reported": a session that is
// not an agent, an agent whose hooks are not installed, or one that has ended.
const (
	AgentWorking = "working" // you sent a prompt, or it got on with a tool
	AgentWaiting = "waiting" // blocked on you mid-task: a permission or a question
	AgentDone    = "done"    // finished its turn; your move
)

// Agent is the state an agent last reported, and when.
type Agent struct {
	State string
	Since time.Time
}

// FormatAgent is the option value for a state set at a moment: the state, a
// space, and the unix time. One value rather than two options so that setting
// it is one tmux call and the two halves can never disagree.
func FormatAgent(state string, at time.Time) string {
	return state + " " + strconv.FormatInt(at.Unix(), 10)
}

// ParseAgent reads an option value back. Anything unrecognised is the zero
// Agent: a state ft does not know is one it cannot show correctly, and saying
// nothing beats saying something wrong.
func ParseAgent(v string) Agent {
	state, at, _ := strings.Cut(strings.TrimSpace(v), " ")
	switch state {
	case AgentWorking, AgentWaiting, AgentDone:
	default:
		return Agent{}
	}
	a := Agent{State: state}
	if secs, err := strconv.ParseInt(at, 10, 64); err == nil && secs > 0 {
		a.Since = time.Unix(secs, 0)
	}
	return a
}

// shells are the commands that mean "the agent is no longer running". The
// agent keys run "<tool>; exec $SHELL", so a session whose foreground command
// is a shell is one whose agent has exited, and a state it left behind — say
// "working" from an agent that crashed before its end hook ran — is stale.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "tcsh": true, "nu": true,
}

// AgentNow is the state worth showing for a session: what its agent reported,
// unless the session has gone back to a shell, in which case nothing.
func (s Session) AgentNow() Agent {
	if s.Agent.State == "" || shells[filepath.Base(strings.TrimPrefix(s.Command, "-"))] {
		return Agent{}
	}
	return s.Agent
}

// NeedsYou reports whether a session is waiting on you: an agent blocked
// mid-task, which needs an answer whether or not you have looked at it, or a
// bell you have not seen yet, which is how an agent that has finished (or any
// other tool) says so. A finished agent you have already looked at is not
// waiting — that is the difference between done and done-and-unread, and the
// bell is what carries it: tmux sets the flag only on a window nobody is
// viewing and clears it when you attach.
//
// An agent that is working does not need you, whatever the flag says. The
// flag outlives the moment that set it until someone attaches, so a question
// that was answered without you — a prompt approved by a classifier, say —
// would otherwise keep the session flagged while the agent got on with it.
func (s Session) NeedsYou() bool {
	switch s.AgentNow().State {
	case AgentWaiting:
		return true
	case AgentWorking:
		return false
	}
	return s.Alert
}
