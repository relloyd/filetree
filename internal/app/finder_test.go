package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/fsops"
	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/search"
	"github.com/relloyd/filetree/internal/state"
	"github.com/relloyd/filetree/internal/tree"
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
	return walkParams{start: root, filter: f, showIgnored: true, max: maxCands}
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
		height:        40,
		mode:          modeFuzzy,
		input:         textinput.New(),
		typeInput:     textinput.New(),
		grepInput:     textinput.New(),
		bmInput:       textinput.New(),
		cfg:           &config.Config{General: config.General{FuzzyMaxMatches: 200}},
		repoRoots:     map[string]string{},
		statuses:      map[string]*gitx.RepoStatus{},
		branches:      map[string]string{},
		statusPending: map[string]bool{},
		marked:        map[string]bool{},
		showIgnored:   true,
	}
	m.buildActionKeysForTest()
	// startFuzzy focuses the query field; textinput drops every key when it is
	// not focused, so a fixture that skips this silently ignores edits.
	m.input.Focus()
	return m
}

// rootedModel is a finder model backed by a real tree, for the tests that
// walk, search, and jump for real.
func rootedModel(t *testing.T, root string) *Model {
	t.Helper()
	m := finderModel()
	m.stateDir = t.TempDir()
	m.st = state.Load(m.stateDir, root)
	m.recent = state.LoadRecent(m.stateDir, root)
	w, err := fsops.NewWatcher(10 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	m.watcher = w
	m.tr = tree.New(root, fsops.ReadDir)
	if err := m.tr.Expand(m.tr.Root); err != nil {
		t.Fatal(err)
	}
	m.reflatten()
	return m
}

// buildActionKeysForTest fills the action-key map the way buildBindings does,
// without needing a full config.
func (m *Model) buildActionKeysForTest() {
	m.actionKeys = map[string]string{
		"finder-next-field": "tab",
		"finder-prev-field": "shift+tab",
		"finder-more":       "ctrl+g",
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

// Two hardcoded orders have to agree: the enum cycleFinderField walks by
// number, and the sequence renderFinderHeader appends its lines in. Nothing
// links them, so changing one alone is a silent bug — tab would jump from the
// first line to the third and back up, and every other test would still pass.
//
// This drives the real cycle and reads the real header, so it fails on either
// half of that change rather than restating one of the two lists.
func TestFinderFieldOrderMatchesHeader(t *testing.T) {
	m := finderModel()
	m.width = 80
	// Both optional fields render only when focused or non-empty; fill them so
	// every line is on screen at once.
	m.typeInput.SetValue("hcl")
	m.grepInput.SetValue("dependency")

	labels := map[finderField]string{fieldQuery: "Find", fieldGrep: "Grep", fieldType: "Type"}

	var tabbed []string
	for range int(finderFieldCount) {
		tabbed = append(tabbed, labels[m.finderField])
		m.cycleFinderField(1)
	}
	if m.finderField != fieldQuery {
		t.Fatalf("a full cycle of %d tabs ended on field %d, not back at Find", finderFieldCount, m.finderField)
	}

	var drawn []string
	for _, line := range m.renderFinderHeader() {
		for _, name := range tabbed {
			if strings.Contains(plainText(line), " "+name+" ") {
				drawn = append(drawn, name)
			}
		}
	}

	if !slices.Equal(tabbed, drawn) {
		t.Errorf("tab visits %v but the header draws %v; the enum in fuzzy.go and\n"+
			"renderFinderHeader in view.go have to list the fields in the same order", tabbed, drawn)
	}
}

// Tab cycles the finder's input lines and each field keeps its text.
func TestCycleFinderFieldKeepsText(t *testing.T) {
	m := finderModel()
	m.input.SetValue("terragr")
	m.typeInput.SetValue("hcl")
	m.grepInput.SetValue("dependency")

	if m.finderField != fieldQuery {
		t.Fatalf("initial field = %d", m.finderField)
	}
	for i, want := range []finderField{fieldGrep, fieldType, fieldQuery} {
		m.cycleFinderField(1)
		if m.finderField != want {
			t.Errorf("tab %d landed on field %d, want %d", i+1, m.finderField, want)
		}
	}
	m.cycleFinderField(-1)
	if m.finderField != fieldType {
		t.Errorf("shift+tab went to %d, want fieldType", m.finderField)
	}
	if m.input.Value() != "terragr" || m.typeInput.Value() != "hcl" || m.grepInput.Value() != "dependency" {
		t.Errorf("cycling lost text: %q / %q / %q",
			m.input.Value(), m.typeInput.Value(), m.grepInput.Value())
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
	if m.finderField != fieldGrep {
		t.Errorf("tab in the finder left the field at %d", m.finderField)
	}
}

// --- content mode ---

func hits(paths ...string) []search.Hit {
	out := make([]search.Hit, len(paths))
	for i, p := range paths {
		out[i] = search.Hit{Path: p, Line: i + 1, Text: "dependency \"vpc\""}
	}
	return out
}

// Content mode is derived from the Grep field rather than being a mode of its
// own, so the single modeFuzzy arm in View and handleKey still covers it.
func TestGreppingIsDerivedFromTheField(t *testing.T) {
	m := finderModel()
	if m.grepping() {
		t.Error("an empty Grep field should not be content mode")
	}
	m.grepInput.SetValue("dependency")
	if !m.grepping() {
		t.Error("a non-empty Grep field should be content mode")
	}
}

// The Type filter picks the files, the pattern picks the lines, and the Find
// query narrows which of them are listed — the fd-then-rg combination.
func TestFindQueryNarrowsContentHits(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dependency")
	m.addGrepHits(hits("infra/prod/terragrunt.hcl", "infra/stage/terragrunt.hcl", "modules/vpc/main.tf"))

	if got := m.finderLen(); got != 3 {
		t.Fatalf("rows with no query = %d, want 3", got)
	}
	m.input.SetValue("prod")
	m.refuzzy()
	if got := m.finderLen(); got != 1 {
		t.Fatalf("rows for query %q = %d, want 1", "prod", got)
	}
	if got := m.finderPath(0); got != "infra/prod/terragrunt.hcl" {
		t.Errorf("row 0 = %q", got)
	}

	// Clearing the query brings them all back.
	m.input.SetValue("")
	m.refuzzy()
	if got := m.finderLen(); got != 3 {
		t.Errorf("rows after clearing the query = %d, want 3", got)
	}
}

// Enter acts on the file; the line number is context for choosing, not a
// destination.
func TestFinderPathUsesContentHits(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("x")
	m.addGrepHits(hits("a/one.go", "b/two.go"))

	if got := m.finderPath(1); got != "b/two.go" {
		t.Errorf("finderPath(1) = %q, want b/two.go", got)
	}
	if got := m.finderPath(9); got != "" {
		t.Errorf("out-of-range finderPath = %q, want empty", got)
	}
	if got := m.finderPath(-1); got != "" {
		t.Errorf("negative finderPath = %q, want empty", got)
	}
}

// One file full of matches must not crowd out every other file.
func TestGrepHitsRespectTheDisplayCap(t *testing.T) {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = 3
	m.grepInput.SetValue("x")

	m.addGrepHits(hits("a.go", "b.go"))
	m.addGrepHits(hits("c.go", "d.go", "e.go"))
	if got := len(m.grepHits); got != 3 {
		t.Errorf("hits kept = %d, want the cap of 3", got)
	}
}

// A search abandoned by retyping the pattern must not deliver into its
// replacement.
func TestStaleGrepResultsAreDropped(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dependency")
	m.grepGen = 2

	m.Update(grepResultMsg{gen: 1, hits: hits("stale.go"), done: true})
	if len(m.grepHits) != 0 {
		t.Errorf("stale hits were accepted: %v", m.grepHits)
	}

	m.Update(grepResultMsg{gen: 2, hits: hits("fresh.go"), done: true})
	if len(m.grepHits) != 1 || m.grepHits[0].Path != "fresh.go" {
		t.Errorf("current hits = %v, want fresh.go", m.grepHits)
	}
	if m.grepRunning {
		t.Error("a done result left the search marked as running")
	}
}

// Reaching the cap has to stop the search. Draining the rest of a full-tree
// run to throw it away is wasted work on both sides, and leaves "searching…"
// up beside a counter that already says the results are capped.
func TestReachingTheCapStopsTheSearch(t *testing.T) {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = 2
	m.grepInput.SetValue("dependency")
	m.grepGen = 1
	m.grepRunning = true
	m.grepCh = make(chan search.Result, 1)
	m.grepCancel = func() {}

	_, cmd := m.Update(grepResultMsg{gen: 1, hits: hits("a.go", "b.go", "c.go")})

	if !m.grepCapped {
		t.Fatal("three hits under a cap of two did not report capped")
	}
	if cmd != nil {
		t.Error("the search was re-armed after the cap was reached")
	}
	if m.grepRunning {
		t.Error("still reporting a running search after stopping at the cap")
	}
	if m.grepCancel != nil {
		t.Error("ripgrep was not cancelled at the cap")
	}
}

// Below the cap the search carries on.
func TestBelowTheCapTheSearchContinues(t *testing.T) {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = 50
	m.grepInput.SetValue("dependency")
	m.grepGen = 1
	m.grepRunning = true
	m.grepCh = make(chan search.Result, 1)

	_, cmd := m.Update(grepResultMsg{gen: 1, hits: hits("a.go")})
	if cmd == nil {
		t.Error("an uncapped batch did not re-arm the search")
	}
	if !m.grepRunning {
		t.Error("an uncapped batch stopped the search")
	}
}

// Matches dropped for sitting on an over-long line accumulate across batches
// and reset with each new search — the user needs to know the search was not
// exhaustive.
func TestSkippedCountAccumulatesAndResets(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("source")
	m.grepGen = 1
	m.grepCh = make(chan search.Result, 1)

	m.Update(grepResultMsg{gen: 1, hits: hits("a.go"), skipped: 1})
	m.Update(grepResultMsg{gen: 1, hits: hits("b.go"), skipped: 2, done: true})
	if m.grepSkipped != 3 {
		t.Errorf("grepSkipped = %d, want 3", m.grepSkipped)
	}

	m.grepInput.SetValue("other")
	m.scheduleGrep()
	if m.grepSkipped != 0 {
		t.Errorf("grepSkipped = %d after a new search, want 0", m.grepSkipped)
	}
}

// Stopping a search invalidates it, so a batch still in flight when the finder
// is reopened cannot be taken for the current one — it used to be accepted and
// then re-arm on the nilled channel, blocking a goroutine forever.
func TestStoppingInvalidatesResultsInFlight(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.grepInput.SetValue("source")
	m.grepGen = 1
	m.grepCh = make(chan search.Result, 1)
	gen := m.grepGen

	// Leaving the finder and coming back: both stop the search.
	m.startFuzzy()
	m.grepInput.SetValue("source") // as if retyped, so grepping() is true again

	_, cmd := m.Update(grepResultMsg{gen: gen, hits: hits("stale.go")})
	if len(m.grepHits) != 0 {
		t.Errorf("stale hits landed in the new session: %v", m.grepHits)
	}
	if cmd != nil {
		t.Error("a stale batch re-armed the reader — on a nil channel it blocks forever")
	}
}

// ripgrep's own errors (a half-typed regex, most often) have to reach the user.
func TestGrepErrorIsReported(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("(")
	m.grepGen = 1
	m.Update(grepResultMsg{gen: 1, done: true, err: errors.New("regex parse error")})
	if m.grepErr != "regex parse error" {
		t.Errorf("grepErr = %q, want the ripgrep message", m.grepErr)
	}
}

// Retyping the pattern invalidates the search in flight rather than mixing its
// results into the new one.
func TestRetypingThePatternInvalidatesTheSearch(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dep")
	first := m.scheduleGrep()
	if first == nil {
		t.Fatal("scheduleGrep returned no command for a non-empty pattern")
	}
	gen := m.grepGen

	m.grepInput.SetValue("depe")
	m.scheduleGrep()
	if m.grepGen == gen {
		t.Error("retyping did not start a new generation")
	}

	// The first debounce firing is now stale and must not spawn ripgrep.
	m.Update(grepDebounceMsg{gen: gen})
	if m.grepCh != nil {
		t.Error("a stale debounce started a search")
	}
}

// Clearing the Grep field leaves content mode without scheduling anything.
func TestClearingTheGrepFieldEndsContentMode(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dependency")
	m.addGrepHits(hits("a.go"))
	m.grepInput.SetValue("")

	if cmd := m.scheduleGrep(); cmd != nil {
		t.Error("an empty pattern scheduled a search")
	}
	if m.grepping() {
		t.Error("still in content mode with an empty pattern")
	}
	if len(m.grepHits) != 0 {
		t.Errorf("hits survived leaving content mode: %v", m.grepHits)
	}
}

// Each field in use costs a line of the result list.
func TestFinderHeaderGrowsWithEachField(t *testing.T) {
	m := finderModel()
	base := m.finderHeaderLines()
	if base != 1 {
		t.Fatalf("header with only Find = %d lines, want 1", base)
	}
	m.typeInput.SetValue("hcl")
	if got := m.finderHeaderLines(); got != 2 {
		t.Errorf("header with Type = %d lines, want 2", got)
	}
	m.grepInput.SetValue("dependency")
	if got := m.finderHeaderLines(); got != 3 {
		t.Errorf("header with Type and Grep = %d lines, want 3", got)
	}
}

// The whole pipeline over a real tree and a real ripgrep: the filter selects
// the files, the pattern selects the lines, and enter reveals the file.
func TestContentSearchEndToEnd(t *testing.T) {
	if !search.Available() {
		t.Skip("ripgrep not installed")
	}
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("infra/prod/terragrunt.hcl", "dependency \"vpc\" {}\n")
	write("infra/dev/terragrunt.hcl", "locals {}\n")
	write("notes.md", "dependency notes\n")

	m := rootedModel(t, root)

	f, err := search.CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	m.fuzzyFilter = f
	m.grepInput.SetValue("dependency")
	m.grepGen = 1

	// Drive the search to completion the way Update would.
	m.startGrep(1)
	for {
		r, ok := <-m.grepCh
		if !ok {
			break
		}
		m.addGrepHits(r.Hits)
		if r.Done {
			if r.Err != nil {
				t.Fatalf("search failed: %v", r.Err)
			}
			break
		}
	}

	if m.finderLen() != 1 {
		var got []string
		for i := range m.finderLen() {
			got = append(got, m.finderPath(i))
		}
		t.Fatalf("rows = %v, want only infra/prod/terragrunt.hcl "+
			"(notes.md is the wrong type, dev/ the wrong content)", got)
	}
	if got := m.finderPath(0); got != "infra/prod/terragrunt.hcl" {
		t.Fatalf("row 0 = %q", got)
	}

	// Enter reveals the file in the tree.
	m.fuzzyJump()
	sel := m.selected()
	if sel == nil || sel.Path != filepath.Join(root, "infra", "prod", "terragrunt.hcl") {
		t.Errorf("after jumping, selection = %v", sel)
	}
	if m.mode != modeNormal {
		t.Errorf("jump left the finder open")
	}
}

// --- resuming and clearing ---

// The fields survive leaving the finder on their own, so resume is a matter of
// not resetting them. Opening fresh must still clear them.
func TestResumeKeepsTheFieldsAndStartClearsThem(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.input.SetValue("terragr")
	m.typeInput.SetValue("hcl")
	m.grepInput.SetValue("dependency")
	m.finderField = fieldType

	m.resumeFuzzy()
	if m.input.Value() != "terragr" || m.typeInput.Value() != "hcl" || m.grepInput.Value() != "dependency" {
		t.Errorf("resume lost fields: %q / %q / %q",
			m.input.Value(), m.typeInput.Value(), m.grepInput.Value())
	}
	if m.mode != modeFuzzy {
		t.Error("resume did not open the finder")
	}
	if m.finderField != fieldType {
		t.Errorf("resume moved focus to field %d, want the field it was left on", m.finderField)
	}

	m.startFuzzy()
	if m.input.Value() != "" || m.typeInput.Value() != "" || m.grepInput.Value() != "" {
		t.Errorf("a fresh open kept fields: %q / %q / %q",
			m.input.Value(), m.typeInput.Value(), m.grepInput.Value())
	}
	if m.finderField != fieldQuery {
		t.Errorf("a fresh open left focus on field %d, want fieldQuery", m.finderField)
	}
}

// The Type filter has to be back in force on the resumed walk, not merely
// redisplayed — applyTypeFilter short-circuits on unchanged text, so enterFuzzy
// compiles it directly.
func TestResumeRecompilesTheTypeFilter(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.typeInput.SetValue("hcl")
	m.fuzzyFilter = search.Filter{} // as if never compiled

	m.resumeFuzzy()
	if m.fuzzyFilter.Empty() {
		t.Fatal("resumed with an empty filter; the walk would be unfiltered")
	}
	if !m.fuzzyFilter.Match("a/b/main.hcl") || m.fuzzyFilter.Match("a/b/main.tf") {
		t.Error("the resumed filter does not behave like *.hcl")
	}
}

// A resumed content search is re-run: the old hits are stale and their
// generation was invalidated when the finder was left.
func TestResumeRerunsAContentSearch(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.grepInput.SetValue("dependency")
	m.grepHits = hits("stale.go")

	m.resumeFuzzy()
	if len(m.grepHits) != 0 {
		t.Errorf("stale hits survived the resume: %v", m.grepHits)
	}
	if !m.grepRunning {
		t.Error("a resumed content search was not re-armed")
	}
}

// With no pattern there is nothing to re-run.
func TestResumeWithoutAPatternRunsNoSearch(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.typeInput.SetValue("hcl")

	m.resumeFuzzy()
	if m.grepRunning {
		t.Error("resume started a search with an empty Grep field")
	}
}

// The row that was jumped from is re-selected once it shows up again.
func TestResumeRestoresTheRow(t *testing.T) {
	m := finderModel()
	m.lastPick = finderPick{rel: "c/three.go"}
	m.resumeWant = m.lastPick

	m.addFuzzyCands([]string{"a/one.go", "b/two.go", "c/three.go", "d/four.go"})
	if got := m.finderPath(m.fuzzySel); got != "c/three.go" {
		t.Errorf("selection = %q, want the row resumed onto", got)
	}
	if !m.resumeWant.isZero() {
		t.Error("the target was not cleared after being matched")
	}
}

// In content mode the same file appears once per matching line, so the line
// number picks the occurrence.
func TestResumeRestoresTheContentRowByLine(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dependency")
	m.resumeWant = finderPick{rel: "b.go", line: 2}

	m.addGrepHits([]search.Hit{
		{Path: "a.go", Line: 1}, {Path: "b.go", Line: 1},
		{Path: "b.go", Line: 2}, {Path: "c.go", Line: 1},
	})
	if got := m.finderPath(m.fuzzySel); got != "b.go" {
		t.Fatalf("selection = %q, want b.go", got)
	}
	if got := m.grepHits[m.grepRows[m.fuzzySel]].Line; got != 2 {
		t.Errorf("selected line = %d, want 2", got)
	}
}

// A file that is gone leaves the selection at the top rather than anywhere
// arbitrary, and the walk finishing stops the search for it.
func TestResumeGivesUpWhenTheRowIsGone(t *testing.T) {
	m := finderModel()
	m.fuzzyGen = 1
	m.resumeWant = finderPick{rel: "deleted.go"}

	m.Update(fuzzyCandsMsg{gen: 1, cands: []string{"a/one.go", "b/two.go"}, done: true})
	if m.fuzzySel != 0 {
		t.Errorf("selection = %d, want the top of the list", m.fuzzySel)
	}
	if !m.resumeWant.isZero() {
		t.Error("a finished walk left the target pending")
	}
}

// In content mode the walk runs alongside ripgrep and usually finishes first.
// It must not cancel the restore: the rows it completes are not the rows being
// restored into.
func TestWalkFinishingDoesNotCancelAContentRestore(t *testing.T) {
	m := finderModel()
	m.grepInput.SetValue("dependency")
	m.fuzzyGen, m.grepGen = 1, 1
	m.grepCh = make(chan search.Result, 1)
	m.resumeWant = finderPick{rel: "b.go", line: 2}

	// The walk completes with the target nowhere in its candidates.
	m.Update(fuzzyCandsMsg{gen: 1, cands: []string{"a.go", "b.go"}, done: true})
	if m.resumeWant.isZero() {
		t.Fatal("the walk finishing cancelled a content-mode restore")
	}

	// ripgrep then produces it, and the row is selected.
	m.Update(grepResultMsg{gen: 1, hits: []search.Hit{
		{Path: "a.go", Line: 1}, {Path: "b.go", Line: 2},
	}, done: true})
	if got := m.finderPath(m.fuzzySel); got != "b.go" {
		t.Errorf("selection = %q, want b.go", got)
	}
}

// Typing means the user has moved on; a pending restore must not yank the
// selection out from under them.
func TestTypingAbandonsTheResumeTarget(t *testing.T) {
	m := finderModel()
	m.resumeWant = finderPick{rel: "c/three.go"}
	m.fuzzyCands = []string{"a/one.go", "b/two.go", "c/three.go"}

	m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if !m.resumeWant.isZero() {
		t.Error("the target survived a keystroke")
	}
}

// Clearing empties all three fields, drops the filter, and leaves focus put.
func TestClearFinderEmptiesEverything(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.input.SetValue("terragr")
	m.typeInput.SetValue("hcl")
	m.grepInput.SetValue("dependency")
	m.grepHits = hits("a.go")
	m.finderField = fieldGrep
	f, err := search.CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	m.fuzzyFilter = f

	m.clearFinder()

	if m.input.Value() != "" || m.typeInput.Value() != "" || m.grepInput.Value() != "" {
		t.Errorf("fields survived the clear: %q / %q / %q",
			m.input.Value(), m.typeInput.Value(), m.grepInput.Value())
	}
	if !m.fuzzyFilter.Empty() {
		t.Error("the compiled filter survived the clear")
	}
	if len(m.grepHits) != 0 {
		t.Errorf("content hits survived the clear: %v", m.grepHits)
	}
	if m.finderField != fieldGrep {
		t.Errorf("clearing moved focus to field %d; it should stay put", m.finderField)
	}
	if m.mode != modeFuzzy {
		t.Error("clearing left the finder")
	}
}

// ctrl+o is finder-local, like the other finder keys, and never reaches the
// normal-mode binding table where it would shadow a command key.
func TestFinderClearIsFinderLocal(t *testing.T) {
	m := &Model{cfg: config.Default()}
	m.buildBindings()

	if got := m.actionKeys["finder-clear"]; got != "ctrl+o" {
		t.Errorf("default finder-clear = %q, want ctrl+o", got)
	}
	if _, ok := m.bindings["ctrl+o"]; ok {
		t.Error("finder-clear leaked into the normal-mode binding table")
	}
	// Resume is the opposite: it is a normal-mode action and must be bound.
	if got := m.actionKeys["finder-resume"]; got != "f" {
		t.Errorf("default finder-resume = %q, want f", got)
	}
	if _, ok := m.bindings["f"]; !ok {
		t.Error("finder-resume is not bound in normal mode")
	}
}

// The mode collision guard: "f" resumes from the tree, but inside the finder
// it is an ordinary character and has to reach the query.
func TestPlainFTypesInsideTheFinder(t *testing.T) {
	m := finderModel()
	m.input.SetValue("abc")
	m.input.SetCursor(0)

	m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})

	if got := m.input.Value(); got != "fabc" {
		t.Errorf("query = %q, want fabc — f should type, not resume", got)
	}
	if m.mode != modeFuzzy {
		t.Error("f left the finder")
	}
}

// --- the shown ripgrep command ---

// A Type filter alone is enough to have something worth copying: the command
// that lists the files it selects. With neither field there is nothing to say.
func TestFinderCommandFollowsWhicheverFieldsAreFilled(t *testing.T) {
	f, err := search.CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}

	empty := rootedModel(t, t.TempDir())
	if got := empty.finderCommand(); got != "" {
		t.Errorf("no Type and no Grep = %q, want empty", got)
	}

	typeOnly := rootedModel(t, t.TempDir())
	typeOnly.fuzzyFilter = f
	got := typeOnly.finderCommand()
	if !strings.Contains(got, "--files") {
		t.Errorf("Type alone = %q, want a --files listing", got)
	}
	if strings.Contains(got, "-e") {
		t.Errorf("Type alone = %q, want no pattern", got)
	}
	if !strings.Contains(got, "'*.hcl'") {
		t.Errorf("Type alone = %q, want the filter globs", got)
	}

	// Typing a pattern turns it into a content search.
	both := rootedModel(t, t.TempDir())
	both.fuzzyFilter = f
	both.grepInput.SetValue("dependency")
	got = both.finderCommand()
	if strings.Contains(got, "--files") {
		t.Errorf("Type and Grep = %q, should search rather than list", got)
	}
	if !strings.Contains(got, "-e") || !strings.Contains(got, "'*.hcl'") {
		t.Errorf("Type and Grep = %q, want both the pattern and the globs", got)
	}

	// Grep alone still works, filter or no filter.
	grepOnly := rootedModel(t, t.TempDir())
	grepOnly.grepInput.SetValue("dependency")
	if got := grepOnly.finderCommand(); !strings.Contains(got, "-e") {
		t.Errorf("Grep alone = %q, want a search", got)
	}
}

// The listing form must not carry flags that only make sense when searching.
func TestFileListCommandOmitsSearchOnlyFlags(t *testing.T) {
	f, err := search.CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, t.TempDir())
	m.fuzzyFilter = f
	m.cfg.General.FuzzyGrepMaxPerFile = 5

	// Compared as whole words: "-n" is a substring of "--no-ignore", which the
	// listing legitimately carries.
	fields := strings.Fields(m.finderCommand())
	for _, unwanted := range []string{"--max-count", "-n", "--json", "-e"} {
		if slices.Contains(fields, unwanted) {
			t.Errorf("--files command %v should not carry %q", fields, unwanted)
		}
	}
}

