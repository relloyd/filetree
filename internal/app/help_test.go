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

	// Without this the test would pass just as well on a list that fits, which
	// is not the situation being guarded.
	const height = 48
	if len(rows) <= height {
		t.Fatalf("only %d help rows against a height of %d: one column still fits, so this test proves nothing", len(rows), height)
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
