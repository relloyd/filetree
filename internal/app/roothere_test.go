package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// rootModel is a project with a nested subtree to dive into:
//
//	project/sub/a/b/deep.txt
//	project/other/x.txt
func rootModel(t *testing.T) (m *Model, project string) {
	t.Helper()
	project = t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "sub", "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"sub/a/b/deep.txt", "other/x.txt", "top.txt"} {
		if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(f)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m = rootedModel(t, project)
	m.mode = modeNormal
	m.buildBindings()
	return m, project
}

// expandRels opens directories by root-relative path, the way a saved state or
// a few presses of "l" would have.
func expandRels(m *Model, rels ...string) {
	for _, rel := range rels {
		m.tr.ExpandRel(rel)
	}
	m.reflatten()
}

// selectPathOrFail puts the cursor on a path, expanding to reach it.
func selectPathOrFail(t *testing.T, m *Model, path string) {
	t.Helper()
	if rel := m.tr.Rel(filepath.Dir(path)); rel != "." {
		expandRels(m, rel)
	}
	for i, r := range m.rows {
		if r.Node.Path == path {
			m.cursor = i
			return
		}
	}
	t.Fatalf("%q is not a visible row", path)
}

func pressRune(t *testing.T, m *Model, r rune) tea.Cmd {
	t.Helper()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	return cmd
}

// The gesture itself: ">" on a directory makes it the root.
func TestRootHereRootsAtTheSelectedDir(t *testing.T) {
	m, project := rootModel(t)
	sub := filepath.Join(project, "sub")
	selectPathOrFail(t, m, sub)

	pressRune(t, m, '>')
	atRoot(t, m, sub, "after >")
	if m.homeRoot != project {
		t.Errorf("homeRoot = %q, want the project to return to", m.homeRoot)
	}
}

// On a file there is no subtree to descend into, so it takes the file's
// directory — the same rule "F" uses for scoping the finder.
func TestRootHereOnAFileTakesItsParent(t *testing.T) {
	m, project := rootModel(t)
	selectPathOrFail(t, m, filepath.Join(project, "other", "x.txt"))

	pressRune(t, m, '>')
	atRoot(t, m, filepath.Join(project, "other"), "after > on a file")
}

// At the top there is nothing to root into. Saying so beats a dead key.
func TestRootHereAtTheRootSaysSo(t *testing.T) {
	m, project := rootModel(t)
	m.cursor = 0 // the root row

	pressRune(t, m, '>')
	atRoot(t, m, project, "after > at the root")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want nothing to return to", m.homeRoot)
	}
	if m.statusMsg == "" || m.statusErr {
		t.Errorf("status = %q (err=%v), want a plain note", m.statusMsg, m.statusErr)
	}
}

// The tree must not collapse under the cursor: what was open stays open,
// renamed to the new root. Expansion is stored root-relative, so "sub/a/b"
// becomes "a/b" — the rename RebaseRels does.
func TestRootHereCarriesTheOpenDirsIn(t *testing.T) {
	m, project := rootModel(t)
	sub := filepath.Join(project, "sub")
	expandRels(m, "sub/a/b")
	if got := m.tr.ExpandedRels(); !slices.Contains(got, "sub/a/b") {
		t.Fatalf("fixture did not expand sub/a/b: %q", got)
	}
	selectPathOrFail(t, m, sub)

	pressRune(t, m, '>')
	atRoot(t, m, sub, "after >")
	if got := m.tr.ExpandedRels(); !slices.Contains(got, "a/b") {
		t.Errorf("expanded = %q, want the open dirs carried in as a/b", got)
	}
	// And nothing from outside the new root came with them.
	for _, rel := range m.tr.ExpandedRels() {
		if rel == "other" {
			t.Error("a sibling of the new root was carried in")
		}
	}
}

// The seed is a first impression, not an override: a subtree you have been in
// before opens the way you left it.
func TestASecondVisitUsesTheSubtreesOwnMemory(t *testing.T) {
	m, project := rootModel(t)
	sub := filepath.Join(project, "sub")

	// Visit once, leave "a" open and "a/b" shut, and come back out — which
	// saves that arrangement against the subtree's own root.
	selectPathOrFail(t, m, sub)
	pressRune(t, m, '>')
	atRoot(t, m, sub, "first visit")
	expandRels(m, "a")
	m.escKey()

	// Now open sub/a/b up here and dive in again. The subtree remembers "a"
	// alone, and that memory must beat what happens to be open in the project.
	expandRels(m, "sub/a/b")
	selectPathOrFail(t, m, sub)
	pressRune(t, m, '>')
	atRoot(t, m, sub, "second visit")
	got := m.tr.ExpandedRels()
	if !slices.Contains(got, "a") {
		t.Errorf("expanded = %q, want the subtree's remembered a", got)
	}
	if slices.Contains(got, "a/b") {
		t.Errorf("expanded = %q, want a/b left shut — the seed should not apply", got)
	}
}

