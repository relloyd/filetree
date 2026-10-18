package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// letter is a printable key as the terminal delivers it.
func letter(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// chordModel is a tree with one command on a chord of its own, "n x", whose
// run leaves a file behind — the way to see that a sequence ran something
// without needing tmux.
func chordModel(t *testing.T, cmds map[string]config.Command) *Model {
	t.Helper()
	m := treeModel(t)
	m.cfg.Commands = cmds
	m.buildBindings()
	return m
}

func touchCommand(t *testing.T, key string) (config.Command, string) {
	ran := filepath.Join(t.TempDir(), "ran")
	return config.Command{Run: "touch " + ran, Mode: config.ModeBackground, Key: key}, ran
}

func ranCommand(t *testing.T, cmd tea.Cmd, path string) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	if msg, ok := cmd().(cmdDoneMsg); !ok || msg.err != nil {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// The first key waits and runs nothing; the second completes the binding.
func TestChordRunsOnItsSecondKey(t *testing.T) {
	c, ran := touchCommand(t, "n x")
	m := chordModel(t, map[string]config.Command{"mine": c})

	if cmd := pressKey(t, m, letter('n')); cmd != nil {
		t.Fatal(`"n" ran something on its own`)
	}
	if m.chord != "n" {
		t.Fatalf("chord = %q after n, want it waiting on n", m.chord)
	}
	if !ranCommand(t, pressKey(t, m, letter('x')), ran) {
		t.Fatal(`"n x" did not run the command bound to it`)
	}
	if m.chord != "" {
		t.Errorf("chord = %q after it completed, want it cleared", m.chord)
	}
}

// Esc and ctrl+c back out of a half-typed chord and nothing else: esc is also
// clear-marks and ctrl+c is quit, and neither may fire from inside a chord.
func TestChordCancels(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}},
		{"ctrl+c", ctrlC()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := treeModel(t)
			m.marked[m.tr.Root.Path] = true
			pressKey(t, m, letter('n'))
			if cmd := pressKey(t, m, tc.key); quits(cmd) {
				t.Fatal("cancelling the chord quit ft")
			}
			if m.chord != "" {
				t.Errorf("chord = %q, want it cancelled", m.chord)
			}
			if !m.marked[m.tr.Root.Path] {
				t.Error("esc cleared the marks as well as the chord")
			}
		})
	}
}

// A key that completes nothing cancels the chord and is swallowed: it was typed
// as the end of a chord, not meant for the tree.
func TestChordSwallowsAnUnboundKey(t *testing.T) {
	c, ran := touchCommand(t, "z")
	m := chordModel(t, map[string]config.Command{"zed": c})
	pressKey(t, m, letter('n'))
	if ranCommand(t, pressKey(t, m, letter('z')), ran) {
		t.Fatal(`"n z" ran z's own command`)
	}
	if m.chord != "" {
		t.Errorf("chord = %q, want it cancelled", m.chord)
	}
	if !strings.Contains(m.statusMsg, "n z is not bound") {
		t.Errorf("status = %q, want it to say n z is not bound", m.statusMsg)
	}
}

// hjkl are the arrows inside a chord. Outside tmux the shell actions say so,
// which is how this tells "n l" reached shell-right from it being unbound.
func TestChordTakesHJKLForArrows(t *testing.T) {
	for _, k := range []rune{'h', 'j', 'k', 'l'} {
		m := treeModel(t)
		m.selfPane = ""
		pressKey(t, m, letter('n'))
		pressKey(t, m, letter(k))
		if m.statusMsg != "not running inside tmux" {
			t.Errorf("n %c: status = %q, want a shell action's answer", k, m.statusMsg)
		}
	}
}

// The alias is a fallback: a chord that names the letter outright keeps it.
func TestChordLetterBeatsItsArrowAlias(t *testing.T) {
	c, ran := touchCommand(t, "n l")
	m := chordModel(t, map[string]config.Command{"mine": c})
	pressKey(t, m, letter('n'))
	if !ranCommand(t, pressKey(t, m, letter('l')), ran) {
		t.Error(`"n l" went to the arrow alias rather than its own binding`)
	}
}

// A key bound on its own cannot also start a chord. The chord wins, and the
// loser is reported rather than going quiet.
func TestChordPrefixDisplacesASingleKey(t *testing.T) {
	c, _ := touchCommand(t, "n")
	m := chordModel(t, map[string]config.Command{"old-n": c})
	if !mentions(m.keyConflicts, "n", "commands.old-n") {
		t.Errorf("the command on n went unreported: %v", m.keyConflicts)
	}
	if cmd := pressKey(t, m, letter('n')); cmd != nil || m.chord != "n" {
		t.Errorf("n ran something (cmd=%v) or did not wait (chord=%q)", cmd != nil, m.chord)
	}
}

// Navigation cannot be given up, so a chord starting on one of its keys is the
// one refused.
func TestChordCannotStartOnNavigation(t *testing.T) {
	c, _ := touchCommand(t, "j x")
	m := chordModel(t, map[string]config.Command{"mine": c})
	if !mentions(m.keyConflicts, "j x", "commands.mine") {
		t.Errorf("the refused chord went unreported: %v", m.keyConflicts)
	}
	before := m.cursor
	pressKey(t, m, letter('j'))
	if m.chord != "" || m.cursor == before {
		t.Errorf("j started a chord (%q) or stopped moving down", m.chord)
	}
}