// Everything that decides the result set has to appear, and the whole thing
// has to survive a paste into a shell.
func TestGrepCommandIsPasteable(t *testing.T) {
	root := t.TempDir()
	m := rootedModel(t, root)
	f, err := search.CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	m.fuzzyFilter = f
	m.showHidden, m.showIgnored = true, true
	m.cfg.General.FuzzyGrepMaxPerFile = 5
	m.grepInput.SetValue("dependency \"vpc\"")

	got := m.finderCommand()
	for _, want := range []string{"rg", "-n", "--hidden", "--no-ignore", "--max-count 5", "'*.hcl'", "!.git/", "-e"} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q is missing %q", got, want)
		}
	}
	// A pattern with a space and a quote must come back as one shell word.
	if !strings.Contains(got, `'dependency "vpc"'`) {
		t.Errorf("command %q does not quote the pattern safely", got)
	}
	// The root is the search path, so the command runs from anywhere.
	if !strings.HasSuffix(got, config.ShellQuote(root)) {
		t.Errorf("command %q does not end with the root %q", got, root)
	}

	// Scoped, that path narrows with it — otherwise the copied command would
	// describe a different result set than the one on screen.
	sub := filepath.Join(root, "infra", "prod")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m.scopeDir = "infra/prod"
	if got := m.finderCommand(); !strings.HasSuffix(got, config.ShellQuote(sub)) {
		t.Errorf("scoped command %q does not end with %q", got, sub)
	}
}

