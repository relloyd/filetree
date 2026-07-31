package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// A command may take "tab", even though the finder wants it for
// finder-next-field, because buildBindings keeps finder-next-field out of
// m.bindings: the finder handles tab itself, inside the modeFuzzy switch, so
// the main view never sees it. Bind tab to a normal-mode action — or add
// finder-next-field to the actions map — and any command on tab is shadowed
// with nothing to show for it: tab goes on cycling the finder's fields and the
// command quietly stops running.
//
// The starter used to rely on this for focus-right, and no longer does — that
// moved to ctrl+l when the pane commands were reworked. The property is still
// worth holding: tab is offered to commands, so it has to keep working for one.
func TestACommandCanOwnTab(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.mode = modeNormal
	ran := filepath.Join(t.TempDir(), "ran")
	m.cfg.Commands = map[string]config.Command{
		"focus-right": {Run: "touch " + ran, Mode: config.ModeBackground, Key: "tab"},
	}
	m.buildBindings()

	// The collision this test is about only exists while the finder still wants
	// tab for itself.
	if got := m.actionKeys["finder-next-field"]; got != "tab" {
		t.Fatalf("finder-next-field = %q, want tab", got)
	}

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd == nil {
		t.Fatal(`"tab" produced no command in the main view`)
	}
	msg, ok := cmd().(cmdDoneMsg)
	if !ok {
		t.Fatalf("tab ran something other than a user command: %T", cmd())
	}
	if msg.name != "focus-right" || msg.err != nil {
		t.Fatalf("ran %q: err=%v out=%q", msg.name, msg.err, msg.out)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("command did not run: %v", err)
	}

	// And the shape of the failure being guarded against: actions are bound
	// after commands, so anything that lands an action on tab takes it away
	// silently. Asserting it here keeps the check above honest — it is only
	// meaningful if a shadowed binding is something this test could detect.
	m.cfg.Keys = map[string]string{"quit": "tab"}
	m.buildBindings()
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab}); cmd != nil {
		if _, ok := cmd().(cmdDoneMsg); ok {
			t.Error("an action bound to tab did not shadow the command key")
		}
	}
}
