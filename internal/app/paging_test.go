package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// pagingTreeModel is the tree view over enough files to page through.
func pagingTreeModel(t *testing.T) *Model {
	t.Helper()
	dir := t.TempDir()
	for i := range 80 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := rootedModel(t, dir)
	m.mode = modeNormal
	m.width, m.height = 80, 24
	m.buildBindings()
	return m
}

// The tree pages the way the finder does: pgdn and Option+↓ a screenful less
// one row, ctrl+d half of one, cmd+arrows to the ends.
func TestTreePagingKeys(t *testing.T) {
	m := pagingTreeModel(t)
	page, half := fullPage(m.treeVisibleRows()), halfPage(m.treeVisibleRows())
	if page == half {
		t.Fatalf("a page of %d rows cannot tell full from half", m.treeVisibleRows())
	}

	for _, tc := range []struct {
		key  tea.KeyPressMsg
		want int
	}{
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, page},
		{tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt}, page},
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, half},
	} {
		m.cursor = 0
		m.handleKey(tc.key)
		if m.cursor != tc.want {
			t.Errorf("%s: cursor = %d, want %d", tc.key.String(), m.cursor, tc.want)
		}
	}

	m.cursor = 0
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModSuper})
	if want := len(m.rows) - 1; m.cursor != want {
		t.Errorf("super+down: cursor = %d, want the last row %d", m.cursor, want)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	if want := len(m.rows) - 1 - page; m.cursor != want {
		t.Errorf("alt+up: cursor = %d, want %d", m.cursor, want)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModSuper})
	if m.cursor != 0 {
		t.Errorf("super+up: cursor = %d, want 0", m.cursor)
	}
}

// The help page takes the same keys, and none of them edits its filter.
func TestHelpPagingKeys(t *testing.T) {
	m := starterHelpModel(t)
	m.width, m.height = 60, 22
	m.toggleHelp()
	page := m.helpPage()

	press := func(k tea.KeyPressMsg) { m.handleHelpKey(k, k.String()) }

	press(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	if m.helpScroll != page {
		t.Errorf("alt+down: scroll = %d, want a page (%d)", m.helpScroll, page)
	}
	press(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if want := page - halfPage(page); m.helpScroll != want {
		t.Errorf("ctrl+u: scroll = %d, want %d", m.helpScroll, want)
	}
	press(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModSuper})
	rows := m.helpVisibleRows()
	if want := len(rows) - m.helpCapacity(rows); m.helpScroll != want {
		t.Errorf("super+down: scroll = %d, want the last screenful at %d", m.helpScroll, want)
	}
	press(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModSuper})
	if m.helpScroll != 0 {
		t.Errorf("super+up: scroll = %d, want 0", m.helpScroll)
	}
	if got := m.helpInput.Value(); got != "" {
		t.Errorf("a paging key edited the filter: %q", got)
	}
}
