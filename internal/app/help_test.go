package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/relloyd/filetree/internal/config"
)

// starterHelpModel is a model carrying the command set ft actually ships, so
// these tests track the starter rather than a hand-written copy of it that
// would quietly stop resembling it.
func starterHelpModel(t *testing.T) *Model {
	t.Helper()
	cfg, err := config.EnsureAndLoad(t.TempDir()) // writes the starter, then loads it
	if err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, t.TempDir())
	m.cfg = cfg
	m.buildBindings()
	return m
}

// The help list is the fixed keys plus a row per command key, and the tmux pane
// commands pushed it past a single screen: 51 rows against the ~48 a 50-row
// terminal leaves. The old renderer stopped at the height and dropped the rest
// silently — and since commands sort after the fixed keys, the rows it dropped
// were always the commands, i.e. exactly the part that varies by config.
func TestHelpShowsEveryRowOnAWidePane(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()

	// A height the list cannot fit in one column, derived rather than fixed:
	// merging the finder rows into their own column took the starter from ~55
	// rows to ~49, which a hardcoded 48 survived by one row.
	height := len(rows) - 3
	if height < 10 {
		t.Fatalf("only %d help rows: too few for this test to prove anything", len(rows))
	}

	out := m.layoutHelp(rows, height, 200)
	for _, r := range rows {
		if !strings.Contains(out, r.desc) {
			t.Errorf("help row %q (%s) is missing at 200x%d", r.key, r.desc, height)
		}
	}
	if strings.Contains(out, "more") {
		t.Errorf("help reported dropped rows on a pane wide enough for every one of them:\n%s", out)
	}
}

// Where columns cannot rescue it — a sidebar-width pane — the list is still
// cut, but it has to say so rather than just ending.
func TestHelpSaysWhenItHasDroppedRows(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()
	out := m.layoutHelp(rows, 20, 60)
	if !strings.Contains(out, "… ") || !strings.Contains(out, "more") {
		t.Errorf("no \"… N more\" marker on a pane too small for %d rows:\n%s", len(rows), out)
	}
}

// Nothing may exceed the pane width, in either layout. A single column is never
// narrowed to fit, so an over-long description would wrap onto the next line —
// and every line after it in the help would then be one row further down than
// the renderer thinks, pushing the status bar off the bottom.
func TestHelpNeverExceedsTheWidth(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()
	for _, size := range []struct{ w, h int }{{200, 48}, {160, 48}, {120, 48}, {60, 48}, {40, 18}} {
		for _, line := range strings.Split(m.layoutHelp(rows, size.h, size.w), "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("at %dx%d a help line is %d cells wide: %q", size.w, size.h, got, line)
			}
		}
	}
}

// The height is a hard budget: View stacks the header, this body and the status
// bar, so a body one line over its allowance costs the status bar.
func TestHelpFillsExactlyTheHeight(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()
	for _, size := range []struct{ w, h int }{{200, 48}, {160, 48}, {60, 48}, {200, 12}, {40, 6}} {
		if got := len(strings.Split(m.layoutHelp(rows, size.h, size.w), "\n")); got != size.h {
			t.Errorf("at %dx%d the help body is %d lines, want exactly %d", size.w, size.h, got, size.h)
		}
	}
}

// A command with both keys is one action, so it gets one row carrying both —
// the two rows it used to get differed only by a prefix. And no description may
// still spell out where a key applies: the second column and its colour say it.
func TestHelpMergesTheFinderKeyIntoOneRow(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()

	var both, finderOnly int
	for _, r := range rows {
		if strings.Contains(r.desc, "fuzzy finder:") || strings.Contains(r.desc, "in the finder") {
			t.Errorf("row %q still says where it applies: %q", r.key, r.desc)
		}
		switch {
		case r.key != "" && r.finderKey != "":
			both++
		case r.finderKey != "":
			finderOnly++
		}
	}
	if both == 0 || finderOnly == 0 {
		t.Fatalf("expected rows of both kinds, got %d with two keys and %d finder-only", both, finderOnly)
	}
	// The starter binds edit to e/ctrl+e and focus-right to ctrl+l in both
	// places: one merged row each, and only the second is a ditto.
	for _, tc := range []struct{ key, finderKey, want string }{
		{"e", "ctrl+e", "ctrl+e"},
		{"ctrl+l", "ctrl+l", sameKeyMark},
		{"", "tab", "tab"},
		{"R", "", ""},
	} {
		if got := finderCell(helpRow{key: tc.key, finderKey: tc.finderKey}); got != tc.want {
			t.Errorf("finderCell(%q, %q) = %q, want %q", tc.key, tc.finderKey, got, tc.want)
		}
	}
}

// Warnings are rendered above the table rather than inside it. Inside, their
// sentences set the width of the shared description column, and one conflict
// was enough to cost a wide pane its second column and start truncating the
// bindings themselves.
func TestConfigWarningsDoNotCostTheTableItsColumns(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()
	const w, h = 150, 45

	clean := m.layoutHelp(rows, h, w)
	m.cfg.Keys = map[string]string{"worktree-new": "R"}
	m.cfg.Unknown = []string{"commands.diff.worktree-new"}
	m.buildBindings()

	if got := m.layoutHelp(m.helpRows(), h, w); got != clean {
		t.Error("a config warning changed the shape of the key table")
	}
	// And the table really is multi-column at this size, or the check above
	// would hold just as well for two single-column layouts.
	body := strings.Split(clean, "\n")
	var packed int
	for _, line := range body {
		if strings.Count(line, "move selection")+strings.Count(line, "toggle this help") > 0 && lipgloss.Width(line) > 90 {
			packed++
		}
	}
	if packed == 0 {
		t.Errorf("expected columns side by side at %dx%d:\n%s", w, h, clean)
	}

	warn := strings.Join(m.helpWarnings(w), "\n")
	if !strings.Contains(warn, "config warning") || !strings.Contains(warn, "commands.diff.worktree-new") {
		t.Errorf("the warning block is missing its contents:\n%s", warn)
	}
}

// The warning block is part of the height budget, so its lines have to be
// counted one by one — a wrapped conflict is several lines, not one.
func TestHelpWarningsCountEveryRenderedLine(t *testing.T) {
	m := starterHelpModel(t)
	m.cfg.Keys = map[string]string{"worktree-new": "R"}
	m.buildBindings()

	for _, w := range []int{40, 56, 100, 200} {
		lines := m.helpWarnings(w)
		for _, l := range lines {
			if strings.Contains(l, "\n") {
				t.Errorf("at width %d a warning entry holds more than one line: %q", w, l)
			}
			if got := lipgloss.Width(l); got > w {
				t.Errorf("at width %d a warning line is %d cells wide: %q", w, got, l)
			}
		}
	}
}
