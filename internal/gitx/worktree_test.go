package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn returns a runner for git commands in dir that fails the test on
// error, with a fixed identity so commits work on any machine.
func gitIn(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// newRepo creates a repo with one commit on main and returns its path.
func newRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := gitIn(t, dir)
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-m", "init")
	return dir
}

func worktreeCount(t *testing.T, repo string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	return strings.Count(string(out), "worktree ")
}

func TestWorktreeLifecycle(t *testing.T) {
	base := t.TempDir()
	repo := newRepo(t, filepath.Join(base, "repo"))

	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsLinkedWorktree(repo) {
		t.Error("the main repo is not a linked worktree")
	}
	if IsLinkedWorktree(plain) {
		t.Error("a plain directory is not a linked worktree")
	}

	wt := filepath.Join(base, "wt-feature")
	if err := WorktreeAdd(repo, wt, "feature", true); err != nil {
		t.Fatalf("WorktreeAdd -b: %v", err)
	}
	if !IsLinkedWorktree(wt) {
		t.Error("created worktree not detected as linked")
	}
	if !HasRef(repo, "feature") {
		t.Error("new branch not created")
	}
	main, err := MainRepoOf(wt)
	if err != nil {
		t.Fatalf("MainRepoOf: %v", err)
	}
	if resolve(t, main) != resolve(t, repo) {
		t.Errorf("MainRepoOf = %q, want %q", main, repo)
	}

	// Existing ref (no -b): the main branch checked out a second time is
	// refused by git, so use the branch created above's start point.
	wt2 := filepath.Join(base, "wt-other")
	if err := WorktreeAdd(repo, wt2, "other", true); err != nil {
		t.Fatalf("WorktreeAdd second: %v", err)
	}

	before := worktreeCount(t, repo)
	if err := WorktreeRemove(repo, wt, false); err != nil {
		t.Fatalf("WorktreeRemove clean: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present: %v", err)
	}
	if after := worktreeCount(t, repo); after != before-1 {
		t.Errorf("worktree count = %d, want %d", after, before-1)
	}

	// A dirty worktree needs --force.
	if err := os.WriteFile(filepath.Join(wt2, "untracked.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = WorktreeRemove(repo, wt2, false)
	if err == nil {
		t.Fatal("removing a dirty worktree should fail")
	}
	if !IsDirtyWorktreeError(err) {
		t.Errorf("IsDirtyWorktreeError(%v) = false", err)
	}
	if err := WorktreeRemove(repo, wt2, true); err != nil {
		t.Fatalf("forced WorktreeRemove: %v", err)
	}
	if _, err := os.Stat(wt2); !os.IsNotExist(err) {
		t.Errorf("forced removal left the dir: %v", err)
	}
}

// TestWorktreeAddExistingRef covers the plain (no -b) add against a branch
// that already exists locally.
func TestWorktreeAddExistingRef(t *testing.T) {
	base := t.TempDir()
	repo := newRepo(t, filepath.Join(base, "repo"))
	gitIn(t, repo)("branch", "topic")

	if !HasRef(repo, "topic") {
		t.Fatal("topic branch missing")
	}
	if HasRef(repo, "nope") {
		t.Error("HasRef should be false for an unknown branch")
	}

	wt := filepath.Join(base, "wt-topic")
	if err := WorktreeAdd(repo, wt, "topic", false); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if !IsLinkedWorktree(wt) {
		t.Error("worktree not linked")
	}
	if err := WorktreeAdd(repo, filepath.Join(base, "again"), "topic", false); err == nil {
		t.Error("checking a branch out twice should fail")
	}
}

// TestFetchPR uses a local bare "origin" holding a refs/pull/N/head ref, so
// the PR path is exercised without a network.
func TestFetchPR(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	repo := newRepo(t, filepath.Join(base, "repo"))
	run := gitIn(t, repo)
	run("remote", "add", "origin", origin)
	run("push", "origin", "HEAD:refs/pull/7/head")

	branch, err := FetchPR(repo, 7)
	if err != nil {
		t.Fatalf("FetchPR: %v", err)
	}
	if branch != "pr-7" {
		t.Errorf("branch = %q, want pr-7", branch)
	}
	if !HasRef(repo, "pr-7") {
		t.Error("pr-7 branch not created")
	}

	// Re-fetching the same PR stays a success (already-there is fine).
	if branch, err = FetchPR(repo, 7); err != nil || branch != "pr-7" {
		t.Errorf("second FetchPR = %q, %v", branch, err)
	}

	wt := filepath.Join(base, "wt-pr7")
	if err := WorktreeAdd(repo, wt, "pr-7", false); err != nil {
		t.Fatalf("WorktreeAdd pr-7: %v", err)
	}
	// With pr-7 checked out, a fetch that would update it fails; FetchPR
	// still reports the existing branch.
	if branch, err = FetchPR(repo, 7); err != nil || branch != "pr-7" {
		t.Errorf("FetchPR with pr-7 checked out = %q, %v", branch, err)
	}

	// A PR the origin does not have is a real error.
	if _, err := FetchPR(repo, 9); err == nil {
		t.Error("fetching a missing PR should fail")
	}
}

func TestFetchBranch(t *testing.T) {
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	upstream := newRepo(t, filepath.Join(base, "upstream"))
	up := gitIn(t, upstream)
	up("remote", "add", "origin", origin)
	up("checkout", "-b", "shared")
	up("push", "origin", "shared")

	repo := newRepo(t, filepath.Join(base, "repo"))
	gitIn(t, repo)("remote", "add", "origin", origin)
	if HasRef(repo, "shared") {
		t.Fatal("shared should not exist locally yet")
	}
	if err := FetchBranch(repo, "shared"); err != nil {
		t.Fatalf("FetchBranch: %v", err)
	}
	if !HasRef(repo, "shared") {
		t.Error("FetchBranch did not create the local branch")
	}
	if err := FetchBranch(repo, "absent"); err == nil {
		t.Error("fetching a missing branch should fail")
	}
}

func TestParseWorktreeInput(t *testing.T) {
	cases := []struct {
		in     string
		pr     int
		branch string
	}{
		{"123", 123, ""},
		{"#123", 123, ""},
		{"pr/123", 123, ""},
		{"PR123", 123, ""},
		{"pr-7", 7, ""},
		{" 42 ", 42, ""},
		{"main", 0, "main"},
		{"feature/x", 0, "feature/x"},
		{"prod", 0, "prod"},
		{"pr", 0, "pr"},
		{"123abc", 0, "123abc"},
		{"release-2", 0, "release-2"},
		{"0", 0, "0"},
		{"", 0, ""},
	}
	for _, c := range cases {
		pr, branch := ParseWorktreeInput(c.in)
		if pr != c.pr || branch != c.branch {
			t.Errorf("ParseWorktreeInput(%q) = %d,%q want %d,%q", c.in, pr, branch, c.pr, c.branch)
		}
	}
}

func TestWorktreeDirName(t *testing.T) {
	for in, want := range map[string]string{
		"feature/x":       "feature-x",
		"main":            "main",
		"a/b/c":           "a-b-c",
		"#12":             "pr-12",
		"pr/12":           "pr-12",
		"77":              "pr-77",
		"v1.0":            "v1.0",
		"..":              "",
		".":               "",
		"../../escape":    "..-..-escape",
		" spaced/branch ": "spaced-branch",
	} {
		if got := WorktreeDirName(in); got != want {
			t.Errorf("WorktreeDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func resolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}
