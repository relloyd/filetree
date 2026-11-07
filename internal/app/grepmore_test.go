package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/search"
)

// grepMoreModel is a content search with the shipped per-file cap.
func grepMoreModel() *Model {
	m := finderModel()
	m.cfg.General.FuzzyGrepMaxPerFile = 5
	m.grepInput.SetValue("x")
	return m
}

func more(path string) search.Hit { return search.Hit{Path: path, More: true} }

// The placeholder is a row — it can be selected, and enter goes to its file —
// but not a match, so the match cap does not count it.
func TestMorePlaceholderIsARowNotAMatch(t *testing.T) {
	m := grepMoreModel()
	m.cfg.General.FuzzyMaxMatches = 3
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 2}, more("a.go"), {Path: "b.go", Line: 1}})

	if m.grepCapped {
		t.Error("three matches against a cap of three reported capped: the placeholder was counted")
	}
	if got := m.finderLen(); got != 4 {
		t.Errorf("rows = %d, want 4 (three matches and the placeholder)", got)
	}
	if got := m.finderPath(2); got != "a.go" {
		t.Errorf("placeholder row's path = %q, want its file", got)
	}
	if m.grepMore != 1 {
		t.Errorf("grepMore = %d, want 1", m.grepMore)
	}

	m.addGrepHits([]search.Hit{{Path: "c.go", Line: 1}})
	if !m.grepCapped {
		t.Error("a fourth match did not cap the list")
	}
}

// Once the list is capped, a placeholder is dropped with the rest: the file it
// speaks for may not have made it in.
func TestMorePlaceholderIsDroppedOnceCapped(t *testing.T) {
	m := grepMoreModel()
	m.cfg.General.FuzzyMaxMatches = 1
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "b.go", Line: 1}, more("b.go")})
	if got := m.finderLen(); got != 1 {
		t.Errorf("rows = %d, want only the one match", got)
	}
}

// Find narrows by path, and the placeholder has its file's path, so it stays
// or goes with that file's matches.
func TestFindNarrowingKeepsThePlaceholderWithItsFile(t *testing.T) {
	m := grepMoreModel()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, more("a.go"), {Path: "b.go", Line: 1}, more("b.go")})
	m.input.SetValue("!a.go")
	m.refuzzy()
	if got := m.finderLen(); got != 2 {
		t.Fatalf("rows = %d, want b.go's match and placeholder", got)
	}
	for i := range 2 {
		if got := m.finderPath(i); got != "b.go" {
			t.Errorf("row %d = %q, want b.go", i, got)
		}
	}
}

// alt+a lifts the per-file cap for the session and searches again, since the
// matches past the cap were never read; pressing it again puts the cap back.
func TestAllMatchesTogglesThePerFileCap(t *testing.T) {
	m := grepMoreModel()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, more("a.go")})

	alt := tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt}
	_, cmd := m.handleKey(alt)
	if !m.grepAllMatches {
		t.Fatal("alt+a did not turn all-matches on")
	}
	if cmd == nil {
		t.Error("toggling returned no command; the search has to run again")
	}
	if got := m.grepQuery().MaxPerFile; got != 0 {
		t.Errorf("MaxPerFile with all matches = %d, want 0 (no cap)", got)
	}
	if len(m.grepHits) != 0 || m.grepMore != 0 {
		t.Errorf("old hits survived the re-search: %v (more %d)", m.grepHits, m.grepMore)
	}
	if got := m.grepInput.Value(); got != "x" {
		t.Errorf("alt+a reached the input: %q", got)
	}

	m.handleKey(alt)
	if m.grepAllMatches || m.grepQuery().MaxPerFile != 5 {
		t.Errorf("second alt+a: all=%v, MaxPerFile=%d; want the cap of 5 back", m.grepAllMatches, m.grepQuery().MaxPerFile)
	}

}

// The setting is the session's, like ctrl+g's factor: reopening keeps it.
func TestAllMatchesSurvivesReopening(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.cfg.General.FuzzyGrepMaxPerFile = 5
	m.toggleAllMatches()
	m.startFuzzy()
	if !m.grepAllMatches {
		t.Error("reopening the finder reset all-matches")
	}
}

// With no cap configured there is nothing to lift, and with no Grep field there
// is no search to lift it from.
func TestAllMatchesDoesNothingWhereItCannot(t *testing.T) {
	m := grepMoreModel()
	m.cfg.General.FuzzyGrepMaxPerFile = 0
	m.toggleAllMatches()
	if m.grepAllMatches {
		t.Error("toggled with no per-file cap configured")
	}

	m = grepMoreModel()
	m.finderSrc = srcBookmark
	m.toggleAllMatches()
	if m.grepAllMatches {
		t.Error("toggled from the bookmark list, which has no Grep field")
	}
}

// The placeholder says which file and nothing else, and in a narrow pane it
// still takes the two lines every stacked row takes.
func TestMorePlaceholderRow(t *testing.T) {
	m := grepMoreModel()
	m.width = 100
	row := m.renderGrepRow(more("internal/app/view.go"), false)
	if len(row) != 1 || !strings.Contains(rowText(row), "… more in internal/app/view.go") {
		t.Errorf("wide row = %q", rowText(row))
	}

	m.width = 34
	row = m.renderGrepRow(more("internal/app/deeply/nested/view.go"), true)
	if got, want := len(row), m.finderRowHeight(); got != want {
		t.Errorf("narrow row is %d lines, want %d", got, want)
	}
	if !strings.Contains(rowText(row), "view.go") {
		t.Errorf("narrow row lost the file name: %q", rowText(row))
	}
}