// Esc goes home, and the project comes back exactly as it was — no reverse
// rename needed, because each root's expansion lives in its own state file.
func TestEscReturnsToTheProjectWithItsExpansionIntact(t *testing.T) {
	m, project := rootModel(t)
	expandRels(m, "other", "sub/a")
	before := m.tr.ExpandedRels()
	selectPathOrFail(t, m, filepath.Join(project, "sub"))

	pressRune(t, m, '>')
	m.escKey()
	atRoot(t, m, project, "after esc")
	if got := m.tr.ExpandedRels(); !slices.Equal(got, before) {
		t.Errorf("expanded = %q, want the project restored to %q", got, before)
	}
}

// Rooting deeper replaces the root rather than stacking it, so one Esc always
// goes home however far in you went. This is enterView's remember-once rule.
func TestRootingDeeperStillNeedsOnlyOneEsc(t *testing.T) {
	m, project := rootModel(t)
	sub := filepath.Join(project, "sub")
	selectPathOrFail(t, m, sub)
	pressRune(t, m, '>')
	atRoot(t, m, sub, "first >")

	selectPathOrFail(t, m, filepath.Join(sub, "a"))
	pressRune(t, m, '>')
	atRoot(t, m, filepath.Join(sub, "a"), "second >")
	if m.homeRoot != project {
		t.Fatalf("homeRoot = %q, want it still pointing at the project", m.homeRoot)
	}

	m.escKey()
	atRoot(t, m, project, "one esc from two levels in")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want nothing left to return to", m.homeRoot)
	}
}

// A root that cannot be loaded must leave nothing half-done: the view stays put
// and Esc must not become a switch to nowhere. Same contract as
// TestAFailedViewSwitchLeavesNothingToReturnTo, reached through ">".
func TestAFailedRootHereLeavesNothingToReturnTo(t *testing.T) {
	m, project := rootModel(t)
	other := filepath.Join(project, "other")
	selectPathOrFail(t, m, other)
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}

	pressRune(t, m, '>')
	atRoot(t, m, project, "after > onto a deleted dir")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want nothing to return to", m.homeRoot)
	}
}

// shift+enter is bound alongside ">" for terminals that report modified keys.
// It is claimed as navigation, so a config cannot take it away.
func TestShiftEnterRootsHereToo(t *testing.T) {
	m, project := rootModel(t)
	sub := filepath.Join(project, "sub")
	selectPathOrFail(t, m, sub)

	msg := tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	if got := msg.String(); got != "shift+enter" {
		t.Fatalf("the chord stringifies as %q, not the key this binds", got)
	}
	if _, bound := m.bindings["shift+enter"]; !bound {
		t.Fatal("shift+enter is not bound")
	}
	m.handleKey(msg)
	atRoot(t, m, sub, "after shift+enter")

	// Plain enter keeps its own meaning, so a chord that never arrives expands
	// the directory rather than being mistaken for this.
	m2, project2 := rootModel(t)
	sub2 := filepath.Join(project2, "sub")
	selectPathOrFail(t, m2, sub2)
	m2.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	atRoot(t, m2, project2, "after plain enter")
}

// The header is the only thing saying Esc goes somewhere, since a re-rooted
// tree is otherwise indistinguishable from having started ft in that directory.
func TestTheHeaderShowsARootedSubtree(t *testing.T) {
	m, project := rootModel(t)
	m.width = 100
	if got := m.renderHeader(); strings.Contains(got, "subtree") {
		t.Fatalf("the project root should carry no chip:\n%s", got)
	}
	selectPathOrFail(t, m, filepath.Join(project, "sub"))
	pressRune(t, m, '>')
	if got := m.renderHeader(); !strings.Contains(got, "subtree") {
		t.Errorf("header does not say we are in a subtree:\n%s", got)
	}
}
