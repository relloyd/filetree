package agenthook

import (
	"testing"

	"github.com/relloyd/filetree/internal/tmux"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload string
		want    Action
	}{
		{"prompt", "prompt", `{"prompt":"fix it"}`, Action{State: tmux.AgentWorking}},
		{"tool", "tool", `{"tool_name":"Bash"}`, Action{State: tmux.AgentWorking}},
		{"stop", "stop", `{}`, Action{State: tmux.AgentDone, Alert: true, Message: "finished its turn"}},
		{"end", "end", ``, Action{Clear: true}},

		// Claude Code's payload for a permission prompt, and Copilot's. The
		// message is the agent's own, which says far more than ft could.
		{"claude permission", "notify",
			`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`,
			Action{State: tmux.AgentWaiting, Alert: true, Message: "Claude needs your permission to use Bash"}},
		{"copilot question", "notify",
			`{"sessionId":"s","timestamp":1,"cwd":"/r","notification_type":"elicitation_dialog","message":"Which branch?"}`,
			Action{State: tmux.AgentWaiting, Alert: true, Message: "Which branch?"}},
		{"blocking, no message", "notify", `{"notification_type":"permission_prompt"}`,
			Action{State: tmux.AgentWaiting, Alert: true, Message: "waiting for you"}},

		// Notifications that are not the agent blocking on you change nothing.
		{"claude idle reminder", "notify", `{"notification_type":"idle_prompt","message":"Claude is waiting for your input"}`, Action{}},
		{"copilot shell done", "notify", `{"notification_type":"shell_completed"}`, Action{}},
		{"unreadable payload", "notify", `{not json`, Action{}},
		{"no payload", "notify", ``, Action{}},
	}
	for _, c := range cases {
		got, err := Decide(c.event, []byte(c.payload))
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Decide = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A misspelt event in a hook config must say so rather than do nothing.
func TestDecideUnknownEvent(t *testing.T) {
	if _, err := Decide("Stop", nil); err == nil {
		t.Error("an unknown event should be an error")
	}
}

func TestEventsAreAllKnown(t *testing.T) {
	for _, e := range Events {
		if _, err := Decide(e, nil); err != nil {
			t.Errorf("listed event %q is not understood: %v", e, err)
		}
	}
}

func TestTitle(t *testing.T) {
	cases := []struct{ session, want string }{
		{"ft/agent/filetree/main/claude", "filetree/main · claude"},
		{"ft/shell/filetree-1a2b3c4d", "shell/filetree-1a2b3c4d"},
		{"work", "work"},
	}
	for _, c := range cases {
		if got := Title(tmux.DefaultPrefix, c.session); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.session, got, c.want)
		}
	}
}