// The Grep line names the cap and its key while it is holding matches back,
// and says when it has been lifted; otherwise it says nothing.
func TestGrepLineReportsThePerFileCap(t *testing.T) {
	header := func(m *Model) string { return strings.Join(m.renderFinderHeader(), "\n") }

	m := grepMoreModel()
	m.width = 120
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}})
	if h := header(m); strings.Contains(h, "/file") || strings.Contains(h, "all matches") {
		t.Errorf("nothing held back, but the header mentions the cap: %q", h)
	}

	m.addGrepHits([]search.Hit{more("a.go")})
	if h := header(m); !strings.Contains(h, "5/file (alt+a: all)") {
		t.Errorf("header does not offer the toggle: %q", h)
	}

	m.grepAllMatches = true
	if h := header(m); !strings.Contains(h, "all matches") {
		t.Errorf("header does not say the cap is lifted: %q", h)
	}
}

// A command already on alt+a as its finder_key loses it to finder-all-matches,
// and says so, rather than either stopping ft or shadowing the action.
func TestFinderKeyOnAltAYieldsToAllMatches(t *testing.T) {
	m := treeModel(t)
	m.cfg.Commands = map[string]config.Command{
		"mine": {Run: "true", Mode: config.ModeBackground, FinderKey: "alt+a"},
	}
	m.buildBindings()

	if got := m.actionKeys["finder-all-matches"]; got != "alt+a" {
		t.Errorf("finder-all-matches = %q, want it to keep alt+a", got)
	}
	if name, ok := m.finderCmds["alt+a"]; ok {
		t.Errorf("commands.%s still owns alt+a in the finder", name)
	}
	found := false
	for _, c := range m.keyConflicts {
		if c.key == "alt+a" && strings.Contains(c.refused, "mine") {
			found = true
		}
	}
	if !found {
		t.Errorf("no conflict reported for alt+a: %v", m.keyConflicts)
	}
}

// A placeholder's line is the first match it stands for, so the pick a
// command or a resume records for it points into the file where the unseen
// matches start.
func TestSelectedPickOnAPlaceholderCarriesItsLine(t *testing.T) {
	m := grepMoreModel()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 9, More: true}})
	m.fuzzySel = 1
	if got := m.selectedPick(); got != (finderPick{rel: "a.go", line: 9}) {
		t.Errorf("pick = %+v, want a.go at line 9", got)
	}
}

// Moving the cursor in a field is not an edit: a resume still waiting for its
// row has to survive it.
func TestCursorKeysKeepTheResumeTarget(t *testing.T) {
	m := grepMoreModel()
	m.input.SetValue("abc")
	m.resumeWant = finderPick{rel: "a.go", line: 3}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.resumeWant.isZero() {
		t.Fatal("a cursor key gave up on the resume target")
	}
	m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !m.resumeWant.isZero() {
		t.Error("an edit kept the resume target")
	}
}

// alt+a searches again from scratch; the row it was pressed on comes back
// once the new results reach it. Lifting the cap returns to the exact line —
// a placeholder's first hidden match — and restoring it returns to the file.
func TestAllMatchesKeepsTheSelection(t *testing.T) {
	m := grepMoreModel()
	m.cfg.General.FuzzyGrepMaxPerFile = 2
	m.addGrepHits([]search.Hit{
		{Path: "a.go", Line: 1}, {Path: "b.go", Line: 1}, {Path: "b.go", Line: 2}, {Path: "b.go", Line: 3, More: true},
	})
	m.fuzzySel = 3 // "… more in b.go"

	m.toggleAllMatches()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "b.go", Line: 1}, {Path: "b.go", Line: 2}, {Path: "b.go", Line: 3}, {Path: "b.go", Line: 4}})
	if got := m.grepHits[m.grepRows[m.fuzzySel]]; got.Path != "b.go" || got.Line != 3 {
		t.Errorf("after lifting the cap the selection is on %+v, want b.go:3", got)
	}

	m.fuzzySel = 4 // b.go:4, which the cap will hide
	m.toggleAllMatches()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "b.go", Line: 1}, {Path: "b.go", Line: 2}, {Path: "b.go", Line: 3, More: true}})
	if got := m.finderPath(m.fuzzySel); got != "b.go" {
		t.Errorf("after restoring the cap the selection is on %q, want b.go", got)
	}
}

// The counter counts matches, not rows: placeholders would push it past the
// match cap it is read against. On a placeholder it shows the match above.
func TestCounterCountsMatchesNotPlaceholders(t *testing.T) {
	m := grepMoreModel()
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 2}, more("a.go"), {Path: "b.go", Line: 1}})
	for sel, want := range []string{"1/3", "2/3", "2/3", "3/3"} {
		m.fuzzySel = sel
		if got := m.renderFinderCounter(); !strings.Contains(got, want) {
			t.Errorf("sel %d: counter %q, want %s", sel, got, want)
		}
	}
}

// The hint is about the list on screen: none when the Find query has hidden
// every placeholder, and no key named when the action has none.
func TestGrepHintFollowsTheList(t *testing.T) {
	header := func(m *Model) string { return strings.Join(m.renderFinderHeader(), "\n") }
	m := grepMoreModel()
	m.width = 120
	m.addGrepHits([]search.Hit{{Path: "a.go", Line: 1}, more("a.go"), {Path: "b.go", Line: 1}})

	m.input.SetValue("!a.go")
	m.refuzzy()
	if h := header(m); strings.Contains(h, "/file") {
		t.Errorf("no placeholder is listed, but the header still offers alt+a: %q", h)
	}

	m.input.SetValue("")
	m.refuzzy()
	m.actionKeys["finder-all-matches"] = ""
	h := header(m)
	if !strings.Contains(h, "5/file") || strings.Contains(h, "(") {
		t.Errorf("with no key the header should state the cap alone: %q", h)
	}
}