// A pattern that would be read as a flag, or that contains a quote, must not
// break the command the user copies.
func TestGrepCommandQuotesHostilePatterns(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	for _, pattern := range []string{"--force", "it's", "a b", "$(rm -rf /)", "`id`"} {
		m.grepInput.SetValue(pattern)
		got := m.finderCommand()
		if !strings.Contains(got, "-e ") {
			t.Errorf("pattern %q: no -e in %q", pattern, got)
		}
		if strings.Contains(got, " "+pattern+" ") && strings.ContainsAny(pattern, " '$`") {
			t.Errorf("pattern %q appears unquoted in %q", pattern, got)
		}
	}
}

// --- session match limit ---

// candModel is a finder holding n candidates and a low configured cap, for
// exercising the limit.
func candModel(n, limit int) *Model {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = limit
	for i := range n {
		m.fuzzyCands = append(m.fuzzyCands, fmt.Sprintf("pkg%03d/handler.go", i))
	}
	return m
}

// The zero value has to mean 1×, since tests and fresh models never set it.
func TestFuzzyFactorDefaultsToOne(t *testing.T) {
	m := candModel(0, 10)
	if got := m.fuzzyFactor(); got != 1 {
		t.Errorf("fuzzyFactor() = %d, want 1", got)
	}
	if got := m.fuzzyLimit(); got != 10 {
		t.Errorf("fuzzyLimit() = %d, want the configured 10", got)
	}
}

