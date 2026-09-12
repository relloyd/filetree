package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

	out := m.layoutHelp(rows, height, 200, 0)
	for _, r := range rows {
		if !strings.Contains(out, r.desc) {
			t.Errorf("help row %q (%s) is missing at 200x%d", r.key, r.desc, height)
		}
	}
	if strings.Contains(out, "more") {
		t.Errorf("help reported dropped rows on a pane wide enough for every one of them:\n%s", out)
	}
}

// Where columns cannot rescue it — a sidebar-width pane — the list does not all
// fit, and it has to say how much is off the page rather than just ending.
func TestHelpSaysWhatIsOffThePage(t *testing.T) {
	m := starterHelpModel(t)
	rows := m.helpRows()
	out := m.layoutHelp(rows, 20, 60, 0)
	if !strings.Contains(out, "↓ ") {
		t.Errorf("no count of the rows below on a pane too small for %d of them:\n%s", len(rows), out)
	}
	if strings.Contains(out, "↑ ") {
		t.Error("the page reported rows above it while sitting at the top")
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
		for _, line := range strings.Split(m.layoutHelp(rows, size.h, size.w, 0), "\n") {
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
		if got := len(strings.Split(m.layoutHelp(rows, size.h, size.w, 0), "\n")); got != size.h {
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

	clean := m.layoutHelp(rows, h, w, 0)
	m.cfg.Keys = map[string]string{"worktree-new": "R"}
	m.cfg.Unknown = []string{"commands.diff.worktree-new"}
	m.buildBindings()

	if got := m.layoutHelp(m.helpRows(), h, w, 0); got != clean {
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

// Everything the page can show has to be reachable by scrolling. This is the
// whole point of the change: on a narrow pane the tail used to be counted and
// then left where nobody could get at it.
func TestHelpScrollsToEveryRow(t *testing.T) {
	m := starterHelpModel(t)
	m.width, m.height = 60, 22
	rows := m.helpVisibleRows()

	capacity := m.helpCapacity(rows)
	if capacity >= len(rows) {
		t.Fatalf("this pane fits all %d rows, so it cannot test scrolling", len(rows))
	}

	// The last row must appear once the page is scrolled to the bottom.
	last := rows[len(rows)-1].desc
	bottom := m.layoutHelp(rows, m.treeHeight(), m.width, len(rows))
	if !strings.Contains(bottom, truncate(last, 40)) {
		t.Errorf("the final row never comes into view:\n%s", bottom)
	}

	// And scrolling past the end is refused rather than showing blankness.
	m.helpScroll = 9999
	m.clampHelpScroll()
	if want := len(rows) - capacity; m.helpScroll != want {
		t.Errorf("scroll clamped to %d, want %d so the last screenful stays full", m.helpScroll, want)
	}
	m.helpScroll = -5
	m.clampHelpScroll()
	if m.helpScroll != 0 {
		t.Errorf("scroll clamped to %d, want 0", m.helpScroll)
	}
}

// Once scrolled, the footer has to say there is a way back up. A reader who has
// lost their place needs both numbers, not just the tail.
func TestHelpFooterCountsBothDirections(t *testing.T) {
	m := starterHelpModel(t)
	m.width, m.height = 60, 22
	rows := m.helpVisibleRows()

	out := m.layoutHelp(rows, m.treeHeight(), m.width, 5)
	if !strings.Contains(out, "↑ 5") {
		t.Errorf("no count of the rows above after scrolling:\n%s", out)
	}
	if !strings.Contains(out, "↓ ") {
		t.Errorf("no count of the rows below after scrolling:\n%s", out)
	}
}

// Typing filters, and it matches the key columns as well as the description so
// that "ctrl" finds the chords.
func TestHelpFilter(t *testing.T) {
	m := starterHelpModel(t)
	all := len(m.helpVisibleRows())

	m.helpInput.SetValue("worktree")
	got := m.helpVisibleRows()
	if len(got) == 0 || len(got) >= all {
		t.Fatalf("filtering by description kept %d of %d rows", len(got), all)
	}
	for _, r := range got {
		if !strings.Contains(strings.ToLower(helpSearchText(r)), "worktree") {
			t.Errorf("row %q does not match the filter", r.desc)
		}
	}

	m.helpInput.SetValue("ctrl")
	if got := m.helpVisibleRows(); len(got) == 0 {
		t.Error("filtering by key found nothing for \"ctrl\"")
	}

	// The finder's own query syntax, so "!" excludes here as it does there.
	m.helpInput.SetValue("!a !e !i !o !u")
	if got, all := len(m.helpVisibleRows()), all; got >= all {
		t.Errorf("exclusions kept %d of %d rows, want fewer", got, all)
	}

	m.helpInput.SetValue("zzzznothing")
	if got := m.helpVisibleRows(); len(got) != 0 {
		t.Errorf("a query matching nothing kept %d rows", len(got))
	}
}

// The page is a reference, so it keeps its own grouping rather than taking the
// fuzzy matcher's ranking. Navigation first, then the commands in catalogue
// order, filtered or not.
func TestHelpFilterKeepsPageOrder(t *testing.T) {
	m := starterHelpModel(t)
	all := m.helpRows()
	position := map[string]int{}
	for i, r := range all {
		position[r.desc] = i
	}

	m.helpInput.SetValue("e")
	got := m.helpVisibleRows()
	if len(got) < 3 {
		t.Fatalf("filter kept only %d rows, too few to test ordering", len(got))
	}
	for i := 1; i < len(got); i++ {
		if position[got[i-1].desc] >= position[got[i].desc] {
			t.Errorf("filtered rows are out of page order: %q came before %q",
				got[i-1].desc, got[i].desc)
		}
	}
}

// A filter that matches nothing has to say so rather than leaving a blank page.
func TestHelpSaysWhenNothingMatches(t *testing.T) {
	m := starterHelpModel(t)
	m.helpInput.SetValue("zzzznothing")
	out := m.layoutHelp(m.helpVisibleRows(), m.treeHeight(), 80, 0)
	if !strings.Contains(out, "nothing matches") {
		t.Errorf("an empty result rendered as a blank page:\n%s", out)
	}
}

// Changing the filter has to put the page back to the top: the rows underneath
// have changed, and an offset measured against the old set would start the new
// one part way down.
func TestHelpFilterResetsScroll(t *testing.T) {
	m := starterHelpModel(t)
	m.width, m.height = 60, 22
	m.toggleHelp() // focuses the filter, as pressing "?" does
	m.helpScroll = 5

	m.handleHelpKey(tea.KeyPressMsg{Code: 'w', Text: "w"}, "w")
	if m.helpScroll != 0 {
		t.Errorf("scroll = %d after typing, want the page back at the top", m.helpScroll)
	}

	// A key the input ignores must leave the position alone, or the page would
	// jump to the top on every stray press.
	m.helpScroll = 5
	m.handleHelpKey(tea.KeyPressMsg{Code: tea.KeyF1}, "f1")
	if m.helpScroll != 5 {
		t.Errorf("scroll = %d after a key that changed nothing, want 5", m.helpScroll)
	}
}

// esc leaves, and leaves nothing behind: the next visit starts clean.
func TestHelpEscapeClosesAndClears(t *testing.T) {
	m := starterHelpModel(t)
	m.toggleHelp()
	m.helpInput.SetValue("worktree")
	m.helpScroll = 4

	m.handleHelpKey(tea.KeyPressMsg{Code: tea.KeyEscape}, "esc")
	if m.mode != modeNormal {
		t.Errorf("mode = %v after esc, want modeNormal", m.mode)
	}
	if m.helpInput.Value() != "" || m.helpScroll != 0 {
		t.Errorf("esc left filter %q and scroll %d behind", m.helpInput.Value(), m.helpScroll)
	}
}

// The wheel is what the page is discovered with, so it has to move it.
func TestHelpWheelScrolls(t *testing.T) {
	m := starterHelpModel(t)
	m.width, m.height = 60, 22
	m.toggleHelp()

	m.handleWheel(tea.Mouse{Button: tea.MouseWheelDown})
	if m.helpScroll <= 0 {
		t.Errorf("scroll = %d after a wheel-down, want it to have moved", m.helpScroll)
	}
	down := m.helpScroll
	m.handleWheel(tea.Mouse{Button: tea.MouseWheelUp})
	if m.helpScroll >= down {
		t.Errorf("scroll = %d after a wheel-up, want less than %d", m.helpScroll, down)
	}
}
