package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/relloyd/filetree/internal/tmux"
)

// hookRecorder is a hookEnv whose outside world is a log of what was asked of
// it, so a test reads as the sequence of calls a hook makes.
type hookRecorder struct {
	calls    []string
	attached int
	notify   bool
}

func (r *hookRecorder) env(pane string) hookEnv {
	return hookEnv{
		pane:     pane,
		now:      time.Unix(1700000000, 0),
		settings: func() (string, bool) { return tmux.DefaultPrefix, r.notify },
		describe: func(p string) (tmux.PaneInfo, error) {
			r.calls = append(r.calls, "describe "+p)
			return tmux.PaneInfo{Session: "ft/agent/filetree/main/claude", Attached: r.attached, TTY: "/dev/ttys009"}, nil
		},
		set:   func(p, v string) error { r.calls = append(r.calls, "set "+p+" "+v); return nil },
		clear: func(p string) error { r.calls = append(r.calls, "clear "+p); return nil },
		bell:  func(tty string) error { r.calls = append(r.calls, "bell "+tty); return nil },
		post: func(title, msg string) error {
			r.calls = append(r.calls, "post "+title+": "+msg)
			return nil
		},
	}
}

func TestAgentHookSequence(t *testing.T) {
	permission := `{"notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`
	cases := []struct {
		name     string
		event    string
		payload  string
		attached int
		notify   bool
		want     []string
	}{
		{"prompt only records", "prompt", "", 0, true,
			[]string{"set %3 working 1700000000"}},
		{"end clears", "end", "", 0, true,
			[]string{"clear %3"}},
		// Nobody attached: the bell for tmux's flag, and a banner.
		{"waiting, away", "notify", permission, 0, true, []string{
			"set %3 waiting 1700000000",
			"describe %3",
			"bell /dev/ttys009",
			"post filetree/main · claude: Claude needs your permission to use Bash",
		}},
		// Someone is looking: the bell still rings — tmux decides whether it
		// is news — but no banner on top of what is already on screen.
		{"done, watched", "stop", "", 1, true, []string{
			"set %3 done 1700000000",
			"describe %3",
			"bell /dev/ttys009",
		}},
		{"notify off", "stop", "", 0, false, []string{
			"set %3 done 1700000000",
			"describe %3",
			"bell /dev/ttys009",
		}},
		// A notification that is not the agent blocking on you does nothing.
		{"idle reminder", "notify", `{"notification_type":"idle_prompt"}`, 0, true, nil},
	}
	for _, c := range cases {
		r := &hookRecorder{attached: c.attached, notify: c.notify}
		if err := r.env("%3").run(c.event, []byte(c.payload)); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(r.calls, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, r.calls, c.want)
		}
	}
}

// Outside tmux there is no session to report on: nothing is touched, and it
// is not a failure — the hooks are installed for every terminal.
func TestAgentHookOutsideTmux(t *testing.T) {
	r := &hookRecorder{notify: true}
	if err := r.env("").run("stop", nil); err != nil {
		t.Fatalf("outside tmux should succeed, got %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("outside tmux should do nothing, got %q", r.calls)
	}
}

// A misspelt event is reported even outside tmux, where it would otherwise
// never be noticed until the day it mattered.
func TestAgentHookUnknownEvent(t *testing.T) {
	r := &hookRecorder{}
	if err := r.env("").run("Stop", nil); err == nil {
		t.Error("an unknown event should fail")
	}
}

// A tmux failure surfaces rather than being swallowed: an agent shows a
// failing hook, and a state that silently never updates looks like a bug in
// the list instead.
func TestAgentHookTmuxFailure(t *testing.T) {
	r := &hookRecorder{}
	e := r.env("%3")
	e.set = func(string, string) error { return errors.New("no server running") }
	if err := e.run("prompt", nil); err == nil {
		t.Error("a failing set-option should be reported")
	}
}

func TestAgentHookArgs(t *testing.T) {
	if !agentHookArgs([]string{"agent-hook", "stop"}) {
		t.Error("agent-hook with an event should dispatch")
	}
	// The tree form takes one argument, so a directory of this name opens.
	if agentHookArgs([]string{"agent-hook"}) {
		t.Error("a lone agent-hook is a directory, not the subcommand")
	}
}