// The list has to say when it is hiding results, or there is no reason to
// press ctrl+g.
func TestFinderCappedReportsDroppedResults(t *testing.T) {
	m := candModel(50, 5)
	m.refuzzy() // empty query: the browse list
	if !m.finderCapped() {
		t.Error("50 candidates under a cap of 5 did not report capped")
	}

	small := candModel(3, 5)
	small.refuzzy()
	if small.finderCapped() {
		t.Error("3 candidates under a cap of 5 reported capped")
	}

	// And with a query in play.
	q := candModel(50, 5)
	q.input.SetValue("handler")
	q.refuzzy()
	if !q.finderCapped() {
		t.Error("a capped query result did not report capped")
	}
}

// Raising the limit reveals more of the candidates already walked — no second
// walk, and the selection stays where the user left it.
func TestRaiseFuzzyLimitRevealsMoreWithoutRewalking(t *testing.T) {
	m := candModel(50, 5)
	m.input.SetValue("handler")
	m.refuzzy()
	if len(m.fuzzyMatches) != 5 {
		t.Fatalf("matches = %d, want the cap of 5", len(m.fuzzyMatches))
	}
	m.moveFuzzySel(3)
	cands := len(m.fuzzyCands)

	if cmd := m.raiseFuzzyLimit(); cmd != nil {
		t.Error("raising in name mode returned a command; no re-walk should be needed")
	}
	if got := m.fuzzyFactor(); got != 2 {
		t.Errorf("factor = %d, want 2", got)
	}
	if len(m.fuzzyMatches) != 10 {
		t.Errorf("matches after raising = %d, want 10", len(m.fuzzyMatches))
	}
	if m.fuzzySel != 3 {
		t.Errorf("selection = %d, want it kept at 3", m.fuzzySel)
	}
	if len(m.fuzzyCands) != cands {
		t.Errorf("candidates changed (%d -> %d); the walk should not have re-run", cands, len(m.fuzzyCands))
	}

	// Each press multiplies by one more.
	m.raiseFuzzyLimit()
	if got := m.fuzzyFactor(); got != 3 {
		t.Errorf("factor after a second raise = %d, want 3", got)
	}
	if len(m.fuzzyMatches) != 15 {
		t.Errorf("matches after a second raise = %d, want 15", len(m.fuzzyMatches))
	}
}

