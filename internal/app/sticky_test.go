package app

import (
	tea "charm.land/bubbletea/v2"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relloyd/filetree/internal/tree"
)

// stickyModel builds a tree nested `depth` directories deep with `files` files
// in each of them, every directory expanded, and the sticky parents on. The
// shape is what the feature exists for: enough rows that the parents scroll
// off, and enough depth to exercise the cap.
func stickyModel(t *testing.T, depth, files, height int) *Model {
	t.Helper()
	root := t.TempDir()
	dir := root
	for d := range depth {
		dir = filepath.Join(dir, "d"+string(rune('a'+d)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range files {
			name := filepath.Join(dir, "f"+string(rune('a'+d))+string(rune('0'+f))+".go")
			if err := os.WriteFile(name, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := rootedModel(t, root)
	// rootedModel comes from the finder fixture, which starts in modeFuzzy;
	// the tree and its mouse handling are normal-mode only.
	m.mode = modeNormal
	m.cfg.General.StickyParents = true
	m.height, m.width = height, 80

	// Expand everything so the tree is as deep on screen as it is on disk.
	for range depth {
		for _, r := range m.rows {
			if r.Node.IsDir && !r.Node.Expanded {
				if err := m.tr.Expand(r.Node); err != nil {
					t.Fatal(err)
				}
			}
		}
		m.reflatten()
	}
	return m
}

// bodyLines is what View() actually puts between the header and the status bar.
func bodyLines(m *Model) []string { return strings.Split(m.renderTree(), "\n") }

// The frame invariant: header + body + status == m.height. The pinned block is
// paid for out of the body, so stickyLines and treeVisibleRows must add back up
// to treeHeight exactly — a max(1, …) anywhere in that arithmetic would push
// the frame a row over and cost the status bar.
func TestStickyLinesFitTheBody(t *testing.T) {
	m := stickyModel(t, 12, 2, 40)
	for h := range 49 {
		m.height = h
		for _, scroll := range []int{0, 1, 5, 13, len(m.rows) - 1} {
			if scroll < 0 || scroll >= len(m.rows) {
				continue
			}
			m.scroll = scroll
			s, v := m.stickyLines(), m.treeVisibleRows()
			if s+v != m.treeHeight() {
				t.Errorf("h=%d scroll=%d: sticky %d + visible %d != treeHeight %d",
					h, scroll, s, v, m.treeHeight())
			}
			if v < 1 {
				t.Errorf("h=%d scroll=%d: treeVisibleRows = %d, want at least 1", h, scroll, v)
			}
			if s > m.stickyCap() {
				t.Errorf("h=%d scroll=%d: %d pinned lines exceeds the cap %d",
					h, scroll, s, m.stickyCap())
			}
		}
	}
}

// The renderer's contract with the layout, the same one renderFinderHeader
// keeps with finderHeaderLines: renderTree reserves stickyLines() rows for the
// block, so a block of any other length costs the bottom tree row.
func TestRenderStickyRowsMatchesStickyLines(t *testing.T) {
	m := stickyModel(t, 10, 3, 24)
	for _, h := range []int{3, 6, 12, 24, 48} {
		m.height = h
		for scroll := range len(m.rows) {
			m.scroll = scroll
			if got := len(m.renderStickyRows()); got != m.stickyLines() {
				t.Fatalf("h=%d scroll=%d: rendered %d lines, stickyLines says %d",
					h, scroll, got, m.stickyLines())
			}
		}
	}
}

// The body is a fixed budget whatever else is true, on and off. There was no
// test for this before the pinned block existed, and it is what View() depends
// on to keep the status bar on screen.
func TestTreeBodyIsAlwaysTreeHeightLines(t *testing.T) {
	m := stickyModel(t, 10, 3, 24)
	for _, on := range []bool{true, false} {
		m.cfg.General.StickyParents = on
		for h := 1; h <= 40; h++ {
			m.height = h
			for _, scroll := range []int{0, 3, 17, len(m.rows) - 1, len(m.rows)} {
				m.scroll = max(0, min(scroll, len(m.rows)-1))
				if got := len(bodyLines(m)); got != m.treeHeight() {
					t.Fatalf("sticky=%v h=%d scroll=%d: body is %d lines, want %d",
						on, h, m.scroll, got, m.treeHeight())
				}
			}
		}
	}
}

// The block is anchored on the scroll offset, not on the cursor. That is the
// whole design decision: the wheel moves the offset without moving the cursor,
// and pins that followed the cursor would then label rows with a directory
// none of them are in.
func TestStickyRowsAreTheTopRowsAncestors(t *testing.T) {
	m := stickyModel(t, 6, 2, 30)
	m.scroll = len(m.rows) - 1
	want := tree.Ancestors(m.rows[m.scroll])
	got := m.stickyRows()
	if len(got) != len(want) {
		t.Fatalf("pinned %d rows, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Node != want[i].Node {
			t.Errorf("pinned[%d] = %s, want %s", i, got[i].Node.Name, want[i].Node.Name)
		}
		if i > 0 && got[i].Depth <= got[i-1].Depth {
			t.Errorf("pinned rows are not root-first: %v", got)
		}
	}
	// Moving the cursor alone must change nothing.
	before := m.renderStickyRows()
	m.cursor = 0
	if after := m.renderStickyRows(); !equalLines(before, after) {
		t.Error("the pinned block followed the cursor; it must follow the scroll offset")
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The classic sticky-scroll bug is the cursor ending up underneath the pinned
// block. Reserving rather than overlaying is supposed to make that impossible,
// but only if ensureVisible measures against the content window rather than
// the whole body — so this checks the rendered frame, not just the arithmetic.
func TestCursorIsNeverHiddenByStickyParents(t *testing.T) {
	m := stickyModel(t, 8, 3, 12)
	for _, h := range []int{5, 9, 12, 40} {
		m.height = h
		for c := range len(m.rows) {
			m.cursor = c
			m.ensureVisible()
			if m.cursor < m.scroll || m.cursor >= m.scroll+m.treeVisibleRows() {
				t.Fatalf("h=%d cursor=%d: outside the window [%d,%d)",
					h, c, m.scroll, m.scroll+m.treeVisibleRows())
			}
			// The proof that actually matters: the row is on the screen, on the
			// line the layout says it is. This is what catches an off-by-one
			// between the layout and the renderer.
			lines := bodyLines(m)
			at := m.stickyLines() + (m.cursor - m.scroll)
			if at >= len(lines) {
				t.Fatalf("h=%d cursor=%d: line %d is past the %d-line body", h, c, at, len(lines))
			}
			name := m.rows[m.cursor].Node.Name
			if !strings.HasSuffix(strings.TrimRight(plainText(lines[at]), " "), name) {
				t.Fatalf("h=%d cursor=%d: body line %d is %q, want it to end in %q",
					h, c, at, plainText(lines[at]), name)
			}
		}
	}
}

// The window height and the scroll offset decide each other, so both clamps
// iterate. Idempotence is the fixed point they are iterating towards, and it is
// what stops "G" on a deep tree from oscillating between two offsets.
func TestScrollSettles(t *testing.T) {
	m := stickyModel(t, 10, 3, 14)
	for _, h := range []int{5, 9, 14, 40} {
		m.height = h
		for c := range len(m.rows) {
			m.cursor = c
			m.ensureVisible()
			s := m.scroll
			m.ensureVisible()
			if m.scroll != s {
				t.Fatalf("h=%d cursor=%d: ensureVisible moved the offset %d -> %d on a second pass",
					h, c, s, m.scroll)
			}
			m.clampScroll()
			if m.scroll != s {
				t.Fatalf("h=%d cursor=%d: clampScroll fought ensureVisible, %d -> %d",
					h, c, s, m.scroll)
			}
		}
	}
}

// Scrolling past the end must stop at a full screen of rows, not leave a tail
// of blank lines where the tree should be.
func TestClampScrollLeavesNoBlankTail(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.scroll = 10_000
	m.clampScroll()
	if m.scroll+m.treeVisibleRows() > len(m.rows) {
		t.Errorf("scroll %d + %d visible runs past the %d rows",
			m.scroll, m.treeVisibleRows(), len(m.rows))
	}
	lines := bodyLines(m)
	if last := strings.TrimSpace(plainText(lines[len(lines)-1])); last == "" {
		t.Errorf("the last body line is blank after clamping to the end: %q", last)
	}
}

// With the flag off the tree must render exactly as it did before the feature
// existed: the old loop, byte for byte, styling included.
func TestStickyParentsOffKeepsTheTreeUnchanged(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.cursor, m.scroll = 30, 25
	m.cfg.General.StickyParents = false

	if m.stickyLines() != 0 {
		t.Fatalf("stickyLines = %d, want 0", m.stickyLines())
	}
	if m.treeVisibleRows() != m.treeHeight() {
		t.Fatalf("treeVisibleRows = %d, want the whole body %d",
			m.treeVisibleRows(), m.treeHeight())
	}
	// Reconstruct the pre-feature loop and compare raw, ANSI and all.
	h := m.treeHeight()
	var want []string
	for i := m.scroll; i < min(len(m.rows), m.scroll+h); i++ {
		style := rowNormal
		if i == m.cursor {
			style = rowSelected
		}
		want = append(want, m.renderRow(m.rows[i], style))
	}
	for len(want) < h {
		want = append(want, "")
	}
	if got := m.renderTree(); got != strings.Join(want, "\n") {
		t.Error("the tree does not render the way it did before the feature")
	}
}

// The layout helpers run from Update on every keystroke, and several fixtures
// — paste_test.go's bare &Model{} among them — carry no config at all. They
// must read as "off" rather than dereference it. (renderRow has always needed
// a config, for the icon setting, so rendering is not in scope here.)
func TestStickyParentsWithoutAConfigIsOff(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.scroll = 25
	m.cfg = nil
	if got := m.stickyRows(); got != nil {
		t.Errorf("stickyRows = %v, want nil with no config", got)
	}
	if m.stickyLines() != 0 || m.treeVisibleRows() != m.treeHeight() {
		t.Errorf("sticky %d / visible %d, want 0 / %d",
			m.stickyLines(), m.treeVisibleRows(), m.treeHeight())
	}
	m.cursor = 40
	m.ensureVisible() // must not panic, and must still settle
	m.clampScroll()
}

// The click mapping is the inverse of the layout, so it is tested against the
// render rather than against a second copy of the arithmetic: for every body
// line, the row rowAtY names must be the row drawn on it.
func TestClickMapsToTheRenderedRow(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	for _, scroll := range []int{0, 4, 17, 30, len(m.rows) - 1} {
		m.scroll = max(0, min(scroll, len(m.rows)-1))
		m.clampScroll()
		lines := bodyLines(m)
		for y := 1; y <= m.treeHeight(); y++ {
			idx, ok := m.rowAtY(y)
			line := strings.TrimRight(plainText(lines[y-1]), " ")
			if !ok {
				if line != "" {
					t.Errorf("scroll=%d y=%d: no row mapped, but the line reads %q", m.scroll, y, line)
				}
				continue
			}
			if name := m.rows[idx].Node.Name; !strings.HasSuffix(line, name) {
				t.Errorf("scroll=%d y=%d: mapped to %q, but the line reads %q", m.scroll, y, name, line)
			}
		}
	}
	// Outside the body maps to nothing: Y 0 is the header, and past treeHeight
	// is the status bar.
	if _, ok := m.rowAtY(0); ok {
		t.Error("the header line mapped to a row")
	}
	if _, ok := m.rowAtY(m.treeHeight() + 1); ok {
		t.Error("the status bar mapped to a row")
	}
}

// A pinned parent is a real row, above the offset, so clicking it selects that
// directory and scrolls to it — the same thing clicking any other row does.
// Without the mapping it would silently select whatever row happened to be at
// that offset instead.
func TestClickOnAStickyRowSelectsThatDirectory(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.scroll = len(m.rows) - 1
	m.clampScroll()
	pinned := m.stickyRows()
	if len(pinned) == 0 {
		t.Fatal("no parents pinned; the fixture is not deep enough")
	}
	want := pinned[0].Node

	// X past the chevron, so this is a plain select rather than a toggle.
	if _, cmd := m.handleClick(tea.Mouse{Button: tea.MouseLeft, X: 60, Y: 1}); cmd != nil {
		t.Errorf("a select should issue no command, got %v", cmd)
	}
	if got := m.selected(); got != want {
		t.Fatalf("selected %v, want the pinned %s", got, want.Name)
	}
	if m.cursor < m.scroll || m.cursor >= m.scroll+m.treeVisibleRows() {
		t.Errorf("the tree did not scroll to the clicked parent: cursor %d, window [%d,%d)",
			m.cursor, m.scroll, m.scroll+m.treeVisibleRows())
	}
}

// Clicking a pinned parent's chevron collapses it, which is the useful thing
// to be able to do with a directory you can see but have scrolled away from.
func TestClickOnAStickyChevronCollapses(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.scroll = len(m.rows) - 1
	m.clampScroll()
	pinned := m.stickyRows()
	if len(pinned) == 0 {
		t.Fatal("no parents pinned; the fixture is not deep enough")
	}
	target := pinned[0]
	before := len(m.rows)

	m.handleClick(tea.Mouse{Button: tea.MouseLeft, X: target.Depth * 2, Y: 1})
	if target.Node.Expanded {
		t.Errorf("%s is still expanded after a chevron click", target.Node.Name)
	}
	if len(m.rows) >= before {
		t.Errorf("rows went %d -> %d; collapsing should have removed some", before, len(m.rows))
	}
}

// A chain longer than the cap keeps the deepest parents — they name the
// immediate containers — and says so, or a block starting four columns in with
// no depth-1 line above it reads as a rendering fault rather than a truncation.
func TestDeepChainKeepsTheNearestParentsAndSaysSo(t *testing.T) {
	m := stickyModel(t, 12, 1, 14) // treeHeight 12 -> cap 4
	m.scroll = len(m.rows) - 1
	m.clampScroll()

	rows := m.stickyRows()
	if len(rows) != m.stickyCap() {
		t.Fatalf("pinned %d rows, want the cap %d", len(rows), m.stickyCap())
	}
	full := tree.Ancestors(m.rows[m.scroll])
	for i := range rows {
		if want := full[len(full)-len(rows)+i]; rows[i].Node != want.Node {
			t.Errorf("pinned[%d] = %s, want the deeper %s", i, rows[i].Node.Name, want.Node.Name)
		}
	}

	lines := m.renderStickyRows()
	if got := plainText(lines[0]); !strings.HasPrefix(got, "…") {
		t.Errorf("the first pinned line is %q, want it to open with … for the dropped parents", got)
	}
	for i, l := range lines[1:] {
		if strings.HasPrefix(plainText(l), "…") {
			t.Errorf("pinned line %d also carries the elision marker: %q", i+1, plainText(l))
		}
	}
}

// The wheel moves the offset without moving the cursor, so the cursor can end
// up on a pinned parent. It is on screen there, and must be drawn as the
// selection rather than as an ordinary pinned line.
func TestCursorOnAPinnedParentIsStillTheSelection(t *testing.T) {
	m := stickyModel(t, 8, 3, 20)
	m.cursor = 2 // a directory near the top, an ancestor of everything below
	m.scroll = len(m.rows) - 1
	m.clampScroll()

	pinned := m.stickyRows()
	var at = -1
	for i, r := range pinned {
		if r.Node == m.rows[m.cursor].Node {
			at = i
		}
	}
	if at < 0 {
		t.Skip("the cursor's node is not pinned at this offset")
	}
	lines := m.renderStickyRows()
	if !strings.Contains(lines[at], "48;2;38;79;120") {
		t.Errorf("the pinned cursor row is not drawn as the selection: %q", lines[at])
	}
}
