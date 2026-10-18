package app

import (
	"os"
	"path/filepath"
	"strings"
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

// The quit confirmation is the one place ctrl+c is not rewritten to esc: it
// was a ctrl+c that asked the question, and a second one is the answer. The
// same goes for quit's own key when [keys] gives it one — even "q", which
// otherwise backs out of a confirmation.
func TestQuitConfirmationAnswers(t *testing.T) {
	q := tea.KeyPressMsg{Code: 'q', Text: "q"}
	for _, tc := range []struct {
		name      string
		quitKey   string
		key       tea.KeyPressMsg
		wantQuit  bool
		wantClose bool
	}{
		{"ctrl+c again closes", "", ctrlC(), true, true},
		{"y closes", "", tea.KeyPressMsg{Code: 'y', Text: "y"}, true, true},
		{"enter closes", "", tea.KeyPressMsg{Code: tea.KeyEnter}, true, true},
		{"n backs out", "", tea.KeyPressMsg{Code: 'n', Text: "n"}, false, false},
		{"esc backs out", "", tea.KeyPressMsg{Code: tea.KeyEscape}, false, false},
		{"q backs out while quit has no key", "", q, false, false},
		{"q closes once quit is bound to it", "q", q, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := treeModel(t)
			if tc.quitKey != "" {
				m.cfg.Keys = map[string]string{"quit": tc.quitKey}
				m.buildBindings()
			}
			m.ownedSession = "$1"
			m.mode, m.pending = modeConfirm, &pendingOp{kind: opQuitSession, busy: []string{"hx"}}

			if got := quits(pressKey(t, m, tc.key)); got != tc.wantQuit {
				t.Fatalf("quit = %v, want %v", got, tc.wantQuit)
			}
			if got := m.ClosesSession() == "$1"; got != tc.wantClose {
				t.Errorf("ClosesSession = %q, want closing %v", m.ClosesSession(), tc.wantClose)
			}
			if !tc.wantQuit && (m.mode != modeNormal || m.pending != nil) {
				t.Errorf("after backing out: mode %v, pending %+v; want the tree", m.mode, m.pending)
			}
		})
	}
}

// A session ft did not create is never ft's to end, nor is one it cannot place
// itself in, nor any once the setting is off — including turned off by a
// config reload after startup. All of them quit exactly as they always have,
// and none of them reaches tmux to find out.
func TestQuitLeavesTheSessionAlone(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session string
		pane    string
		setting bool
	}{
		{"started inside a session of your own", "", "%1", true},
		{"owner, but no pane to find the session by", "$1", "", true},
		{"setting off", "$1", "%1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := treeModel(t)
			m.ownedSession, m.selfPane = tc.session, tc.pane
			m.cfg.General.QuitClosesSession = tc.setting

			if !quits(pressKey(t, m, ctrlC())) {
				t.Fatal("ctrl+c did not quit")
			}
			if got := m.ClosesSession(); got != "" {
				t.Errorf("quit asked for session %q to be closed", got)
			}
		})
	}
}

// The help says ctrl+c ends the session only where it does.
func TestQuitHelpFollowsTheSetting(t *testing.T) {
	desc := func(m *Model) string {
		for _, r := range m.helpRows() {
			if r.key == m.quitKeys() {
				return r.desc
			}
		}
		t.Fatal("no quit row in the help")
		return ""
	}
	m := treeModel(t)
	m.cfg.General.QuitClosesSession = true
	if got := desc(m); strings.Contains(got, "session") {
		t.Errorf("not the owner, but the help says %q", got)
	}
	m.ownedSession, m.selfPane = "$1", "%1"
	if got := desc(m); !strings.Contains(got, "session") {
		t.Errorf("owner, but the help says %q", got)
	}
	m.cfg.General.QuitClosesSession = false
	if got := desc(m); strings.Contains(got, "session") {
		t.Errorf("setting off, but the help says %q", got)
	}
}

func TestQuitConfirmationSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		busy   []string
		others int
		want   string
	}{
		{[]string{"hx"}, 0, "hx still running."},
		{nil, 1, "attached elsewhere."},
		{[]string{"hx", "hx"}, 2, "hx ×2 still running; attached in 2 other places."},
	} {
		m := treeModel(t)
		m.mode, m.pending = modeConfirm, &pendingOp{kind: opQuitSession, busy: tc.busy, others: tc.others}
		// One Render call wraps the whole sentence, so it is contiguous
		// even with the styling left in.
		if got := m.renderConfirm(); !strings.Contains(got, tc.want) {
			t.Errorf("busy %v, others %d: prompt %q, want it to say %q", tc.busy, tc.others, got, tc.want)
		}
	}
}

func TestDescribeBusy(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"hx"}, "hx"},
		{[]string{"hx", "delta"}, "hx, delta"},
		{[]string{"hx", "delta", "hx"}, "hx ×2, delta"},
	} {
		if got := describeBusy(tc.in); got != tc.want {
			t.Errorf("describeBusy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
