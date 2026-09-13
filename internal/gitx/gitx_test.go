package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nul(parts ...string) []byte {
	return []byte(strings.Join(parts, "\x00") + "\x00")
}

func TestParseStatus(t *testing.T) {
	out := nul(
		" M internal/app/model.go",      // modified, unstaged
		"M  cmd/ft/main.go",             // staged
		"MM internal/tree/tree.go",      // staged with further unstaged edits
		"?? notes.txt",                  // untracked file
		"?? scratch/",                   // untracked dir
		"!! bin/",                       // ignored dir
		"!! coverage.out",               // ignored file
		"R  new_name.go", "old_name.go", // rename: source follows in next token
		"UU conflicted.go",
	)
	rs := parseStatus("/repo", out)

	cases := []struct {
		rel   string
		isDir bool
		want  Code
	}{
		{"internal/app/model.go", false, Modified},
		{"cmd/ft/main.go", false, Staged},
		{"internal/tree/tree.go", false, Modified},
		{"notes.txt", false, Untracked},
		{"scratch", true, Untracked},
		{"scratch/inner.txt", false, Untracked}, // inherited from untracked dir
		{"bin", true, Ignored},
		{"bin/ft", false, Ignored}, // inherited from ignored dir
		{"coverage.out", false, Ignored},
		{"new_name.go", false, Staged},
		{"old_name.go", false, None}, // rename source is not an entry
		{"conflicted.go", false, Conflict},
		{"untouched.go", false, None},
	}
	for _, c := range cases {
		if got := rs.CodeFor(c.rel, c.isDir); got != c.want {
			t.Errorf("CodeFor(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}

	for _, dir := range []string{"internal/app", "internal", "cmd/ft", "."} {
		if !rs.DirContainsChanges(dir) {
			t.Errorf("DirContainsChanges(%q) = false, want true", dir)
		}
	}
	if rs.DirContainsChanges("bin") {
		t.Error("ignored entries must not mark ancestors as changed")
	}
}

func TestNilStatusIsSafe(t *testing.T) {
	var rs *RepoStatus
	if rs.CodeFor("x", false) != None || rs.DirContainsChanges("x") {
		t.Error("nil RepoStatus should report no status")
	}
}

// TestReadStatusLeavesTheIndexAlone guards --no-optional-locks. ft re-reads
// status in the background whenever a watched directory changes, and a plain
// `git status` takes .git/index.lock to write refreshed stat data back — on
// every run, measured — so a lazygit commit landing in that window fails with
// "index.lock: File exists". The index being replaced is the visible half of
// taking that lock: git writes index.lock and renames it over the index.
func TestReadStatusLeavesTheIndexAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// The user's own config must not decide the outcome: an fsmonitor or an
	// untracked cache changes when git writes the index.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	repo := t.TempDir()
	file := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(file, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "a.txt"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Stale stat data in the index is the case where a status allowed to
	// write certainly will.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}

	index := filepath.Join(repo, ".git", "index")
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStatus(repo); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("ReadStatus rewrote .git/index, so it took index.lock; git must run with --no-optional-locks")
	}
}

func TestFindRepoRoot(t *testing.T) {
	// This test file lives inside the filetree repo, so walking up from the
	// package dir must find a root whose .git exists.
	root := FindRepoRoot(".")
	if root == "" {
		t.Skip("not running inside a git repo")
	}
	if FindRepoRoot("/") != "" {
		t.Error("/ should not be inside a repo")
	}
}
