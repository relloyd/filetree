package app

import (
	"testing"

	"github.com/sahilm/fuzzy"
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
	got := rerankMatches("filetree", fuzzy.Find("filetree", cands))
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].Str != "filetree" {
		t.Errorf("top match = %q, want the top-level dir", got[0].Str)
	}
}

func TestRerankKeepsFuzzyOrderAmongPeers(t *testing.T) {
	// Same depth, same basename relationship: better fuzzy score stays first.
	cands := []string{"a/xmainx.go", "a/main.go"}
	got := rerankMatches("main", fuzzy.Find("main", cands))
	if len(got) != 2 || got[0].Str != "a/main.go" {
		t.Errorf("order = %v", got)
	}
}

func TestRerankEmpty(t *testing.T) {
	if out := rerankMatches("x", nil); len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}
