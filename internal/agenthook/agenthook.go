// Package agenthook decides what an agent's lifecycle hook means for the
// session it runs in. It is the pure half of "ft agent-hook <event>": the
// command (cmd/ft/agenthook.go) reads the event and the hook's JSON payload,
// asks Decide what to do, and does it through tmux and the platform.
//
// The events are ft's own small vocabulary rather than any one agent's, so
// the same five words wire up Claude Code and Copilot CLI alike — each agent's
// hook config maps its own event names onto them (see the README):
//
//	prompt  you sent a prompt                      → working
//	tool    a tool finished running                → working
//	notify  the agent raised a notification        → waiting, if it blocks on you
//	stop    the agent finished its turn            → done
//	end     the agent's session ended              → state cleared
//
// "tool" is what ends a "waiting": no event fires when you approve a
// permission, so the first sign the agent is moving again is the approved
// tool completing.
package agenthook

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/relloyd/filetree/internal/tmux"
)

// Events is every event Decide understands, in the order the doc lists them.
var Events = []string{"prompt", "tool", "notify", "stop", "end"}

// Action is what one hook call should do.
type Action struct {
	State string // the state to record; "" records nothing
	Clear bool   // remove the state instead

	// Alert asks for the user's attention: a bell on the pane, which tmux
	// turns into the session's "!" if nobody is looking, and a desktop
	// notification when nobody is attached at all.
	Alert   bool
	Message string // the notification's text, when Alert is set
}

// payload is the part of a hook's stdin Decide reads. Claude Code and Copilot
// CLI both send notification_type and message on their notification event, in
// these spellings; everything else in the payload is ignored.
type payload struct {
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
}

// blocking are the notification types that mean "stopped until you answer".
// Both agents use these two names. The rest — Claude's idle_prompt (a reminder
// after its turn has already ended, which "stop" reported), auth_success,
// Copilot's notices about background shells and subagents — are not about
// the agent waiting on you, and change nothing.
var blocking = map[string]bool{
	"permission_prompt":  true,
	"elicitation_dialog": true,
}

// Decide maps an event and its payload to an Action. An unknown event is an
// error, because it can only mean a hook config that is wrong, and a hook that
// silently did nothing would look exactly like a feature that does not work.
//
// A payload that is missing or will not parse is not an error: only "notify"
// reads it, and an unreadable notification is one ft cannot classify, so it
// does nothing — the agent's own notification still happens either way.
func Decide(event string, raw []byte) (Action, error) {
	switch event {
	case "prompt", "tool":
		return Action{State: tmux.AgentWorking}, nil
	case "notify":
		var p payload
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &p)
		}
		if !blocking[p.NotificationType] {
			return Action{}, nil
		}
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			msg = "waiting for you"
		}
		return Action{State: tmux.AgentWaiting, Alert: true, Message: msg}, nil
	case "stop":
		return Action{State: tmux.AgentDone, Alert: true, Message: "finished its turn"}, nil
	case "end":
		return Action{Clear: true}, nil
	}
	return Action{}, fmt.Errorf("unknown agent event %q (want one of %s)", event, strings.Join(Events, ", "))
}

// Title is the notification title for a session: its name without the prefix
// and, for an agent session, laid out as "repo/branch · tool" so the tool reads
// separately from where it is working. Anything that does not parse is shown
// as named.
func Title(prefix, session string) string {
	if p, ok := tmux.ParseName(prefix, session); ok && p.Kind == tmux.KindAgent {
		return p.Repo + "/" + p.Branch + " · " + p.Tool
	}
	return strings.TrimPrefix(session, prefix)
}
