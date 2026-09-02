package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/search"
)

// The bug this layout exists for: at sidebar width every result row used to
// compute the space left for its second column, find none, and silently drop
// it — so a content search rendered as a list of bare paths, without the one
// thing you ran it to see.
func TestNarrowGrepRowKeepsTheMatchedText(t *testing.T) {
	m := finderModel()
	m.width = 34
	m.grepInput.SetValue("vpc")
	hit := search.Hit{Path: "internal/app/fuzzy.go", Line: 596, Text: "dependency \"vpc\""}

	row := m.renderGrepRow(hit, true)
	if len(row) != 2 {
		t.Fatalf("a narrow grep row is %d lines, want 2: %q", len(row), rowText(row))
	}
	got := rowText(row)
	if !strings.Contains(got, "vpc") {
		t.Errorf("row = %q, want the matched text", got)
	}
	// The location is what enter acts on, so it is never the half that goes.
	if !strings.Contains(got, ":596") {
		t.Errorf("row = %q, want the line number", got)
	}
	if !strings.Contains(got, "fuzzy.go") {
		t.Errorf("row = %q, want the basename", got)
	}
	if w := rowWidth(row); w > m.width {
		t.Errorf("row is %d cells wide, want <= %d: %q", w, m.width, got)
	}
}

// Wide enough and the two columns share a line, as they always have.
func TestWideGrepRowStaysOnOneLine(t *testing.T) {
	m := finderModel()
	m.width = 120
	row := m.renderGrepRow(search.Hit{Path: "a.go", Line: 1, Text: "package a"}, false)
	if len(row) != 1 {
		t.Fatalf("a wide grep row is %d lines, want 1: %q", len(row), rowText(row))
	}
	if got := rowText(row); !strings.Contains(got, "package a") {
		t.Errorf("row = %q, want the matched text on the line", got)
	}
}

// A long path loses its head, not its basename, and says so with an ellipsis
// rather than being cut off mid-path with nothing to mark it.
func TestLongPathsTruncateFromTheLeft(t *testing.T) {
	m := finderModel()
	m.width = 40
	m.grepInput.SetValue("x") // renderGrepRow is only reached while grepping
	row := m.renderGrepRow(search.Hit{
		Path: "internal/app/deeply/nested/directory/tree/target.go",
		Line: 7,
		Text: "x := 1",
	}, false)
	got := rowText(row)
	if !strings.Contains(got, "target.go:7") {
		t.Errorf("row = %q, want the basename and line kept", got)
	}
	if !strings.Contains(got, ellipsis) {
		t.Errorf("row = %q, want an ellipsis marking the dropped head", got)
	}
	if w := rowWidth(row); w > m.width {
		t.Errorf("row is %d cells wide, want <= %d: %q", w, m.width, got)
	}
}

// The row height and the visible count have to agree, or the list scrolls by
// one amount and draws by another.
func TestFinderRowHeightMatchesTheVisibleCount(t *testing.T) {
	m := finderModel()
	m.height = 22 // treeHeight 20, one header line, so 19 lines for results

	m.width = 120
	if h := m.finderRowHeight(); h != 1 {
		t.Errorf("a wide finder has row height %d, want 1", h)
	}
	if got, want := m.fuzzyVisibleRows(), 19; got != want {
		t.Errorf("wide fuzzyVisibleRows = %d, want %d", got, want)
	}

	// Narrow, but a source with nothing worth stacking: still one line each.
	m.width = 34
	if h := m.finderRowHeight(); h != 1 {
		t.Errorf("a narrow name search has row height %d, want 1", h)
	}

	m.grepInput.SetValue("vpc")
	if h := m.finderRowHeight(); h != 2 {
		t.Errorf("a narrow content search has row height %d, want 2", h)
	}
	if got, want := m.fuzzyVisibleRows(), 19/2; got != want {
		t.Errorf("narrow fuzzyVisibleRows = %d, want %d", got, want)
	}
}