// Chords can be longer than two keys, and the spacing in the config does not
// matter.
func TestChordOfThreeKeysWrittenLoosely(t *testing.T) {
	c, ran := touchCommand(t, "  g   x y ")
	m := chordModel(t, map[string]config.Command{"mine": c})
	// "g" is navigation, so that one is refused; move it to a free letter.
	if !mentions(m.keyConflicts, "g x y", "commands.mine") {
		t.Fatalf("g x y was not refused: %v", m.keyConflicts)
	}
	m.cfg.Keys = map[string]string{"mine": "q  x y"}
	m.buildBindings()
	pressKey(t, m, letter('q'))
	pressKey(t, m, letter('x'))
	if m.chord != "q x" {
		t.Fatalf("chord = %q after q x, want it still waiting", m.chord)
	}
	if !ranCommand(t, pressKey(t, m, letter('y')), ran) {
		t.Error(`"q x y" did not run`)
	}
}

// The status bar says what can follow while a chord waits, arrows first.
func TestChordHintsInTheStatusBar(t *testing.T) {
	m := chordModel(t, config.Default().Commands)
	m.width = 120
	m.note("something earlier", false)
	pressKey(t, m, letter('n'))
	bar := plainText(m.renderStatus())
	for _, want := range []string{"n …", "→ beside", "↓ below", "↑ above", "← left edge", "space popup", "esc cancel"} {
		if !strings.Contains(bar, want) {
			t.Errorf("status bar %q lacks %q", bar, want)
		}
	}
	if strings.Contains(bar, "something earlier") {
		t.Error("an old message hid the chord hints")
	}
	if strings.Index(bar, "beside") > strings.Index(bar, "popup") {
		t.Error("the arrows should lead the hints")
	}
}

// A click is not the end of a chord.
func TestChordCancelledByTheMouse(t *testing.T) {
	m := treeModel(t)
	pressKey(t, m, letter('n'))
	m.handleClick(tea.Mouse{Button: tea.MouseLeft, Y: 1})
	if m.chord != "" {
		t.Errorf("chord = %q after a click, want it cancelled", m.chord)
	}
}

func TestKeyLabel(t *testing.T) {
	for in, want := range map[string]string{
		"n right": "n →",
		"n left":  "n ←",
		"n space": "n space",
		"right":   "right",
		"alt+n":   "alt+n",
	} {
		if got := keyLabel(in); got != want {
			t.Errorf("keyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// A chord left half-typed when something async raised a confirmation must not
// take the first key typed after the confirmation closes.
func TestChordDoesNotSurviveAnotherMode(t *testing.T) {
	m := treeModel(t)
	pressKey(t, m, letter('n'))
	m.mode = modeConfirm // as handleWorktreeRemoved does, between two key presses
	m.pending = &pendingOp{kind: opTrash}
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.chord != "" {
		t.Fatalf("chord = %q after the confirmation, want it dropped", m.chord)
	}
	before := m.cursor
	pressKey(t, m, letter('j'))
	if m.cursor == before {
		t.Error("j after the confirmation finished the old chord instead of moving down")
	}
}

// esc and ctrl+c cancel before anything is looked up, so a chord continuing
// with either could never run; it is refused and said so.
func TestChordCannotContinueWithEsc(t *testing.T) {
	m := treeModel(t)
	m.cfg.Keys = map[string]string{"collapse-all": "n esc"}
	m.buildBindings()
	if !mentions(m.keyConflicts, "n esc", "keys.collapse-all") {
		t.Errorf("n esc went unreported: %v", m.keyConflicts)
	}
	if got := m.actionKeys["collapse-all"]; got != "" {
		t.Errorf("collapse-all = %q, want no key now that its chord is refused", got)
	}
}

// What settleChords takes away is taken off the help page too.
func TestChordLoserLeavesTheHelpPage(t *testing.T) {
	c, _ := touchCommand(t, "n")
	m := chordModel(t, map[string]config.Command{"old-n": c})
	m.cfg.CommandOrder = []string{"old-n"}
	if got := m.actionKeys["old-n"]; got != "" {
		t.Errorf("old-n = %q, want it unbound once the chord took n", got)
	}
	for _, r := range m.helpRows() {
		if r.desc == "run: old-n" {
			t.Errorf("help still lists old-n on %q", r.key)
		}
	}
}

// The finder matches its own keys one press at a time, so a chord for one of
// them is refused and the default kept.
func TestFinderActionsTakeNoChords(t *testing.T) {
	m := treeModel(t)
	m.cfg.Keys = map[string]string{"finder-more": "ctrl+a m"}
	m.buildBindings()
	if !mentions(m.keyConflicts, "ctrl+a m", "keys.finder-more") {
		t.Errorf("the finder chord went unreported: %v", m.keyConflicts)
	}
	if got, want := m.actionKeys["finder-more"], defaultActionKeys["finder-more"]; got != want {
		t.Errorf("finder-more = %q, want its default %q", got, want)
	}
}

// The hjkl note belongs to the chord, not to the action.
func TestShellRightHelpFollowsItsKey(t *testing.T) {
	m := treeModel(t)
	if d := m.shellRightDesc(); !strings.Contains(d, "hjkl") {
		t.Errorf("default desc = %q, want the hjkl note", d)
	}
	m.cfg.Keys = map[string]string{"shell-right": "N"}
	m.buildBindings()
	if d := m.shellRightDesc(); strings.Contains(d, "hjkl") {
		t.Errorf("desc on N = %q, want no hjkl note", d)
	}
}
