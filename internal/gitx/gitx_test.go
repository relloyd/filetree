package gitx

import (
	"strings"
	"testing"
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