// Raising past the number of candidates shows them all and stops reporting a cap.
func TestRaiseFuzzyLimitPastEveryCandidate(t *testing.T) {
	m := candModel(12, 5)
	m.refuzzy()
	for range 3 { // 5 -> 20
		m.raiseFuzzyLimit()
	}
	if len(m.fuzzyMatches) != 12 {
		t.Errorf("matches = %d, want all 12 candidates", len(m.fuzzyMatches))
	}
	if m.finderCapped() {
		t.Error("still reporting capped with every candidate shown")
	}
}

// The raise is a session setting: reopening the finder keeps it.
func TestRaisedLimitSurvivesReopeningTheFinder(t *testing.T) {
	root := t.TempDir()
	m := rootedModel(t, root)
	m.raiseFuzzyLimit()
	m.raiseFuzzyLimit()

	m.startFuzzy()
	if got := m.fuzzyFactor(); got != 3 {
		t.Errorf("factor after reopening = %d, want it kept at 3", got)
	}
}

// Content-mode hits past the cap were discarded, so raising has to search again.
func TestRaiseInContentModeResearches(t *testing.T) {
	m := finderModel()
	m.cfg.General.FuzzyMaxMatches = 2
	m.grepInput.SetValue("dependency")
	m.addGrepHits(hits("a.go", "b.go", "c.go"))

	if !m.finderCapped() {
		t.Fatal("dropped content hits did not report capped")
	}
	cmd := m.raiseFuzzyLimit()
	if cmd == nil {
		t.Error("raising in content mode returned no command; the search has to re-run")
	}
	if len(m.grepHits) != 0 {
		t.Errorf("old hits survived the re-search: %v", m.grepHits)
	}
	if m.grepCapped {
		t.Error("capped flag survived the re-search")
	}
}

