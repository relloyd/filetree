package search

import (
	"slices"
	"testing"
)

func TestCompileFilterGlobs(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		globs []string
	}{
		{"empty", "", nil},
		{"bare word is extension or exact name", "hcl", []string{"*.hcl", "hcl"}},
		{"dot prefix is an extension", ".hcl", []string{"*.hcl"}},
		{"dotted word is a filename", "terragrunt.hcl", []string{"terragrunt.hcl"}},
		{"glob passes through", "*.tf", []string{"*.tf"}},
		{"path glob passes through", "infra/**/*.hcl", []string{"infra/**/*.hcl"}},
		{"comma separated", "hcl,tf", []string{"*.hcl", "hcl", "*.tf", "tf"}},
		{"whitespace is trimmed", " hcl , *.tf ", []string{"*.hcl", "hcl", "*.tf"}},
		{"exclusions are negated", "hcl,!vendor/**", []string{"*.hcl", "hcl", "!vendor/**"}},
		{"empty terms are skipped", "hcl,,!", []string{"*.hcl", "hcl"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := CompileFilter(tc.expr)
			if err != nil {
				t.Fatalf("CompileFilter(%q): %v", tc.expr, err)
			}
			got := f.Globs()
			if len(got) == 0 && len(tc.globs) == 0 {
				return
			}
			if !slices.Equal(got, tc.globs) {
				t.Errorf("Globs() = %v, want %v", got, tc.globs)
			}
		})
	}
}

func TestCompileFilterEmpty(t *testing.T) {
	for _, expr := range []string{"", "   ", ",,"} {
		f, err := CompileFilter(expr)
		if err != nil {
			t.Fatalf("CompileFilter(%q): %v", expr, err)
		}
		if !f.Empty() {
			t.Errorf("CompileFilter(%q).Empty() = false, want true", expr)
		}
		if !f.Match("anything/at/all.go") {
			t.Errorf("CompileFilter(%q) should match everything", expr)
		}
	}
}

func TestCompileFilterBadPattern(t *testing.T) {
	for _, expr := range []string{"[", "a/[b"} {
		if _, err := CompileFilter(expr); err == nil {
			t.Errorf("CompileFilter(%q) = nil error, want a syntax error", expr)
		}
	}
}

func TestFilterHighlight(t *testing.T) {
	tests := []struct {
		name string
		expr string
		rel  string
		want []string // the substrings the returned spans cover
	}{
		{"extension marks the suffix", "hcl", "infra/prod/terragrunt.hcl", []string{".hcl"}},
		{"filename marks the whole basename", "terragrunt.hcl", "a/b/terragrunt.hcl", []string{"terragrunt.hcl"}},
		{"glob marks its literal tail", "*.tf", "modules/vpc/main.tf", []string{".tf"}},
		{"path glob marks each literal run", "infra/**/*.hcl", "infra/prod/eu/terragrunt.hcl", []string{"infra/", "/", ".hcl"}},
		{"bare word matching by name marks it all", "Makefile", "src/Makefile", []string{"Makefile"}},
		{"a non-matching path has no spans", "hcl", "docs/notes.md", nil},
		{"multibyte names keep byte offsets aligned", "hcl", "ドキュメント/naïve-café.hcl", []string{".hcl"}},
		{"an empty filter highlights nothing", "", "a/b.go", nil},
		{"exclusions do not highlight", "!vendor/**", "cmd/main.go", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := CompileFilter(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			spans := f.Highlight(tc.rel)
			var got []string
			for _, s := range spans {
				if s.Start < 0 || s.End > len(tc.rel) || s.Start > s.End {
					t.Fatalf("span %+v out of range for %q (len %d)", s, tc.rel, len(tc.rel))
				}
				got = append(got, tc.rel[s.Start:s.End])
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Highlight(%q) covers %q, want %q", tc.rel, got, tc.want)
			}
		})
	}
}

// Spans must not overlap or run backwards, or the renderer would paint the
// same byte twice.
func TestFilterHighlightSpansAreOrdered(t *testing.T) {
	f, err := CompileFilter("infra/**/*.hcl")
	if err != nil {
		t.Fatal(err)
	}
	spans := f.Highlight("infra/prod/eu/terragrunt.hcl")
	if len(spans) < 2 {
		t.Fatalf("expected several spans, got %+v", spans)
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].End {
			t.Errorf("span %d %+v overlaps the previous %+v", i, spans[i], spans[i-1])
		}
	}
}

func TestLiteralRuns(t *testing.T) {
	tests := []struct {
		glob string
		want []string
	}{
		{"*.hcl", []string{".hcl"}},
		{"terragrunt.hcl", []string{"terragrunt.hcl"}},
		{"infra/**/*.hcl", []string{"infra/", "/", ".hcl"}},
		{"a?b", []string{"a", "b"}},
		{"[abc]x", []string{"x"}},
		{"***", nil},
		{"", nil},
	}
	for _, tc := range tests {
		t.Run(tc.glob, func(t *testing.T) {
			if got := literalRuns(tc.glob); !slices.Equal(got, tc.want) {
				t.Errorf("literalRuns(%q) = %q, want %q", tc.glob, got, tc.want)
			}
		})
	}
}

func TestFilterMatch(t *testing.T) {
	tests := []struct {
		name string
		expr string
		rel  string
		want bool
	}{
		{"extension matches at depth", "hcl", "infra/prod/eu/terragrunt.hcl", true},
		{"extension rejects other suffix", "hcl", "infra/prod/eu/main.tf", false},
		{"extension does not match a fuzzy substring", "hcl", "docs/charlie.md", false},
		{"bare word matches an exact filename", "Makefile", "src/Makefile", true},
		{"bare word still matches its extension form", "go", "cmd/ft/main.go", true},
		{"filename term matches basename anywhere", "terragrunt.hcl", "a/b/c/terragrunt.hcl", true},
		{"filename term rejects a different name", "terragrunt.hcl", "a/b/c/other.hcl", false},
		{"basename glob does not cross separators", "*.hcl", "infra/prod/terragrunt.hcl", true},
		{"path glob anchors at the root", "infra/**/*.hcl", "infra/prod/eu/terragrunt.hcl", true},
		{"path glob rejects another root", "infra/**/*.hcl", "modules/prod/eu/terragrunt.hcl", false},
		{"double star spans zero segments", "infra/**/*.hcl", "infra/terragrunt.hcl", true},
		{"single star does not cross a separator", "infra/*.hcl", "infra/prod/terragrunt.hcl", false},
		{"trailing double star matches a subtree", "vendor/**", "vendor/a/b/c.go", true},
		{"exclusion wins over an include", "hcl,!**/.terragrunt-cache/**", "a/.terragrunt-cache/x/terragrunt.hcl", false},
		{"exclusion leaves other includes alone", "hcl,!**/.terragrunt-cache/**", "a/prod/terragrunt.hcl", true},
		{"exclusion only still matches the rest", "!vendor/**", "cmd/ft/main.go", true},
		{"multiple includes are OR'd", "hcl,tf", "infra/main.tf", true},
		{"root-level file matches an extension", "go", "main.go", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := CompileFilter(tc.expr)
			if err != nil {
				t.Fatalf("CompileFilter(%q): %v", tc.expr, err)
			}
			if got := f.Match(tc.rel); got != tc.want {
				t.Errorf("CompileFilter(%q).Match(%q) = %v, want %v", tc.expr, tc.rel, got, tc.want)
			}
		})
	}
}
