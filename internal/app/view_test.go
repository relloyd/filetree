package app

import (
	"os"
	"path"
	"path/filepath"
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

// The status bar has to divide a fixed width between the selection path and the
// branch. The branch takes only what is left once the path has its floor, so it
// runs full-length on a wide terminal, shortens through the middle of the range,
// and drops out at the narrow end — where the old code instead lost the branch,
// the mark count and the row counter all at once, because an untruncated path
// pushed the gap below one column.
func TestStatusBarSharesWidthBetweenPathAndBranch(t *testing.T) {
	const (
		rel    = "internal/app/rendering/statusbar/view.go" // 40 columns
		branch = "feat/add-finder-constraints-toggle"       // 34 columns
	)
	root := t.TempDir()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	m := rootedModel(t, root)
	m.mode = modeNormal
	m.tr.ExpandRel(path.Dir(rel))
	m.reflatten()
	m.selectPath(abs)
	// Seeding the repo cache stands in for a real .git, so the path and branch
	// under test are fixed strings rather than whatever the temp dir is called.
	m.repoRoots[filepath.Dir(abs)] = root
	m.branches[root] = branch

	if got := m.gitRelPath(m.selected()); got != rel {
		t.Fatalf("selection path = %q, want %q", got, rel)
	}

	sawFull, sawCut, sawNone := false, false, false
	for _, w := range []int{120, 100, 80, 64, 48, 40, 30} {
		m.width = w
		line := plainText(m.renderStatus())

		if got := len([]rune(line)); got > w {
			t.Errorf("w=%d: status bar is %d columns wide: %q", w, got, line)
		}
		// The counter is the last thing that may be dropped, and nothing here
		// is narrow enough to reach that point.
		if !strings.Contains(line, "/6 ") {
			t.Errorf("w=%d: row counter missing: %q", w, line)
		}

		switch {
		case strings.Contains(line, branch):
			sawFull = true
		case strings.Contains(line, "⎇ "):
			sawCut = true
			if !strings.HasPrefix(strings.TrimPrefix(line[strings.Index(line, "⎇ "):], "⎇ "), branch[:minBranchWidth]) {
				t.Errorf("w=%d: truncated branch lost its head: %q", w, line)
			}
		default:
			sawNone = true
			continue
		}
		// Whenever the branch is on screen the path must still have its floor,
		// which is the whole point of the branch yielding first.
		shown := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, " "), "  ", 2)[0])
		if len([]rune(shown)) < minPathWidth {
			t.Errorf("w=%d: path squeezed to %d columns (%q), want at least %d: %q",
				w, len([]rune(shown)), shown, minPathWidth, line)
		}
	}
	if !sawFull || !sawCut || !sawNone {
		t.Errorf("swept widths never produced all three branch states: full=%v truncated=%v absent=%v",
			sawFull, sawCut, sawNone)
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
