package search

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{
			name: "plain search",
			q:    Query{Regex: "dependency"},
			want: []string{"--json", "--no-heading", "--color=never", "-g", "!.git/", "-e", "dependency"},
		},
		{
			name: "toggles follow the tree's visibility",
			q:    Query{Regex: "x", Hidden: true, NoIgnore: true},
			want: []string{"--json", "--no-heading", "--color=never", "--hidden", "--no-ignore", "-g", "!.git/", "-e", "x"},
		},
		{
			// The scope flags come first because searching and listing share
			// them; --max-count is search-only and follows. It is one past the
			// cap: the extra match is how Run knows a file had more.
			name: "per-file cap",
			q:    Query{Regex: "x", MaxPerFile: 5},
			want: []string{"--json", "--no-heading", "--color=never", "-g", "!.git/", "--max-count", "6", "-e", "x"},
		},
		{
			name: "a zero cap is left off",
			q:    Query{Regex: "x", MaxPerFile: 0},
			want: []string{"--json", "--no-heading", "--color=never", "-g", "!.git/", "-e", "x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Args(tc.q); !slices.Equal(got, tc.want) {
				t.Errorf("Args() = %v\nwant     %v", got, tc.want)
			}
		})
	}
}

// The filter the walk uses and the globs ripgrep gets must be the same thing,
// or a filtered content search would disagree with the file list beside it.
func TestArgsCarriesTheFilterGlobs(t *testing.T) {
	f, err := CompileFilter("hcl,!vendor/**")
	if err != nil {
		t.Fatal(err)
	}
	got := Args(Query{Regex: "x", Filter: f})
	want := []string{"-g", "*.hcl", "-g", "hcl", "-g", "!vendor/**"}
	if !containsSeq(got, want) {
		t.Errorf("Args() = %v, want it to contain %v", got, want)
	}
}

// A pattern starting with "-" must reach ripgrep as a pattern, not a flag.
func TestArgsPatternIsNotReadAsAFlag(t *testing.T) {
	got := Args(Query{Regex: "--force"})
	if got[len(got)-2] != "-e" || got[len(got)-1] != "--force" {
		t.Errorf("Args() tail = %v, want [-e --force]", got[len(got)-2:])
	}
}

// A scoped search passes the directory as a trailing positional. It stays
// relative because Run keeps the working directory at the root, which is what
// makes ripgrep go on reporting root-relative paths.
func TestArgsScopePath(t *testing.T) {
	got := Args(Query{Regex: "x", Path: "infra/prod"})
	if last := got[len(got)-1]; last != "infra/prod" {
		t.Errorf("Args() last = %q, want infra/prod", last)
	}
	// The pattern must still be the last thing before it, or "-e" would eat
	// the path instead.
	if got[len(got)-3] != "-e" || got[len(got)-2] != "x" {
		t.Errorf("Args() tail = %v, want [-e x infra/prod]", got[len(got)-3:])
	}
	// Unscoped, there is no positional at all: ripgrep defaults to the cwd.
	if got := Args(Query{Regex: "x"}); got[len(got)-1] != "x" {
		t.Errorf("unscoped Args() ends %q, want the pattern", got[len(got)-1])
	}
}

// Listing files and searching them must agree on which files are in scope,
// which is the whole point of a Type filter that works before a pattern is
// typed. The listing carries nothing that only makes sense when searching.
func TestFileListArgs(t *testing.T) {
	f, err := CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	q := Query{Regex: "x", Filter: f, Hidden: true, NoIgnore: true, MaxPerFile: 5}

	got := FileListArgs(q)
	want := []string{"--files", "--hidden", "--no-ignore", "-g", "*.hcl", "-g", "hcl", "-g", "!.git/"}
	if !slices.Equal(got, want) {
		t.Errorf("FileListArgs() = %v\nwant             %v", got, want)
	}

	// The scope it lists is exactly the scope a search would read.
	searchScope := Args(q)
	for i := 0; i+1 < len(want); i++ {
		if want[i] == "-g" && !containsSeq(searchScope, []string{"-g", want[i+1]}) {
			t.Errorf("glob %q is in the listing but not in the search", want[i+1])
		}
	}
}

