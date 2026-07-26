package app

import (
	"fmt"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
)

// Shallow paths and exact basenames must beat deep vendored copies — the
// "run ft in $HOME, type filetree" case.
func TestRerankPrefersShallowAndBasename(t *testing.T) {
	cands := []string{
		"go/pkg/mod/github.com/someone/filetree@v2.1.0/cmd/main.go",
		"Documents/notes/filetree-ideas.md",
		"filetree",
		"work/vendor/filetree/README.md",
	}
	got := rerankMatches("filetree", fuzzy.Find("filetree", cands), nil)
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].Str != "filetree" {
		t.Errorf("top match = %q, want the top-level dir", got[0].Str)
	}
}

// A deep directory that is expanded and on screen must outrank shallower
// non-visible entries with the same name — the "I can see it, jump to it"
// case.
func TestRerankPrefersVisibleEntries(t *testing.T) {
	cands := []string{
		"handlers",
		"pkg/api/handlers",
		"internal/service/http/handlers",
	}
	visible := map[string]bool{"internal/service/http/handlers": true}
	got := rerankMatches("handlers", fuzzy.Find("handlers", cands), visible)
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].Str != "internal/service/http/handlers" {
		t.Errorf("top match = %q, want the visible deep dir", got[0].Str)
	}
	// Without visibility the shallow entry wins as before.
	got = rerankMatches("handlers", fuzzy.Find("handlers", cands), nil)
	if got[0].Str != "handlers" {
		t.Errorf("top match without visibility = %q, want the shallow entry", got[0].Str)
	}
}

func TestRerankKeepsFuzzyOrderAmongPeers(t *testing.T) {
	// Same depth, same basename relationship: better fuzzy score stays first.
	cands := []string{"a/xmainx.go", "a/main.go"}
	got := rerankMatches("main", fuzzy.Find("main", cands), nil)
	if len(got) != 2 || got[0].Str != "a/main.go" {
		t.Errorf("order = %v", got)
	}
}

// The fuzzy list must scroll with the selection: moving past the visible
// window shifts fuzzyScroll so the selected match is always rendered.
func TestMoveFuzzySelScrollsWindow(t *testing.T) {
	m := &Model{height: 15, fuzzyMatches: make([]fuzzy.Match, 30)} // 12 visible rows
	h := m.fuzzyVisibleRows()

	for range 19 {
		m.moveFuzzySel(1)
	}
	if m.fuzzySel != 19 {
		t.Fatalf("sel = %d", m.fuzzySel)
	}
	if m.fuzzySel < m.fuzzyScroll || m.fuzzySel >= m.fuzzyScroll+h {
		t.Errorf("selection %d outside window [%d,%d)", m.fuzzySel, m.fuzzyScroll, m.fuzzyScroll+h)
	}

	// Half-page down past the end clamps to the last match.
	m.moveFuzzySel(100)
	if m.fuzzySel != 29 || m.fuzzyScroll != 30-h {
		t.Errorf("clamped sel/scroll = %d/%d", m.fuzzySel, m.fuzzyScroll)
	}

	// Back to the top.
	m.moveFuzzySel(-100)
	if m.fuzzySel != 0 || m.fuzzyScroll != 0 {
		t.Errorf("top sel/scroll = %d/%d", m.fuzzySel, m.fuzzyScroll)
	}

	// Empty list never panics or goes negative.
	e := &Model{height: 15}
	e.moveFuzzySel(5)
	if e.fuzzySel != 0 || e.fuzzyScroll != 0 {
		t.Errorf("empty list sel/scroll = %d/%d", e.fuzzySel, e.fuzzyScroll)
	}
}

func TestRerankEmpty(t *testing.T) {
	if out := rerankMatches("x", nil, nil); len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}

func limitModel(limit int) *Model {
	m := &Model{
		height: 40,
		input:  textinput.New(),
		cfg:    &config.Config{General: config.General{FuzzyMaxMatches: limit}},
	}
	for i := range 50 {
		m.fuzzyCands = append(m.fuzzyCands, fmt.Sprintf("dir%02d/file.go", i))
	}
	return m
}

// general.fuzzy_max_matches caps both the empty-query browse list and real
// search results.
func TestFuzzyMaxMatchesCapsResults(t *testing.T) {
	m := limitModel(5)

	m.refuzzy() // empty query: the browse list
	if len(m.fuzzyMatches) != 5 {
		t.Errorf("empty-query matches = %d, want 5", len(m.fuzzyMatches))
	}

	m.input.SetValue("file.go") // matches all 50 candidates
	m.refuzzy()
	if len(m.fuzzyMatches) != 5 {
		t.Errorf("query matches = %d, want 5", len(m.fuzzyMatches))
	}
}

// A limit above the candidate count must not pad or panic.
func TestFuzzyMaxMatchesAboveCandidateCount(t *testing.T) {
	m := limitModel(1000)

	m.refuzzy()
	if len(m.fuzzyMatches) != 50 {
		t.Errorf("empty-query matches = %d, want all 50 candidates", len(m.fuzzyMatches))
	}
}

// Models built without a config (and configs predating the setting) fall back
// to the default rather than capping at zero.
func TestFuzzyLimitFallsBackToDefault(t *testing.T) {
	if got := (&Model{}).fuzzyLimit(); got != config.DefaultFuzzyMaxMatches {
		t.Errorf("nil cfg limit = %d, want %d", got, config.DefaultFuzzyMaxMatches)
	}
	m := &Model{cfg: &config.Config{}} // FuzzyMaxMatches zero-valued
	if got := m.fuzzyLimit(); got != config.DefaultFuzzyMaxMatches {
		t.Errorf("zero-valued limit = %d, want %d", got, config.DefaultFuzzyMaxMatches)
	}
}
