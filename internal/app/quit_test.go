package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// ctrlC is the key as the terminal delivers it: a bare 'c' with the ctrl
// modifier, which Key.String() renders "ctrl+c". Spelled out once here because
// the whole quit path keys off that string.
func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func pressKey(t *testing.T, m *Model, msg tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	_, cmd := m.handleKey(msg)
	return cmd
}

// quits reports whether cmd is the one that ends the program. m.quit is the
// only tea.Quit in the app, so this is "did that press quit".
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// treeModel is a model on a real tree, in the tree view, with bindings built
// from its config — the state every test below starts from.
func treeModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, dir)
	m.mode = modeNormal
	m.buildBindings()
	return m
}

// "q" quitting on a single unshifted press, next to the navigation keys, is one
// mistype away from ending the session. ctrl+c was always bound to the same
// action, so retiring "q" costs nothing and hands the letter back to the keymap.
func TestQuitShipsUnboundOnCtrlCOnly(t *testing.T) {
	m := treeModel(t)

	if _, bound := m.bindings["q"]; bound {
		t.Error(`"q" is still bound in the tree view; it should be free now`)
	}
	if _, bound := m.bindings["ctrl+c"]; !bound {
		t.Fatal("ctrl+c is not bound: nothing would quit the app")
	}
	if got := m.actionKeys["quit"]; got != "" {
		t.Errorf("quit = %q, want it to ship with no key of its own", got)
	}
	if n := len(m.keyConflicts); n != 0 {
		t.Errorf("a default config reported %d conflicts: %v", n, m.keyConflicts)
	}

	if cmd := pressKey(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd != nil {
		t.Errorf(`"q" still did something in the tree view: %T`, cmd())
	}
	if m.mode != modeNormal {
		t.Errorf(`"q" changed the mode to %v`, m.mode)
	}
	if !quits(pressKey(t, m, ctrlC())) {
		t.Error("ctrl+c did not quit from the tree view")
	}
}

// Unbinding is not removing: quit keeps its entry in the action map, so anyone
// who wants the old key back writes one line. This is the property that makes
// shipping it unbound safe rather than opinionated.
func TestQuitCanBeBoundBackToAKey(t *testing.T) {
	m := treeModel(t)
	m.cfg.Keys = map[string]string{"quit": "q"}
	m.buildBindings()

	if n := len(m.keyConflicts); n != 0 {
		t.Fatalf("binding quit to a free key reported %d conflicts: %v", n, m.keyConflicts)
	}
	if got := m.actionKeys["quit"]; got != "q" {
		t.Fatalf("quit = %q, want the key the config asked for", got)
	}
	if !quits(pressKey(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})) {
		t.Error(`"q" did not quit after the config asked for it`)
	}
	// And the guaranteed way out is still guaranteed.
	if !quits(pressKey(t, m, ctrlC())) {
		t.Error("ctrl+c stopped quitting once quit had a letter key")
	}
}

// The point of the change: "q" is back in the pool, so a command can have it.
func TestACommandCanOwnQ(t *testing.T) {
	m := treeModel(t)
	ran := filepath.Join(t.TempDir(), "ran")
	m.cfg.Commands = map[string]config.Command{
		"scratch-note": {Run: "touch " + ran, Mode: config.ModeBackground, Key: "q"},
	}
	m.buildBindings()

	if n := len(m.keyConflicts); n != 0 {
		t.Fatalf(`a command on "q" reported %d conflicts: %v`, n, m.keyConflicts)
	}
	cmd := pressKey(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal(`"q" produced no command`)
	}
	msg, ok := cmd().(cmdDoneMsg)
	if !ok {
		t.Fatalf(`"q" ran something other than the command: %T`, cmd())
	}
	if msg.name != "scratch-note" || msg.err != nil {
		t.Fatalf("ran %q: err=%v out=%q", msg.name, msg.err, msg.out)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("command did not run: %v", err)
	}
}

// With "q" gone, ctrl+c is the only way out — so a press inside an overlay must
// back out to the tree rather than end the session on top of a half-typed
// rename. Each overlay already does exactly that under esc; handleKey routes
// ctrl+c into it, so this asserts the routing reaches every one of them.
func TestCtrlCBacksOutOfEveryOverlayBeforeQuitting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		want  mode
	}{{
		name:  "the help page",
		setup: func(m *Model) { m.mode = modeHelp },
		want:  modeNormal,
	}, {
		name:  "a rename prompt",
		setup: func(m *Model) { m.mode = modePrompt; m.prompt = promptRename },
		want:  modeNormal,
	}, {
		name:  "the finder",
		setup: func(m *Model) { m.mode = modeFuzzy },
		want:  modeNormal,
	}, {
		name: "a delete confirmation",
		setup: func(m *Model) {
			m.mode, m.pending = modeConfirm, &pendingOp{kind: opTrash, items: []string{"/tmp/x"}}
		},
		want: modeNormal,
	}, {
		// This one is asked from inside the session picker, so esc — and now
		// ctrl+c — drops back into the picker rather than all the way out.
		name: "a kill-session confirmation",
		setup: func(m *Model) {
			m.mode, m.pending = modeConfirm, &pendingOp{kind: opKillSession, session: "ft/x"}
		},
		want: modeFuzzy,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			m := treeModel(t)
			tc.setup(m)

			if cmd := pressKey(t, m, ctrlC()); quits(cmd) {
				t.Fatal("ctrl+c quit outright instead of backing out first")
			}
			if m.mode != tc.want {
				t.Fatalf("mode = %v after ctrl+c, want %v", m.mode, tc.want)
			}
			if m.pending != nil {
				t.Errorf("the staged operation survived the cancel: %+v", m.pending)
			}
		})
	}

	// And once the tree has the keyboard again, the next press does quit.
	m := treeModel(t)
	m.mode = modeFuzzy
	pressKey(t, m, ctrlC())
	if !quits(pressKey(t, m, ctrlC())) {
		t.Error("the second ctrl+c did not quit after the first closed the finder")
	}
}

// handleKey rewrites ctrl+c to "esc" before the mode switch, so the rewrite
// must never fall through to the text inputs: a ctrl+c that closed the finder
// *and* typed into it would be a silent corruption of the query.
func TestCtrlCNeverReachesTheFinderInput(t *testing.T) {
	m := treeModel(t)
	m.mode = modeFuzzy
	m.input.SetValue("query")

	pressKey(t, m, ctrlC())
	if m.mode != modeNormal {
		t.Fatalf("mode = %v, want the finder closed", m.mode)
	}
	if got := m.input.Value(); got != "query" {
		t.Errorf("finder query = %q, want it untouched at %q", got, "query")
	}
}
