package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relloyd/filetree/internal/bookmark"
)

const sample = "package main\n\nfunc alpha() {\n\tif x {\n\t\treturn nil\n\t}\n}\n"

// chdir moves into dir for the duration of a test, which is what stands in for
// helix's working directory: "ft bookmark" resolves a relative buffer name
// against its own cwd, and helix runs it with helix's.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initRepo makes dir a git repository, so the bookmark lands in a repo store
// rather than the global one.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

// only returns the single bookmark in the store for repo, failing otherwise.
func only(t *testing.T, cfgDir, repo string) bookmark.Bookmark {
	t.Helper()
	s := bookmark.Load(bookmark.Dir(cfgDir), bookmark.Canonical(repo))
	if len(s.Bookmarks) != 1 {
		t.Fatalf("store for %q holds %d bookmarks, want 1", repo, len(s.Bookmarks))
	}
	return s.Bookmarks[0]
}

// Rows 1, 2 and 5 of the resolution table: three different working directories
// naming the same file three different ways must all reach the same bookmark.
func TestBookmarkResolvesTheBufferNameAgainstItsOwnCwd(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "deep", "nest", "d.go"), sample)

	for _, tc := range []struct {
		name string
		cwd  string
		arg  string
	}{
		{"relative, from the repo root", repo, "deep/nest/d.go"},
		{"absolute", repo, filepath.Join(repo, "deep", "nest", "d.go")},
		{"absolute, from a subdirectory", filepath.Join(repo, "deep"), filepath.Join(repo, "deep", "nest", "d.go")},
		{"relative, from a subdirectory", filepath.Join(repo, "deep"), "nest/d.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgDir := t.TempDir()
			chdir(t, tc.cwd)

			if err := runBookmark(cfgDir, []string{tc.arg, "5"}); err != nil {
				t.Fatal(err)
			}

			b := only(t, cfgDir, repo)
			if b.Path != "deep/nest/d.go" {
				t.Errorf("stored path = %q, want it repo-relative", b.Path)
			}
			if b.Line != 5 || b.Text() != "\t\treturn nil" {
				t.Errorf("stored %d %q", b.Line, b.Text())
			}
		})
	}
}

// Row 7: an unnamed helix buffer expands to the literal "[scratch]", which
// names no file. Failing loudly is the only feedback helix can show.
func TestBookmarkRejectsWhatIsNotAFile(t *testing.T) {
	repo := t.TempDir()
	chdir(t, repo)

	for _, arg := range []string{"[scratch]", "no-such-file.go", "."} {
		t.Run(arg, func(t *testing.T) {
			cfgDir := t.TempDir()

			if err := runBookmark(cfgDir, []string{arg, "5"}); err == nil {
				t.Fatal("want an error so helix reports the failure")
			}
			if _, err := os.Stat(bookmark.Dir(cfgDir)); err == nil {
				t.Error("a rejected bookmark should not create a store")
			}
		})
	}
}

func TestBookmarkRejectsABadLine(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "a.go"), sample)
	chdir(t, repo)

	for _, line := range []string{"0", "-3", "abc", "999"} {
		if err := runBookmark(t.TempDir(), []string{"a.go", line}); err == nil {
			t.Errorf("line %q should be rejected", line)
		}
	}
}

// Row 3: a file in another project is filed under that project, not the one
// the editor happened to be sitting in.
func TestBookmarkFollowsTheFileNotTheWorkingDirectory(t *testing.T) {
	projA, projB := t.TempDir(), t.TempDir()
	initRepo(t, projA)
	initRepo(t, projB)
	writeFile(t, filepath.Join(projB, "x.go"), sample)
	cfgDir := t.TempDir()
	chdir(t, projA)

	if err := runBookmark(cfgDir, []string{filepath.Join(projB, "x.go"), "3"}); err != nil {
		t.Fatal(err)
	}

	if s := bookmark.Load(bookmark.Dir(cfgDir), bookmark.Canonical(projA)); len(s.Bookmarks) != 0 {
		t.Errorf("landed in the cwd's project: %+v", s.Bookmarks)
	}
	if b := only(t, cfgDir, projB); b.Path != "x.go" {
		t.Errorf("stored %q in projB", b.Path)
	}
}