// The command shown to the user must select exactly the lines the search ran
// against; only the output encoding may differ, or "run it yourself to check"
// would be checking something else.
func TestDisplayArgsMatchTheExecutedSearch(t *testing.T) {
	f, err := CompileFilter("hcl,!vendor/**")
	if err != nil {
		t.Fatal(err)
	}
	q := Query{Regex: "dependency", Filter: f, Hidden: true, NoIgnore: true, MaxPerFile: 5}

	run, shown := Args(q), DisplayArgs(q)
	outputFlags := map[string]bool{
		"--json": true, "--no-heading": true, "--color=never": true, "-n": true,
	}
	// The run reads one match per file past the cap, to learn that there were
	// more, and shows none of it; the displayed command states the cap itself.
	if !containsSeq(run, []string{"--max-count", "6"}) || !containsSeq(shown, []string{"--max-count", "5"}) {
		t.Errorf("--max-count: run %v, shown %v; want 6 and 5", run, shown)
	}
	strip := func(args []string) []string {
		var out []string
		for i := 0; i < len(args); i++ {
			switch {
			case outputFlags[args[i]]:
			case args[i] == "--max-count":
				i++ // checked above
			default:
				out = append(out, args[i])
			}
		}
		return out
	}
	if !slices.Equal(strip(run), strip(shown)) {
		t.Errorf("matching flags differ:\n run   %v\n shown %v", strip(run), strip(shown))
	}
	if slices.Contains(shown, "--json") {
		t.Error("the displayed command still asks for --json output")
	}
	if !slices.Contains(shown, "-n") {
		t.Error("the displayed command has no line numbers")
	}
}

func TestParseJSONLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Hit
		ok   bool
	}{
		{
			name: "match",
			line: `{"type":"match","data":{"path":{"text":"a/b/terragrunt.hcl"},"lines":{"text":"dependency \"x\"\n"},"line_number":12,"absolute_offset":0,"submatches":[]}}`,
			want: Hit{Path: "a/b/terragrunt.hcl", Line: 12, Text: `dependency "x"`},
			ok:   true,
		},
		{
			name: "a colon in the path survives",
			line: `{"type":"match","data":{"path":{"text":"weird:dir/a:b.go"},"lines":{"text":"x\n"},"line_number":1}}`,
			want: Hit{Path: "weird:dir/a:b.go", Line: 1, Text: "x"},
			ok:   true,
		},
		{
			name: "crlf is trimmed",
			line: `{"type":"match","data":{"path":{"text":"a.txt"},"lines":{"text":"x\r\n"},"line_number":3}}`,
			want: Hit{Path: "a.txt", Line: 3, Text: "x"},
			ok:   true,
		},
		{
			name: "begin event",
			line: `{"type":"begin","data":{"path":{"text":"a.txt"}}}`,
		},
		{
			name: "end event",
			line: `{"type":"end","data":{"path":{"text":"a.txt"},"binary_offset":null}}`,
		},
		{
			name: "summary event",
			line: `{"type":"summary","data":{"elapsed_total":{"secs":0}}}`,
		},
		{
			name: "non-utf8 path is reported as bytes and skipped",
			line: `{"type":"match","data":{"path":{"bytes":"YS9i"},"lines":{"text":"x\n"},"line_number":1}}`,
		},
		{
			name: "garbage",
			line: `not json at all`,
		},
		{
			name: "empty",
			line: ``,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseJSONLine([]byte(tc.line))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (hit %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("hit = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// grepFixture is a small tree with the pattern in two of three .hcl files and
// in one file of another type, so filter and pattern can be told apart.
func grepFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"infra/prod/terragrunt.hcl":  "dependency \"vpc\" {}\n",
		"infra/stage/terragrunt.hcl": "dependency \"vpc\" {}\n",
		"infra/dev/terragrunt.hcl":   "locals {}\n",
		"notes.md":                   "dependency notes\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runAll(t *testing.T, ctx context.Context, root string, q Query) ([]Hit, error) {
	t.Helper()
	out := make(chan Result)
	go Run(ctx, root, q, out)
	var hits []Hit
	var err error
	for r := range out {
		hits = append(hits, r.Hits...)
		if r.Done {
			err = r.Err
		}
	}
	return hits, err
}

// The fd-then-rg combination: the type filter selects the files, the pattern
// selects the lines.
func TestRunFiltersFilesThenLines(t *testing.T) {
	requireRipgrep(t)
	root := grepFixture(t)
	f, err := CompileFilter("hcl")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := runAll(t, t.Context(), root, Query{Regex: `dependency`, Filter: f})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
		if h.Line != 1 {
			t.Errorf("hit %+v: line = %d, want 1", h, h.Line)
		}
		if !strings.Contains(h.Text, "dependency") {
			t.Errorf("hit %+v: text does not contain the pattern", h)
		}
	}
	slices.Sort(paths)
	want := []string{"infra/prod/terragrunt.hcl", "infra/stage/terragrunt.hcl"}
	if !slices.Equal(paths, want) {
		t.Errorf("hits = %v, want %v (notes.md is the wrong type, dev/ the wrong content)", paths, want)
	}
}

// A file past the cap keeps its first matches and gains one placeholder after
// them; a file within it is untouched. No cap at all means no placeholders.
func TestPerFileCap(t *testing.T) {
	hit := func(path string, line int) Hit { return Hit{Path: path, Line: line, Text: "x"} }
	in := []Hit{hit("a", 1), hit("a", 2), hit("a", 3), hit("a", 4), hit("b", 1), hit("b", 2), hit("c", 1), hit("c", 2), hit("c", 3)}

	run := func(max int) []Hit {
		c := perFileCap{max: max}
		var out []Hit
		for _, h := range in {
			if h, ok := c.take(h); ok {
				out = append(out, h)
			}
		}
		return out
	}

	want := []Hit{hit("a", 1), hit("a", 2), {Path: "a", Line: 3, More: true}, hit("b", 1), hit("b", 2), hit("c", 1), hit("c", 2), {Path: "c", Line: 3, More: true}}
	if got := run(2); !slices.Equal(got, want) {
		t.Errorf("cap 2:\n got  %v\n want %v", got, want)
	}
	if got := run(0); !slices.Equal(got, in) {
		t.Errorf("no cap changed the stream: %v", got)
	}
}

// End to end through ripgrep: the extra match Args asks for comes back as the
// placeholder, never as a row.
func TestRunMarksFilesWithMoreMatches(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("many.txt", "x\nx\nx\nx\n")
	write("two.txt", "x\nx\n")

	hits, err := runAll(t, t.Context(), root, Query{Regex: "x", MaxPerFile: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	count := map[string]int{}
	more := map[string]int{}
	for _, h := range hits {
		if h.More {
			more[h.Path]++
			// The first match left out: line 3, after the two shown.
			if h.Line != 3 || h.Text != "" {
				t.Errorf("placeholder = %+v, want line 3 and no text", h)
			}
			continue
		}
		count[h.Path]++
	}
	if count["many.txt"] != 2 || more["many.txt"] != 1 {
		t.Errorf("many.txt: %d matches, %d placeholders; want 2 and 1", count["many.txt"], more["many.txt"])
	}
	if count["two.txt"] != 2 || more["two.txt"] != 0 {
		t.Errorf("two.txt: %d matches, %d placeholders; want 2 and 0 (exactly at the cap is not more)", count["two.txt"], more["two.txt"])
	}
}

// No matches is an empty result, not an error — ripgrep exits 1 for it.
func TestRunNoMatchesIsNotAnError(t *testing.T) {
	requireRipgrep(t)
	hits, err := runAll(t, t.Context(), grepFixture(t), Query{Regex: "nothing-matches-this"})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %v, want none", hits)
	}
}

// A malformed regex has to surface, since the user is typing it.
func TestRunReportsABadPattern(t *testing.T) {
	requireRipgrep(t)
	_, err := runAll(t, t.Context(), grepFixture(t), Query{Regex: "("})
	if err == nil {
		t.Fatal("err = nil, want a regex error from ripgrep")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error spans multiple lines, it has to fit a status line: %q", err)
	}
}

// Abandoning a search is not a failure, and it must not leave ripgrep running.
func TestRunCancelled(t *testing.T) {
	requireRipgrep(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runAll(t, ctx, grepFixture(t), Query{Regex: "dependency"}); err != nil {
		t.Errorf("cancelled search reported %v, want no error", err)
	}
}

func TestReadBoundedLine(t *testing.T) {
	const limit = 16
	// A short line, one far over the limit, then a short one again.
	long := strings.Repeat("x", limit*4)
	input := "alpha\n" + long + "\nomega\n"
	r := bufio.NewReaderSize(strings.NewReader(input), 8) // tiny buffer: force ErrBufferFull

	line, over, err := readBoundedLine(r, limit)
	if over || err != nil || string(line) != "alpha\n" {
		t.Fatalf("first line = %q over=%v err=%v", line, over, err)
	}

	line, over, err = readBoundedLine(r, limit)
	if !over {
		t.Errorf("over-long line was not reported as skipped (line %q)", line)
	}
	if err != nil {
		t.Errorf("over-long line returned err %v, want nil — the stream continues", err)
	}
	if len(line) > limit {
		t.Errorf("kept %d bytes of an over-long line, want at most %d", len(line), limit)
	}

	// The critical property: the reader is left at the start of the *next*
	// line, so one monstrous line costs a result rather than the search.
	line, over, err = readBoundedLine(r, limit)
	if over || string(line) != "omega\n" {
		t.Errorf("line after the over-long one = %q over=%v, want %q", line, over, "omega\n")
	}
	if err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestReadBoundedLineEdges(t *testing.T) {
	// Exactly at the limit is kept.
	r := bufio.NewReaderSize(strings.NewReader("abcd\n"), 16)
	line, over, err := readBoundedLine(r, 5)
	if over || err != nil || string(line) != "abcd\n" {
		t.Errorf("at-limit line = %q over=%v err=%v", line, over, err)
	}

	// A final line with no trailing newline comes back with io.EOF.
	r = bufio.NewReaderSize(strings.NewReader("tail"), 16)
	line, over, err = readBoundedLine(r, 16)
	if string(line) != "tail" || over || !errors.Is(err, io.EOF) {
		t.Errorf("unterminated line = %q over=%v err=%v, want %q with io.EOF", line, over, err, "tail")
	}
}

// The regression test for the hang: a match on a line too long to buffer used
// to abort the scanner, leaving ripgrep blocked writing to a pipe nobody read
// and cmd.Wait blocked forever. The search must now finish, return the normal
// hits, and say how many it dropped.
func TestRunSurvivesAnOverLongLine(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	// ~3 MB on one line: past maxLine even before ripgrep's JSON escaping,
	// which inflates it roughly fourfold.
	huge := "prefix " + strings.Repeat("sourceMappingURL,", 180000) + " end\n"
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bundle.min.js", huge)
	write("a.txt", "a source line\n")
	write("b.txt", "another source line\n")

	done := make(chan struct{})
	var hits []Hit
	var skipped int
	var err error
	go func() {
		defer close(done)
		out := make(chan Result)
		go Run(t.Context(), root, Query{Regex: "source"}, out)
		for r := range out {
			hits = append(hits, r.Hits...)
			skipped += r.Skipped
			if r.Done {
				err = r.Err
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not finish: the over-long line deadlocked it")
	}

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (the minified bundle)", skipped)
	}
	var paths []string
	for _, h := range hits {
		paths = append(paths, h.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"a.txt", "b.txt"}) {
		t.Errorf("hits = %v, want the two normal files", paths)
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("ripgrep not installed")
	}
}

func containsSeq(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// A scoped search must stay root-relative: the finder resolves every hit
// against the tree root, so a path rebased on the scope would open the wrong
// file. Run keeps the working directory at the root and passes the scope as a
// relative argument, which is what makes ripgrep go on reporting from the root.
func TestRunScopedStaysRootRelative(t *testing.T) {
	if !Available() {
		t.Skip("ripgrep not installed")
	}
	root := t.TempDir()
	for _, rel := range []string{"in/a.go", "in/deep/b.go", "out/c.go"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x // needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ch := make(chan Result)
	go Run(t.Context(), root, Query{Regex: "needle", Path: "in"}, ch)
	var got []string
	for r := range ch {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		for _, h := range r.Hits {
			got = append(got, h.Path)
		}
	}
	slices.Sort(got)

	if !slices.Equal(got, []string{"in/a.go", "in/deep/b.go"}) {
		t.Fatalf("scoped hits = %v, want in/a.go and in/deep/b.go only", got)
	}
	for _, p := range got {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("hit %q does not resolve against the root: %v", p, err)
		}
	}
}