// ctrl+g is remappable like the other finder-local keys, and never leaks into
// normal mode where it would shadow a command key.
func TestFinderMoreIsFinderLocal(t *testing.T) {
	cfg := config.Default()
	m := &Model{cfg: cfg}
	m.buildBindings()

	if got := m.actionKeys["finder-more"]; got != "ctrl+g" {
		t.Errorf("default finder-more = %q, want ctrl+g", got)
	}
	if _, ok := m.bindings["ctrl+g"]; ok {
		t.Error("finder-more leaked into the normal-mode binding table")
	}
}

// --- forward delete vs half-page scroll ---

func TestCtrlDDeletesForwardOnlyWhenThereIsSomethingToDelete(t *testing.T) {
	ctrlD := tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}

	// Cursor mid-text: ctrl+d edits.
	edit := candModel(50, 5)
	edit.input.SetValue("terragrunt")
	edit.input.SetCursor(4)
	edit.refuzzy()
	sel := edit.fuzzySel
	edit.handleKey(ctrlD)
	if got := edit.input.Value(); got != "terrgrunt" {
		t.Errorf("value = %q, want %q (the \"a\" deleted)", got, "terrgrunt")
	}
	if edit.fuzzySel != sel {
		t.Errorf("editing also scrolled the list: sel %d -> %d", sel, edit.fuzzySel)
	}

	// Cursor at the end: nothing to delete, so it scrolls.
	scroll := candModel(50, 50)
	scroll.input.SetValue("handler")
	scroll.input.CursorEnd()
	scroll.refuzzy()
	scroll.handleKey(ctrlD)
	if got := scroll.input.Value(); got != "handler" {
		t.Errorf("value = %q, want it untouched", got)
	}
	if scroll.fuzzySel == 0 {
		t.Error("ctrl+d at end of text did not scroll the list")
	}

	// Empty field: scrolls too — this is the browsing case.
	empty := candModel(50, 50)
	empty.refuzzy()
	empty.handleKey(ctrlD)
	if empty.fuzzySel == 0 {
		t.Error("ctrl+d with an empty query did not scroll the list")
	}
}

// The rule follows focus: deleting in the Type field edits that field. This
// needs a real root, because editing the filter restarts the walk.
func TestCtrlDFollowsTheFocusedField(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.finderField = fieldType
	m.input.Blur()
	m.typeInput.Focus()
	m.typeInput.SetValue("hcl")
	m.typeInput.SetCursor(0)

	m.handleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if got := m.typeInput.Value(); got != "cl" {
		t.Errorf("Type field = %q, want %q", got, "cl")
	}
}

// pgdown is unambiguous: it always scrolls, whatever the cursor is doing.
func TestPgDownAlwaysScrolls(t *testing.T) {
	m := candModel(50, 50)
	m.input.SetValue("handler")
	m.input.SetCursor(0)
	m.refuzzy()

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := m.input.Value(); got != "handler" {
		t.Errorf("pgdown edited the query: %q", got)
	}
	if m.fuzzySel == 0 {
		t.Error("pgdown did not scroll")
	}
}

func strsOf(ms []fuzzy.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Str
	}
	return out
}

// --- running commands from the finder ---

// finderCmdModel is a finder over one file, with a command bound to a chord.
func finderCmdModel(t *testing.T, run, mode string) *Model {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, root)
	m.cfg.Commands = map[string]config.Command{
		"open": {Run: run, Mode: mode, FinderKey: "ctrl+e"},
	}
	m.finderCmds = map[string]string{"ctrl+e": "open"}
	m.fuzzyMatches = []fuzzy.Match{{Str: "a.go"}}
	m.fuzzySel = 0
	return m
}

