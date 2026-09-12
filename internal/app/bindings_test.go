package app

import (
	"os"
	"path/filepath"
	"strings"
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

	// Commands and actions share one key namespace, so an override cannot
	// quietly take a key something else already holds by default — the same
	// rule that has always applied between two actions. Asking for rename on
	// tab is refused, rename keeps "R", and the command keeps running.
	m.cfg.Keys = map[string]string{"rename": "tab"}
	m.buildBindings()
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd == nil {
		t.Fatal(`"tab" produced no command after the refused override`)
	}
	if msg, ok := cmd().(cmdDoneMsg); !ok || msg.name != "focus-right" {
		t.Errorf("tab no longer runs the command that holds it: %T %+v", cmd(), msg)
	}
	if !mentions(m.keyConflicts, "tab", "keys.rename") {
		t.Errorf("the refused override went unreported: %v", m.keyConflicts)
	}
	if got := m.actionKeys["rename"]; got != "R" {
		t.Errorf("rename = %q, want it back on its default R", got)
	}
}

// A command's key is remappable by name, which is the point of putting
// commands and actions in one [keys] namespace: before this, moving a
// command's key meant copying its whole definition into the config.
func TestKeysCanMoveACommand(t *testing.T) {
	m := finderModel()
	m.mode = modeNormal
	m.cfg.Commands = map[string]config.Command{
		"claude-popup": {Name: "claude-popup", Run: "true", Mode: config.ModeBackground, Key: "c"},
	}
	m.cfg.Keys = map[string]string{"claude-popup": "z"}
	m.buildBindings()

	if got := m.actionKeys["claude-popup"]; got != "z" {
		t.Fatalf("claude-popup = %q, want z", got)
	}
	if _, ok := m.bindings["z"]; !ok {
		t.Error(`"z" is not bound after the override`)
	}
	if _, ok := m.bindings["c"]; ok {
		t.Error(`"c" should be free once the command moved off it`)
	}
}

// A command sharing a name with an action is refused rather than merged: one
// name in [keys] cannot mean two things.
func TestCommandCannotTakeAnActionName(t *testing.T) {
	m := finderModel()
	m.mode = modeNormal
	m.cfg.Commands = map[string]config.Command{
		"rename": {Name: "rename", Run: "true", Mode: config.ModeBackground, Key: "Q"},
	}
	m.buildBindings()

	if !mentions(m.keyConflicts, "Q", "commands.rename") {
		t.Errorf("the name clash went unreported: %v", m.keyConflicts)
	}
	// The action keeps its own key and stays reachable.
	if got := m.actionKeys["rename"]; got != "R" {
		t.Errorf("rename = %q, want the action's default R", got)
	}
}

// mentions reports whether some conflict is about key and names who lost.
func mentions(conflicts []keyConflict, key, refused string) bool {
	for _, c := range conflicts {
		if c.key == key && c.refused == refused {
			return true
		}
	}
	return false
}

// The bug this replaces: two actions on one key were both written into a plain
// map, so the winner was whichever Go's randomised iteration reached last — the
// same config could rename on one launch and ask for a worktree on the next.
// Rebuilding many times over is what makes that visible; a single pass had a
// coin's chance of looking fine.
func TestAClashingKeyResolvesTheSameWayEveryTime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		m := rootedModel(t, dir)
		m.mode = modeNormal
		m.cursor = 1 // the file: the root itself cannot be renamed
		m.cfg.Keys = map[string]string{"worktree-new": "R"}
		m.buildBindings()

		if got := m.actionKeys["rename"]; got != "R" {
			t.Fatalf("run %d: rename = %q, want R — the override displaced it", i, got)
		}
		if got := m.actionKeys["worktree-new"]; got != "W" {
			t.Fatalf("run %d: worktree-new = %q, want W — it should fall back", i, got)
		}
		m.handleKey(tea.KeyPressMsg{Code: 'R', Text: "R"})
		if m.mode != modePrompt || m.prompt != promptRename {
			t.Fatalf("run %d: R gave mode=%v prompt=%v, want the rename prompt", i, m.mode, m.prompt)
		}
		if !mentions(m.keyConflicts, "R", "keys.worktree-new") {
			t.Fatalf("run %d: the clash was not reported: %v", i, m.keyConflicts)
		}
	}
}