// The tree finder is the common case and carries no second column, so a narrow
// pane must not halve its list to hold nothing.
func TestNarrowNameSearchDoesNotStack(t *testing.T) {
	m := finderModel()
	m.width = 34
	row := m.renderMatchRow(fuzzy.Match{Str: "internal/app/view.go"}, false, time.Now())
	if len(row) != 1 {
		t.Fatalf("a narrow name row is %d lines, want 1: %q", len(row), rowText(row))
	}
}

// renderFuzzy owns the frame invariant: header + body + status must come to
// exactly the window height, whatever the rows inside it are doing.
func TestNarrowFinderFillsTheBodyExactly(t *testing.T) {
	m := finderModel()
	m.width = 34
	m.grepInput.SetValue("vpc")
	m.grepHits = hits("a.go", "b.go", "c.go", "d.go", "e.go")
	for i := range m.grepHits {
		m.grepRows = append(m.grepRows, i)
	}

	for _, h := range []int{6, 7, 12, 13, 40} {
		m.height = h
		got := strings.Count(m.renderFuzzy(), "\n") + 1
		if want := m.treeHeight(); got != want {
			t.Errorf("height %d: body is %d lines, want %d", h, got, want)
		}
	}
}

// Every line of every row is clipped to the pane. A row wider than the pane
// wraps in the terminal and pushes the status bar off the bottom.
func TestNarrowRowsNeverExceedThePane(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("vpc")
	long := search.Hit{
		Path: "internal/app/deeply/nested/directory/tree/target.go",
		Line: 1234,
		Text: "a very long matched line that will not fit in a sidebar at all",
	}
	for _, w := range []int{20, 24, 30, 34, 50, 79, 80, 120} {
		m.width = w
		for _, line := range m.renderGrepRow(long, true) {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("width %d: line is %d cells: %q", w, got, plainText(line))
			}
		}
	}
}

func TestTruncatePathLeft(t *testing.T) {
	// "internal/x.go" is 13 bytes. At w=6 the ellipsis costs one cell and the
	// last five runes survive — "/x.go", starting at byte 8 — so a match at
	// byte 9 lands at 9-8 plus the ellipsis's three bytes.
	const p = "internal/x.go"

	for _, tc := range []struct {
		name    string
		s       string
		matched []int
		w       int
		want    string
		wantIdx []int
	}{
		{"fits, untouched", p, []int{0, 9}, 20, p, []int{0, 9}},
		{"exactly fits", p, []int{0}, 13, p, []int{0}},
		{"head dropped, tail rebased", p, []int{0, 9}, 6, "…/x.go", []int{4}},
		{"every match in the dropped head", p, []int{0, 1}, 6, "…/x.go", nil},
		{"no room at all", p, []int{0}, 1, "…", nil},
		{"nil matches stay nil", p, nil, 6, "…/x.go", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, idx := truncatePathLeft(tc.s, tc.matched, tc.w)
			if got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if len(idx) != len(tc.wantIdx) {
				t.Fatalf("indexes = %v, want %v", idx, tc.wantIdx)
			}
			for i := range idx {
				if idx[i] != tc.wantIdx[i] {
					t.Fatalf("indexes = %v, want %v", idx, tc.wantIdx)
				}
			}
			// Whatever survives must still point at the runes it named.
			for _, i := range idx {
				if i < 0 || i >= len(got) {
					t.Errorf("index %d is outside %q", i, got)
				}
			}
		})
	}
}

// Multi-byte names are the case a rune count gets wrong: the shift is in
// bytes, because that is what the match positions are in.
func TestTruncatePathLeftShiftsByBytes(t *testing.T) {
	const s = "über/naïve/target.go" // ü and ï are two bytes each
	got, idx := truncatePathLeft(s, []int{len(s) - 2}, 12)
	if !strings.HasSuffix(got, "target.go") {
		t.Fatalf("text = %q, want the basename kept", got)
	}
	if len(idx) != 1 {
		t.Fatalf("indexes = %v, want one survivor", idx)
	}
	if idx[0] != len(got)-2 {
		t.Errorf("index = %d, want %d — the same distance from the end", idx[0], len(got)-2)
	}
}
