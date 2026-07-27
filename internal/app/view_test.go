package app

import (
	"regexp"
	"strings"
	"testing"
)

var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plainText strips styling, so a test can reason about what actually lands in
// which column.
func plainText(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// The header's toggle buttons are click targets, so the x-ranges recorded for
// hit-testing have to be where the buttons really are. The interesting case is
// a terminal too narrow to squeeze the root path into: the gap bottoms out at
// one column and pushes the buttons right of where the right-aligned layout
// wanted them, which used to leave every zone pointing a couple of columns
// short and a click on "hidden" landing on the path.
func TestHeaderZonesMatchTheRenderedButtons(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.showHidden, m.showIgnored = false, true

	for _, w := range []int{120, 80, 60, 40, 20} {
		m.width = w
		// Zones are column offsets and the truncated root carries a
		// multi-byte "…", so this indexes runes, not bytes.
		line := []rune(plainText(m.renderHeader()))
		for _, z := range []struct {
			name string
			zone [2]int
			want string
		}{
			{"hidden", m.zoneHidden, "[ ] hidden"},
			{"ignored", m.zoneIgnored, "[x] ignored"},
		} {
			if z.zone[0] < 0 || z.zone[1] > len(line) {
				t.Errorf("w=%d: %s zone %v falls outside the %d-column line %q",
					w, z.name, z.zone, len(line), string(line))
				continue
			}
			if got := string(line[z.zone[0]:z.zone[1]]); got != z.want {
				t.Errorf("w=%d: %s zone covers %q, want %q (line %q)",
					w, z.name, got, z.want, string(line))
			}
		}
	}
}

// The Dir line names the directory F confined the search to, and a deep path
// is truncated from the left so the part that identifies it survives.
func TestFinderScopeLine(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.width = 30
	m.scopeDir = "internal/app/very/deeply/nested"

	got := plainText(m.renderFinderScope())

	if !strings.Contains(got, "Dir") {
		t.Errorf("Dir line = %q, want it labelled", got)
	}
	if !strings.Contains(got, "nested") {
		t.Errorf("Dir line = %q, want the tail of the path to survive truncation", got)
	}
	if w := len([]rune(got)); w > m.width {
		t.Errorf("Dir line is %d columns wide, want at most %d: %q", w, m.width, got)
	}
}