// A file in no repository still gets remembered, in the global store.
func TestBookmarkOutsideAnyRepoUsesTheGlobalStore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "one\ntwo\nthree\n")
	cfgDir := t.TempDir()
	chdir(t, dir)

	if err := runBookmark(cfgDir, []string{"notes.txt", "2"}); err != nil {
		t.Fatal(err)
	}

	b := only(t, cfgDir, "")
	if !filepath.IsAbs(b.Path) {
		t.Errorf("global store path = %q, want it absolute", b.Path)
	}
	if b.Text() != "two" {
		t.Errorf("text = %q", b.Text())
	}
}

func TestBookmarkLabelIsCollapsedAndCapped(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "a.go"), sample)
	chdir(t, repo)

	cfgDir := t.TempDir()
	if err := runBookmark(cfgDir, []string{"a.go", "3", "  some   spaced\tlabel  "}); err != nil {
		t.Fatal(err)
	}
	if got := only(t, cfgDir, repo).Label; got != "some spaced label" {
		t.Errorf("label = %q", got)
	}

	cfgDir = t.TempDir()
	long := strings.Repeat("x", labelMax*2)
	if err := runBookmark(cfgDir, []string{"a.go", "3", long}); err != nil {
		t.Fatal(err)
	}
	if got := only(t, cfgDir, repo).Label; len(got) != labelMax {
		t.Errorf("label is %d chars, want it capped at %d", len(got), labelMax)
	}
}

// Bookmarking the same place twice is one bookmark, and a different line in
// the same file is a second.
func TestBookmarkingTwiceDoesNotDuplicate(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "a.go"), sample)
	cfgDir := t.TempDir()
	chdir(t, repo)

	for range 3 {
		if err := runBookmark(cfgDir, []string{"a.go", "5"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runBookmark(cfgDir, []string{"a.go", "3"}); err != nil {
		t.Fatal(err)
	}

	s := bookmark.Load(bookmark.Dir(cfgDir), bookmark.Canonical(repo))
	if len(s.Bookmarks) != 2 {
		t.Fatalf("store holds %d bookmarks, want 2", len(s.Bookmarks))
	}
	if s.Bookmarks[0].Line != 3 {
		t.Errorf("newest is line %d, want the one just added", s.Bookmarks[0].Line)
	}
}

// Row 6: a linked worktree resolves to the repository that owns it, so one
// list serves every checkout.
func TestBookmarkInAWorktreeIsFiledUnderTheMainRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "a.go"), sample)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "branch")
	git("worktree", "add", "-q", "-b", "feat", wt)

	cfgDir := t.TempDir()
	chdir(t, wt)
	if err := runBookmark(cfgDir, []string{"a.go", "5"}); err != nil {
		t.Fatal(err)
	}

	// The store is the main repo's, keyed by its real path (macOS /private
	// symlink and all), and the path within it is repo-relative.
	s := bookmark.Load(bookmark.Dir(cfgDir), bookmark.Canonical(repo))
	if len(s.Bookmarks) != 1 {
		t.Fatalf("main repo store holds %d, want 1 (worktree store: %d)",
			len(s.Bookmarks), len(bookmark.Load(bookmark.Dir(cfgDir), wt).Bookmarks))
	}
	b := s.Bookmarks[0]
	// Checkout-relative, not relative to the main repo: the worktree lives
	// elsewhere on disk entirely, so measuring against the main repo would give
	// "../.." and the bookmark could never be resolved against anything.
	if b.Path != "a.go" {
		t.Errorf("path = %q, want it relative to the checkout", b.Path)
	}
	// The claim that one list serves every checkout: taken in the worktree,
	// usable in the main one.
	if r := bookmark.Resolve(bookmark.Canonical(repo), b); r.State != bookmark.Exact || r.AtLine != 5 {
		t.Errorf("resolving in the main checkout: %v at %d, want exact at 5", r.State, r.AtLine)
	}
	if r := bookmark.Resolve(bookmark.Canonical(wt), b); r.State != bookmark.Exact {
		t.Errorf("resolving in the worktree it came from: %v", r.State)
	}
}
