package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// markedModel is a model over a root of files, with an "edit" command on "e"
// that echoes whatever {paths} expanded to, so a test can read back exactly
// what the command was going to act on.
func markedModel(t *testing.T, files ...string) *Model {
	t.Helper()
	m := recentModel(t, files...)
	// echo, not printf: printf reuses its format for each argument, so a list
	// would come back concatenated and the ordering assertions would be blind.
	m.cfg.Commands = map[string]config.Command{
		"edit": {Run: "echo {paths}", Mode: config.ModeBackground, Key: "e", FinderKey: "ctrl+e"},
	}
	m.buildBindings()
	return m
}

// mark adds rel to the marked set in order, the way tapping space does.
func mark(t *testing.T, m *Model, rel string) {
	t.Helper()
	abs := filepath.Join(m.tr.Root.Path, filepath.FromSlash(rel))
	if m.tr.FindByPath(abs) == nil {
		t.Fatalf("%s is not in the tree", rel)
	}
	m.marked[abs] = true
	m.markOrder = append(m.markOrder, abs)
}

// pressE runs the command bound to "e" and returns what it printed.
func pressE(t *testing.T, m *Model) string {
	t.Helper()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if cmd == nil {
		t.Fatal(`"e" produced no command`)
	}
	return strings.TrimRight(drainCmdDone(t, cmd).out, "\n")
}

// The whole point: one command, every marked file, in the order they were
// marked — helix opens buffers in argument order.
func TestMarkedFilesReachTheCommandInMarkOrder(t *testing.T) {
	m := markedModel(t, "one.go", "two.go", "three.go")
	for _, rel := range []string{"two.go", "one.go", "three.go"} {
		mark(t, m, rel)
	}

	got := pressE(t, m)

	root := m.tr.Root.Path
	want := strings.Join([]string{
		filepath.Join(root, "two.go"),
		filepath.Join(root, "one.go"),
		filepath.Join(root, "three.go"),
	}, " ")
	if got != want {
		t.Errorf("command acted on %q, want %q", got, want)
	}
}

// With nothing marked the command still gets the cursor, so the key behaves as
// it always did.
func TestUnmarkedCommandStillActsOnTheSelection(t *testing.T) {
	m := markedModel(t, "one.go", "two.go")
	abs := filepath.Join(m.tr.Root.Path, "two.go")
	m.selectPath(abs)

	if got := pressE(t, m); got != abs {
		t.Errorf("command acted on %q, want the selection %q", got, abs)
	}
}

// A file deleted after it was marked would otherwise be opened as an empty
// buffer. It is dropped and unmarked, the way d and p/m already treat one.
func TestAVanishedMarkIsDroppedAndUnmarked(t *testing.T) {
	m := markedModel(t, "one.go", "two.go")
	mark(t, m, "one.go")
	mark(t, m, "two.go")
	gone := filepath.Join(m.tr.Root.Path, "one.go")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	got := pressE(t, m)

	if want := filepath.Join(m.tr.Root.Path, "two.go"); got != want {
		t.Errorf("command acted on %q, want only %q", got, want)
	}
	if m.marked[gone] || len(m.markOrder) != 1 {
		t.Errorf("the dead mark survived: marked=%v order=%v", m.marked, m.markOrder)
	}
}

func TestAllMarksVanishedIsReportedRatherThanRun(t *testing.T) {
	m := markedModel(t, "one.go")
	mark(t, m, "one.go")
	if err := os.Remove(filepath.Join(m.tr.Root.Path, "one.go")); err != nil {
		t.Fatal(err)
	}

	// note sets the message synchronously and returns a 4-second tick to clear
	// it; running that tick is what a test must not do.
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"}); cmd == nil {
		t.Fatal("expected a status note")
	}
	if !strings.Contains(m.statusMsg, "no longer exist") {
		t.Errorf("status = %q, want it to say the marks are gone", m.statusMsg)
	}
	if !m.statusErr {
		t.Error("the note should be an error")
	}
}

// Marks belong to the tree. In the finder the target is the row under its own
// cursor, and it carries the Grep line — letting tree marks in would open
// something other than what is highlighted.
func TestFinderCommandIgnoresTreeMarks(t *testing.T) {
	m := markedModel(t, "one.go", "two.go")
	mark(t, m, "one.go")
	mark(t, m, "two.go")

	m.startFuzzy()
	m.fuzzyCands = []string{"two.go"}
	m.refuzzy()
	m.fuzzySel = 0

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+e produced no command")
	}
	got := strings.TrimRight(drainCmdDone(t, cmd).out, "\n")
	if want := filepath.Join(m.tr.Root.Path, "two.go"); got != want {
		t.Errorf("finder command acted on %q, want the highlighted row %q", got, want)
	}
}

// clear_marks_after_command is off by default, and when on it fires only for a
// command that actually took the marks as its target.
func TestClearMarksAfterCommandIsConfigurable(t *testing.T) {
	setUp := func(t *testing.T, on bool, run string) *Model {
		m := markedModel(t, "one.go", "two.go")
		m.cfg.General.ClearMarksAfterCommand = on
		m.cfg.Commands = map[string]config.Command{
			"edit": {Run: run, Mode: config.ModeBackground, Key: "e"},
		}
		m.buildBindings()
		mark(t, m, "one.go")
		mark(t, m, "two.go")
		return m
	}

	t.Run("off by default", func(t *testing.T) {
		m := setUp(t, false, "printf '%s' {paths}")
		pressE(t, m)
		if len(m.marked) != 2 {
			t.Errorf("marks = %v, want them kept", m.markOrder)
		}
	})

	t.Run("on, and the command took the marks", func(t *testing.T) {
		m := setUp(t, true, "printf '%s' {paths}")
		pressE(t, m)
		if len(m.marked) != 0 || len(m.markOrder) != 0 {
			t.Errorf("marks = %v, want them consumed", m.markOrder)
		}
	})

	t.Run("on, but the command never named them", func(t *testing.T) {
		m := setUp(t, true, "printf '%s' {dir}")
		pressE(t, m)
		if len(m.marked) != 2 {
			t.Errorf("marks = %v, want a command that ignored them to leave them", m.markOrder)
		}
	})

	// "S" opens a brand new scratch file through the default command, which
	// names {paths} — but the file it opens is not one of the marks, so the set
	// is none of its business.
	t.Run("on, but the target is not a mark", func(t *testing.T) {
		m := setUp(t, true, "printf '%s' {paths}")
		elsewhere := filepath.Join(m.tr.Root.Path, "two.go")
		m.execCommand("edit", m.cfg.Commands["edit"], config.Vars{Path: elsewhere}, false)
		if len(m.marked) != 2 {
			t.Errorf("marks = %v, want them left alone", m.markOrder)
		}
	})
}

// Opening five files should put five files in the history, not one.
func TestOpeningMarkedFilesRecordsThemAll(t *testing.T) {
	m := markedModel(t, "one.go", "two.go", "three.go")
	for _, rel := range []string{"one.go", "two.go", "three.go"} {
		mark(t, m, rel)
	}

	pressE(t, m)

	got := make([]string, len(m.recent.Files))
	for i, f := range m.recent.Files {
		got[i] = f.Path
	}
	// Newest first, and the last one marked was opened last.
	if strings.Join(got, ",") != "three.go,two.go,one.go" {
		t.Errorf("history = %v, want all three newest first", got)
	}
}