// The whole point of the feature: a command runs against the highlighted row
// without closing the finder. An interactive child blocks the event loop
// rather than running beside it, so the finder is frozen and repainted intact;
// a background one never touches the terminal at all.
func TestFinderCommandStaysInTheFinder(t *testing.T) {
	for _, mode := range []string{config.ModeBackground, config.ModeInteractive} {
		t.Run(mode, func(t *testing.T) {
			m := finderCmdModel(t, "true", mode)

			m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

			if m.mode != modeFuzzy {
				t.Error("the command closed the finder")
			}
			if m.fuzzySel != 0 || len(m.fuzzyMatches) != 1 {
				t.Errorf("results disturbed: sel=%d matches=%d", m.fuzzySel, len(m.fuzzyMatches))
			}
		})
	}
}

// The row's absolute path and a Grep hit's line reach the template. Run the
// returned command for real: expansion is the part worth checking end to end.
func TestFinderCommandVars(t *testing.T) {
	m := finderCmdModel(t, "printf '%s' {path}:{line}", config.ModeBackground)

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	done, ok := cmd().(cmdDoneMsg)
	if !ok {
		t.Fatalf("command returned %T, want cmdDoneMsg", cmd())
	}
	// No Grep, so no line: {line} falls back to 1 rather than 0.
	if want := filepath.Join(m.tr.Root.Path, "a.go") + ":1"; done.out != want {
		t.Errorf("expanded to %q, want %q", done.out, want)
	}
	if m.lastPick.rel != "a.go" || m.lastPick.line != 0 {
		t.Errorf("lastPick = %+v, want {a.go 0}", m.lastPick)
	}
}

// On a content row the matched line goes with the path, so the editor opens
// where the match is rather than at the top of the file.
func TestFinderCommandVarsCarryTheGrepLine(t *testing.T) {
	m := finderCmdModel(t, "printf '%s' {line}", config.ModeBackground)
	m.grepInput.SetValue("package")
	m.grepHits = []search.Hit{{Path: "a.go", Line: 42, Text: "package a"}}
	m.grepRows = []int{0}

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if done := cmd().(cmdDoneMsg); done.out != "42" {
		t.Errorf("{line} = %q, want 42", done.out)
	}
	if m.lastPick != (finderPick{rel: "a.go", line: 42}) {
		t.Errorf("lastPick = %+v, want {a.go 42}", m.lastPick)
	}
}

// Returning from an interactive command re-reads the tree. That must not
// disturb the finder underneath it: reflatten moves the tree cursor, which is
// a different thing from the finder's selection.
func TestCmdDoneLeavesTheFinderIntact(t *testing.T) {
	m := finderCmdModel(t, "true", config.ModeInteractive)
	m.fuzzyMatches = []fuzzy.Match{{Str: "a.go"}, {Str: "b.go"}, {Str: "c.go"}}
	m.fuzzySel, m.fuzzyScroll = 2, 1

	m.handleCmdDone(cmdDoneMsg{name: "open", interactive: true})

	if m.mode != modeFuzzy {
		t.Error("returning from the command left the finder")
	}
	if m.fuzzySel != 2 || m.fuzzyScroll != 1 || len(m.fuzzyMatches) != 3 {
		t.Errorf("finder disturbed: sel=%d scroll=%d matches=%d",
			m.fuzzySel, m.fuzzyScroll, len(m.fuzzyMatches))
	}
}

// An empty result list is not an error — there is simply nothing to run on.
func TestFinderCommandOnNoRows(t *testing.T) {
	m := finderCmdModel(t, "true", config.ModeBackground)
	m.fuzzyMatches = nil

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

	if cmd != nil {
		t.Error("a command ran with no row highlighted")
	}
	if m.mode != modeFuzzy {
		t.Error("the finder closed")
	}
}

// A finder_key must not displace a key the finder handles itself, so the
// command lookup comes last. Reserved keys are also rejected at config load.
func TestFinderKeyCannotDisplaceAFinderKey(t *testing.T) {
	m := finderCmdModel(t, "true", config.ModeBackground)
	m.finderCmds = map[string]string{"ctrl+g": "open"}
	m.fuzzyLimitFactor = 1

	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	if m.fuzzyLimitFactor != 2 {
		t.Errorf("limit factor = %d, want 2 — ctrl+g must stay finder-more",
			m.fuzzyLimitFactor)
	}
}