// Navigation is claimed last and cannot be remapped, so anything a config puts
// on one of its keys is dead on arrival. That is survivable; being dead and
// silent is not.
func TestNavigationKeepsItsKeysAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, dir)
	m.mode = modeNormal
	m.cfg.Commands = map[string]config.Command{
		"down-ish": {Run: "true", Mode: config.ModeBackground, Key: "j"},
	}
	m.cfg.Keys = map[string]string{"quit": "k"}
	m.buildBindings()

	if !mentions(m.keyConflicts, "j", "commands.down-ish") {
		t.Errorf(`a command on "j" went unreported: %v`, m.keyConflicts)
	}
	if !mentions(m.keyConflicts, "k", "keys.quit") {
		t.Errorf(`quit on "k" went unreported: %v`, m.keyConflicts)
	}
	// And they really are still navigation, not merely reported as such.
	m.cursor = 0
	m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.cursor == 0 {
		t.Error(`"j" stopped moving the cursor`)
	}
}

// The status bar points at the help page, so the help page has to have it.
func TestHelpListsKeyConflicts(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.cfg.Keys = map[string]string{"worktree-new": "R"}
	m.buildBindings()

	if note := m.configNote(); !strings.Contains(note, "key conflict") {
		t.Errorf("status note = %q, want it to mention the conflict", note)
	}
	warn := strings.Join(m.helpWarnings(100), "\n")
	if !strings.Contains(warn, "worktree-new") || !strings.Contains(warn, "config warning") {
		t.Errorf("the help page does not list the conflict on R:\n%s", warn)
	}

	// A clean config must not grow a warnings block out of nowhere.
	m.cfg.Keys = nil
	m.buildBindings()
	if n := len(m.keyConflicts); n != 0 {
		t.Fatalf("a clean config reported %d conflicts: %v", n, m.keyConflicts)
	}
	if got := m.helpWarnings(100); len(got) != 0 {
		t.Errorf("a clean config still rendered a warnings block: %q", got)
	}
}

// A setting that decoded into nothing is the other half of the same silence: it
// reads like a working line and does nothing whatever. The classic way to get
// one is a keybinding under a [keys] header that is still commented out — TOML
// files it under the last table declared, which is some command, as a field
// that command does not have.
func TestUnknownSettingsAreReported(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.cfg.Unknown = []string{"commands.diff.worktree-new"}
	m.buildBindings()

	note := m.configNote()
	if !strings.Contains(note, "commands.diff.worktree-new") || !strings.Contains(note, "unknown setting") {
		t.Errorf("status note = %q, want it to name the setting", note)
	}
	warn := strings.Join(m.helpWarnings(100), "\n")
	if !strings.Contains(warn, "commands.diff.worktree-new") {
		t.Errorf("the help page does not list the unknown setting:\n%s", warn)
	}

	// Both kinds at once are counted together rather than one hiding the other.
	m.cfg.Keys = map[string]string{"worktree-new": "R"}
	m.buildBindings()
	if note := m.configNote(); !strings.Contains(note, "2 config warnings") {
		t.Errorf("status note = %q, want both counted", note)
	}
}

// The shipped set has to be clash-free on its own, commands included.
//
// TestResolveActionKeysDefaultsAreConflictFree covers the actions, but commands
// and actions share one key namespace, and nothing was checking that a command
// arriving in the catalogue had not landed on a key something else already
// held. A clash is not fatal — ft starts, says how many it found and lists them
// in "?" — so a new command could take a key from an action and the only sign
// would be a line in the help page nobody reads.
func TestShippedKeysDoNotClash(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.cfg.Commands = map[string]config.Command{}
	for _, c := range config.Builtin {
		m.cfg.Commands[c.Name] = c
	}
	m.cfg.Keys = nil
	m.buildBindings()

	if len(m.keyConflicts) != 0 {
		t.Errorf("the shipped keys clash: %v", m.keyConflicts)
	}

	// And every command that ships with a key keeps it, which is the outcome a
	// clash would otherwise quietly take away.
	for _, c := range config.Builtin {
		if c.Key == "" {
			continue
		}
		if got := m.actionKeys[c.Name]; got != c.Key {
			t.Errorf("%s bound to %q, want %q", c.Name, got, c.Key)
		}
	}
}
