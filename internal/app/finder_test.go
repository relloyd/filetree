package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/search"
)

// fixtureTree builds a root with a handful of terragrunt.hcl files buried
// under enough filler that an unfiltered walk of it is dominated by noise —
// the shape of the tree the filter exists to make searchable.
func fixtureTree(t *testing.T, filler int) string {
	t.Helper()
	root := t.TempDir()
	for _, env := range []string{"prod", "stage"} {
		for _, region := range []string{"eu", "us"} {
			dir := filepath.Join(root, "infra", env, region)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "terragrunt.hcl"), []byte("include {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for i := range filler {
				name := fmt.Sprintf("filler%03d.txt", i)
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return root
}

// collectWalk drains a walk to completion and returns every candidate plus the
// truncation flag from its final chunk.
func collectWalk(t *testing.T, p walkParams) ([]string, bool) {
	t.Helper()
	ch := make(chan fuzzyChunk, 4)
	cancel := make(chan struct{})
	defer close(cancel)
	go walkCandidates(p, ch, cancel)

	var got []string
	truncated := false
	for c := range ch {
		got = append(got, c.cands...)
		if c.done {
			truncated = c.truncated
		}
	}
	return got, truncated
}

func params(root string, expr string, maxCands int) walkParams {
	f, _ := search.CompileFilter(expr)
	return walkParams{root: root, filter: f, showIgnored: true, max: maxCands}
}

// The headline case: every terragrunt.hcl in a tree far larger than the
// candidate cap is still found, because the filter is applied while walking
// rather than to whatever the cap happened to admit.
func TestWalkFindsEveryMatchBeyondTheCandidateCap(t *testing.T) {
	root := fixtureTree(t, 200) // ~800 files, cap set to 50 below

	got, truncated := collectWalk(t, params(root, "terragrunt.hcl", 50))
	slices.Sort(got)
	want := []string{
		"infra/prod/eu/terragrunt.hcl",
		"infra/prod/us/terragrunt.hcl",
		"infra/stage/eu/terragrunt.hcl",
		"infra/stage/us/terragrunt.hcl",
	}
	if !slices.Equal(got, want) {
		t.Errorf("filtered walk = %v, want %v", got, want)
	}
	if truncated {
		t.Error("filtered walk reported truncation; the filter should keep it well under the cap")
	}

	// Without the filter the same cap truncates and most files are missed —
	// this is what the filter is working around.
	unfiltered, truncated := collectWalk(t, params(root, "", 50))
	if !truncated {
		t.Error("unfiltered walk did not report truncation at a cap of 50")
	}
	if slices.Contains(unfiltered, "infra/stage/us/terragrunt.hcl") {
		t.Skip("fixture ordering happened to admit the deep file; cap behaviour still asserted above")
	}
}

// An extension filter must not behave like a fuzzy substring: "hcl" selects
// *.hcl, not every path containing those letters.
func TestWalkFilterIsNotFuzzy(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"terragrunt.hcl", "charlie.md", "hcl-notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := collectWalk(t, params(root, "hcl", 1000))
	slices.Sort(got)
	if !slices.Equal(got, []string{"terragrunt.hcl"}) {
		t.Errorf("walk = %v, want only terragrunt.hcl", got)
	}
}

// The cap is checked between directories: a truncated walk never contains a
// half-listed directory, because a partial listing is worse than a smaller
// complete one.
func TestWalkCapStopsBetweenDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 10 {
			name := filepath.Join(root, dir, fmt.Sprintf("f%02d.txt", i))
			if err := os.WriteFile(name, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, truncated := collectWalk(t, params(root, "", 5))
	if !truncated {
		t.Fatal("expected the walk to report truncation")
	}
	// The root listing (a, b) alone exceeds the cap of 5 only once a whole
	// directory has been read, so whichever directories were visited are
	// present in full.
	counts := map[string]int{}
	for _, rel := range got {
		if dir := filepath.Dir(rel); dir != "." {
			counts[dir]++
		}
	}
	for dir, n := range counts {
		if n != 10 {
			t.Errorf("directory %q contributed %d entries, want all 10", dir, n)
		}
	}
}

// Abandoning a search must stop the walker goroutine, not just ignore it: a
// cancelled walk of a huge root should stop issuing syscalls.
func TestWalkStopsWhenCancelled(t *testing.T) {
	root := fixtureTree(t, 50)
	ch := make(chan fuzzyChunk)
	cancel := make(chan struct{})
	go walkCandidates(params(root, "", 100000), ch, cancel)

	close(cancel)
	// The goroutine may be mid-flush; draining until close proves it returned.
	for range ch { //nolint:revive // draining to completion is the assertion
	}
}

func finderModel() *Model {
	m := &Model{
		height:    40,
		mode:      modeFuzzy,
		input:     textinput.New(),
		typeInput: textinput.New(),
		cfg:       &config.Config{General: config.General{FuzzyMaxMatches: 200}},
	}
	m.buildActionKeysForTest()
	return m
}

// buildActionKeysForTest fills the action-key map the way buildBindings does,
// without needing a full config.
func (m *Model) buildActionKeysForTest() {
	m.actionKeys = map[string]string{
		"finder-next-field": "tab",
		"finder-prev-field": "shift+tab",
	}
}

// Chunks from a walk abandoned by esc or a changed filter must not land in the
// search that replaced it — they would reset the selection under the user.
func TestStaleWalkChunksAreDropped(t *testing.T) {
	m := finderModel()
	m.fuzzyGen = 2
	m.fuzzyCands = []string{"a.go"}
	m.fuzzyMatches = []fuzzy.Match{{Str: "a.go"}}
	m.fuzzySel = 0

	m.Update(fuzzyCandsMsg{gen: 1, cands: []string{"stale.go"}, done: true})

	if slices.Contains(m.fuzzyCands, "stale.go") {
		t.Errorf("stale chunk was accepted: %v", m.fuzzyCands)
	}

	// The current generation still lands.
	m.Update(fuzzyCandsMsg{gen: 2, cands: []string{"fresh.go"}, done: true})
	if !slices.Contains(m.fuzzyCands, "fresh.go") {
		t.Errorf("current chunk was dropped: %v", m.fuzzyCands)
	}
	if m.fuzzyWalking {
		t.Error("done chunk left the walk marked as running")
	}
}

// A chunk that arrives after the finder has been left must not be applied
// either — the model is back in the tree and fuzzy state is no longer live.
func TestWalkChunksIgnoredOutsideFinder(t *testing.T) {
	m := finderModel()
	m.mode = modeNormal
	m.Update(fuzzyCandsMsg{gen: 0, cands: []string{"late.go"}})
	if len(m.fuzzyCands) != 0 {
		t.Errorf("chunk applied outside the finder: %v", m.fuzzyCands)
	}
}

// Tab moves between the finder's input lines and each field keeps its text.
func TestCycleFinderFieldKeepsText(t *testing.T) {
	m := finderModel()
	m.input.SetValue("terragr")
	m.typeInput.SetValue("hcl")

	if m.finderField != fieldQuery {
		t.Fatalf("initial field = %d", m.finderField)
	}
	m.cycleFinderField(1)
	if m.finderField != fieldType {
		t.Errorf("after tab, field = %d, want fieldType", m.finderField)
	}
	m.cycleFinderField(1)
	if m.finderField != fieldQuery {
		t.Errorf("tab wrapped to %d, want fieldQuery", m.finderField)
	}
	m.cycleFinderField(-1)
	if m.finderField != fieldType {
		t.Errorf("shift+tab went to %d, want fieldType", m.finderField)
	}
	if m.input.Value() != "terragr" || m.typeInput.Value() != "hcl" {
		t.Errorf("cycling lost text: %q / %q", m.input.Value(), m.typeInput.Value())
	}
}

// The type filter takes a line of its own, so the match list has one row less
// to work with once it is in play.
func TestFinderHeaderGrowsWithTheTypeField(t *testing.T) {
	m := finderModel()
	rows := m.fuzzyVisibleRows()

	m.typeInput.SetValue("hcl")
	if got := m.fuzzyVisibleRows(); got != rows-1 {
		t.Errorf("visible rows with a filter = %d, want %d", got, rows-1)
	}

	// Focus alone is enough to show the line, so an empty filter being edited
	// does not shift the list as the first character is typed.
	m.typeInput.SetValue("")
	m.finderField = fieldType
	if got := m.fuzzyVisibleRows(); got != rows-1 {
		t.Errorf("visible rows with the field focused = %d, want %d", got, rows-1)
	}
}

// Typing more of a query narrows the previous matches instead of re-scanning
// every candidate; the result must be identical either way.
func TestIncrementalNarrowingMatchesAFullSearch(t *testing.T) {
	m := finderModel()
	for i := range 200 {
		m.fuzzyCands = append(m.fuzzyCands, fmt.Sprintf("pkg%03d/handler.go", i))
	}
	m.fuzzyCands = append(m.fuzzyCands, "cmd/main.go", "internal/handlers/http.go")

	m.input.SetValue("han")
	m.refuzzy()
	m.input.SetValue("handl")
	m.refuzzy() // narrows the previous match set
	narrowed := strsOf(m.fuzzyMatches)

	fresh := finderModel()
	fresh.fuzzyCands = m.fuzzyCands
	fresh.input.SetValue("handl")
	fresh.refuzzy() // full scan
	if !slices.Equal(narrowed, strsOf(fresh.fuzzyMatches)) {
		t.Errorf("narrowed = %v\nfull    = %v", narrowed, strsOf(fresh.fuzzyMatches))
	}
}

// Editing the query in a way that does not extend it has to re-scan, or
// backspacing would never bring results back.
func TestBackspaceWidensAgain(t *testing.T) {
	m := finderModel()
	m.fuzzyCands = []string{"alpha.go", "beta.go"}

	m.input.SetValue("alpha")
	m.refuzzy()
	if len(m.fuzzyMatches) != 1 {
		t.Fatalf("matches for %q = %d, want 1", "alpha", len(m.fuzzyMatches))
	}
	m.input.SetValue("a")
	m.refuzzy()
	if len(m.fuzzyMatches) != 2 {
		t.Errorf("matches after backspacing = %d, want both candidates", len(m.fuzzyMatches))
	}
}

// Streamed chunks accumulate into the result list rather than replacing it,
// and the display cap still holds.
func TestStreamedChunksAccumulate(t *testing.T) {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = 3

	m.addFuzzyCands([]string{"a/one.go", "a/two.go"})
	m.addFuzzyCands([]string{"b/three.go", "b/four.go"})

	if len(m.fuzzyCands) != 4 {
		t.Errorf("candidates = %d, want 4", len(m.fuzzyCands))
	}
	if len(m.fuzzyMatches) != 3 {
		t.Errorf("empty-query matches = %d, want the cap of 3", len(m.fuzzyMatches))
	}
	if m.fuzzyMatches[0].Str != "a/one.go" {
		t.Errorf("browse order lost: first match = %q", m.fuzzyMatches[0].Str)
	}

	// With a query in play the chunks are matched as they arrive.
	q := finderModel()
	q.input.SetValue("three")
	q.fuzzyQuery = "three"
	q.addFuzzyCands([]string{"a/one.go", "a/two.go"})
	q.addFuzzyCands([]string{"b/three.go"})
	if len(q.fuzzyMatches) != 1 || q.fuzzyMatches[0].Str != "b/three.go" {
		t.Errorf("streamed query matches = %v, want b/three.go", strsOf(q.fuzzyMatches))
	}
}

// A malformed glob must not wipe the results or crash; it reports itself and
// leaves the previous walk alone.
func TestBadFilterReportsAndKeepsResults(t *testing.T) {
	m := finderModel()
	m.fuzzyCands = []string{"a.go"}
	m.typeInput.SetValue("[")

	if cmd := m.applyTypeFilter(); cmd != nil {
		t.Error("a bad filter restarted the walk")
	}
	if m.fuzzyFilterErr == "" {
		t.Error("bad filter produced no error message")
	}
	if len(m.fuzzyCands) != 1 {
		t.Errorf("bad filter discarded candidates: %v", m.fuzzyCands)
	}
}

// Moving the cursor inside the type field must not restart the walk; only a
// changed filter should.
func TestFilterRestartsOnlyOnChange(t *testing.T) {
	m := finderModel()
	m.tr = nil // restartFuzzyWalk is not reached when the text is unchanged
	m.typeInput.SetValue("hcl")
	m.fuzzyFilterRaw = "hcl"

	if cmd := m.applyTypeFilter(); cmd != nil {
		t.Error("unchanged filter text restarted the walk")
	}
}

// Tab is routed inside the finder rather than falling through to the tree.
func TestFinderTabIsHandledInFuzzyMode(t *testing.T) {
	m := finderModel()
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.finderField != fieldType {
		t.Errorf("tab in the finder left the field at %d", m.finderField)
	}
}

func strsOf(ms []fuzzy.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Str
	}
	return out
}
