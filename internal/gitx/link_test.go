package gitx

import (
	"os/exec"
	"testing"
)

func TestWebURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"git@github.com:user/repo.git", "https://github.com/user/repo", true},
		{"git@github.com:user/repo", "https://github.com/user/repo", true},
		{"ssh://git@github.com/user/repo.git", "https://github.com/user/repo", true},
		{"ssh://git@git.corp.example:2222/team/repo.git", "https://git.corp.example/team/repo", true},
		{"https://github.com/user/repo.git", "https://github.com/user/repo", true},
		{"https://tok@github.example.com:8443/user/repo", "https://github.example.com:8443/user/repo", true},
		{"git://github.com/user/repo.git", "https://github.com/user/repo", true},
		{"git@gitlab.com:group/sub/repo.git", "https://gitlab.com/group/sub/repo", true},
		{"/local/bare/repo.git", "", false},
		{"file:///x/y", "", false},
		{"", "", false},
		{"https://github.com/", "", false},
	}
	for _, c := range cases {
		got, ok := WebURL(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("WebURL(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFileURL(t *testing.T) {
	base, ref := "https://github.com/u/r", "abc123"
	cases := []struct {
		rel   string
		isDir bool
		want  string
	}{
		{"cmd/ft/main.go", false, base + "/blob/abc123/cmd/ft/main.go"},
		{"internal/app", true, base + "/tree/abc123/internal/app"},
		{".", true, base + "/tree/abc123"},
		{"sp ace.txt", false, base + "/blob/abc123/sp%20ace.txt"},
		{"a#b/c.txt", false, base + "/blob/abc123/a%23b/c.txt"},
	}
	for _, c := range cases {
		if got := FileURL(base, ref, c.rel, c.isDir); got != c.want {
			t.Errorf("FileURL(%q, dir=%v) = %q, want %q", c.rel, c.isDir, got, c.want)
		}
	}
}

func TestHeadRef(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "x")

	hash, err := HeadRef(dir, false)
	if err != nil || len(hash) != 40 {
		t.Fatalf("commit ref = %q, %v", hash, err)
	}
	branch, err := HeadRef(dir, true)
	if err != nil || branch != "main" {
		t.Errorf("branch ref = %q, %v", branch, err)
	}

	// Detached HEAD falls back to the hash even in branch mode.
	run("checkout", "--detach")
	ref, err := HeadRef(dir, true)
	if err != nil || ref != hash {
		t.Errorf("detached ref = %q, %v (want hash %q)", ref, err, hash)
	}
}
