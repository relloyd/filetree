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
		height:        40,
		mode:          modeFuzzy,
		input:         textinput.New(),
		typeInput:     textinput.New(),
		grepInput:     textinput.New(),
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

// Tab cycles the finder's input lines and each field keeps its text.
func TestCycleFinderFieldKeepsText(t *testing.T) {
	m := finderModel()
	m.input.SetValue("terragr")
	m.typeInput.SetValue("hcl")
	m.grepInput.SetValue("dependency")

	if m.finderField != fieldQuery {
		t.Fatalf("initial field = %d", m.finderField)
	}
	for i, want := range []finderField{fieldType, fieldGrep, fieldQuery} {
		m.cycleFinderField(1)
		if m.finderField != want {
			t.Errorf("tab %d landed on field %d, want %d", i+1, m.finderField, want)
		}
	}
	m.cycleFinderField(-1)
	if m.finderField != fieldGrep {
		t.Errorf("shift+tab went to %d, want fieldGrep", m.finderField)
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
	if m.finderField != fieldType {
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

// --- the shown ripgrep command ---

// The command is only meaningful in content mode.
func TestGrepCommandOnlyInContentMode(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	if got := m.grepCommand(); got != "" {
		t.Errorf("grepCommand() outside content mode = %q, want empty", got)
	}
	m.grepInput.SetValue("dependency")
	if got := m.grepCommand(); got == "" {
		t.Error("grepCommand() in content mode is empty")
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

	got := m.grepCommand()
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
}

// A pattern that would be read as a flag, or that contains a quote, must not
// break the command the user copies.
func TestGrepCommandQuotesHostilePatterns(t *testing.T) {
	m := rootedModel(t, t.TempDir())
	for _, pattern := range []string{"--force", "it's", "a b", "$(rm -rf /)", "`id`"} {
		m.grepInput.SetValue(pattern)
		got := m.grepCommand()
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