// ctrl+e is textinput's move-to-line-end. Claiming it for the finder is
// deliberate, and costs nothing: "end" still gets you there.
func TestCtrlEIsClaimedButEndStillWorks(t *testing.T) {
	m := finderCmdModel(t, "true", config.ModeBackground)
	m.input.SetValue("abc")
	m.input.SetCursor(0)

	m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if got := m.input.Position(); got != 0 {
		t.Errorf("cursor moved to %d — ctrl+e reached the text input", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	if got := m.input.Position(); got != 3 {
		t.Errorf("end put the cursor at %d, want 3", got)
	}
}

// --- scoping the finder to a directory ---

// The assertion the whole design rests on: a walk started inside the tree
// still speaks root-relative. Everything downstream — fuzzyJump, the ranking
// map, resume, runFinderCommand — resolves finder paths against the tree root,
// so a scoped walk that rebased its output would silently open wrong files.
func TestScopedWalkStillEmitsRootRelativePaths(t *testing.T) {
	root := fixtureTree(t, 0)

	p := params(filepath.Join(root, "infra", "prod"), "", 1000)
	p.startRel = "infra/prod"
	got, _ := collectWalk(t, p)
	slices.Sort(got)

	want := []string{
		"infra/prod/eu",
		"infra/prod/eu/terragrunt.hcl",
		"infra/prod/us",
		"infra/prod/us/terragrunt.hcl",
	}
	if !slices.Equal(got, want) {
		t.Errorf("scoped walk = %v,\nwant %v", got, want)
	}
}

// The walk seeds its candidates with the rows on screen, which come from the
// whole tree. Unfiltered, a scoped search would offer files from outside it.
func TestScopedWalkDropsVisibleRowsOutsideIt(t *testing.T) {
	rels := []string{"a.go", "infra/prod/x.hcl", "infra/stage/y.hcl", "infra/prod"}

	if got := underScope(rels, "infra/prod"); !slices.Equal(got, []string{"infra/prod/x.hcl"}) {
		t.Errorf("underScope = %v, want only infra/prod/x.hcl", got)
	}
	// Unscoped keeps everything, including the directory row itself.
	if got := underScope(rels, ""); !slices.Equal(got, rels) {
		t.Errorf("unscoped underScope = %v, want all of %v", got, rels)
	}
}

// scopeAbs is what the walk starts from and what the copied rg command points
// at, so it has to fall back to the whole tree when there is no scope.
func TestScopeAbs(t *testing.T) {
	m := rootedModel(t, t.TempDir())

	if got := m.scopeAbs(); got != m.tr.Root.Path {
		t.Errorf("unscoped scopeAbs = %q, want the root %q", got, m.tr.Root.Path)
	}
	m.scopeDir = "internal/app"
	if want := filepath.Join(m.tr.Root.Path, "internal", "app"); m.scopeAbs() != want {
		t.Errorf("scoped scopeAbs = %q, want %q", m.scopeAbs(), want)
	}
}

// The directory is the selection itself when it is one, else its parent; the
// root normalises to "" since scoping to it is a no-op.
func TestSelectionDir(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"infra/prod/terragrunt.hcl"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := rootedModel(t, root)
	m.tr.ExpandRel("infra")
	m.tr.ExpandRel("infra/prod")
	m.reflatten()

	if got := m.selectionDir(); got != "" {
		t.Errorf("on the root, selectionDir = %q, want empty", got)
	}
	for _, tc := range []struct{ sel, want string }{
		{"infra", "infra"},                          // a directory scopes to itself
		{"infra/prod", "infra/prod"},                //
		{"infra/prod/terragrunt.hcl", "infra/prod"}, // a file scopes to its parent
	} {
		m.selectPath(filepath.Join(root, filepath.FromSlash(tc.sel)))
		if got := m.selectionDir(); got != tc.want {
			t.Errorf("on %s, selectionDir = %q, want %q", tc.sel, got, tc.want)
		}
	}
}

// The scope belongs to a finder session and is decided at entry: "F" reads it
// from the cursor, "/" drops it, and resume keeps whatever was in force.
func TestScopeIsDecidedAtEntry(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/one.go", "b/two.go"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := rootedModel(t, root)
	m.selectPath(filepath.Join(root, "a"))

	m.startFuzzyHere()
	if got := m.scopeDir; got != "a" {
		t.Fatalf("after F, scopeDir = %q, want a", got)
	}

	// Resuming keeps it even though the cursor has moved on — that is what
	// makes f return to the results it left.
	m.mode = modeNormal
	m.selectPath(filepath.Join(root, "b"))
	m.resumeFuzzy()
	if got := m.scopeDir; got != "a" {
		t.Errorf("after f, scopeDir = %q, want a — resume must not re-read it", got)
	}

	// "/" is the way back to the whole tree.
	m.mode = modeNormal
	m.startFuzzy()
	if got := m.scopeDir; got != "" {
		t.Errorf("after /, scopeDir = %q, want it cleared", got)
	}

	// On the root there is nothing to confine to, so F is just "/".
	m.mode = modeNormal
	m.cursor = 0
	m.startFuzzyHere()
	if got := m.scopeDir; got != "" {
		t.Errorf("F on the root gave scopeDir = %q, want empty", got)
	}
}

// ctrl+o empties the three fields. The scope is not one of them: it belongs to
// the session, so clearing the query must not silently widen the search.
func TestClearFinderKeepsTheScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := rootedModel(t, root)
	m.scopeDir = "internal/app"
	m.input.SetValue("handle")
	m.typeInput.SetValue("go")
	m.grepInput.SetValue("KeyPress")

	m.clearFinder()

	if m.scopeDir != "internal/app" {
		t.Errorf("clear changed the scope to %q", m.scopeDir)
	}
	if m.input.Value() != "" || m.typeInput.Value() != "" || m.grepInput.Value() != "" {
		t.Error("clear left a field populated")
	}
}

// The Dir line is drawn only when there is a scope to report, so it never
// states a possibility — an always-present line that changes as the cursor
// moves reads as part of the query rather than a fact about the search.
func TestDirLineOnlyAppearsWhenScoped(t *testing.T) {
	m := finderModel()
	m.width = 80

	base := m.finderHeaderLines()
	for _, l := range m.renderFinderHeader() {
		if strings.Contains(plainText(l), "Dir") {
			t.Fatalf("unscoped header has a Dir line: %q", plainText(l))
		}
	}

	m.scopeDir = "internal/app"
	if got := m.finderHeaderLines(); got != base+1 {
		t.Errorf("scoped header = %d lines, want %d", got, base+1)
	}
	var found bool
	for _, l := range m.renderFinderHeader() {
		if strings.Contains(plainText(l), "internal/app") {
			found = true
		}
	}
	if !found {
		t.Error("scoped header does not name the directory")
	}
	// It must return exactly finderHeaderLines() lines or the list below it
	// stops lining up.
	if got := len(m.renderFinderHeader()); got != m.finderHeaderLines() {
		t.Errorf("rendered %d lines, finderHeaderLines says %d", got, m.finderHeaderLines())
	}
}

// A scope that vanished while the finder was away must not present as an empty
// result list: ReadDir failure inside the walk is silent, so it is caught first.
func TestMissingScopeFallsBackToTheRoot(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.scopeDir = "gone"

	m.restartFuzzyWalk()

	if m.scopeDir != "" {
		t.Errorf("scopeDir = %q, want it cleared back to the root", m.scopeDir)
	}
	if m.statusMsg == "" {
		t.Error("the fallback was silent; it should say why the scope was dropped")
	}
}

// Finder paths are root-relative, so none of them mean anything after a root
// switch: a bare "README.md" would resolve against the new tree.
func TestLoadRootClearsScopeAndResumeState(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	m.scopeDir = "internal/app"
	m.lastPick = finderPick{rel: "README.md", line: 3}
	m.resumeWant = finderPick{rel: "README.md"}

	if err := m.loadRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if m.scopeDir != "" {
		t.Errorf("scopeDir = %q, want empty after a root switch", m.scopeDir)
	}
	if m.lastPick != (finderPick{}) || m.resumeWant != (finderPick{}) {
		t.Errorf("resume state survived a root switch: last=%+v want=%+v", m.lastPick, m.resumeWant)
	}
}
