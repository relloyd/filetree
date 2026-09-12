package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relloyd/filetree/internal/herdr"
)

// lazygit differs from the editor in exactly two ways, and both are worth
// pinning: it takes no paths, and it refuses outside a checkout.
func TestLazygitProgramShape(t *testing.T) {
	if lazygit.Argv0 != "lazygit" {
		t.Errorf("Argv0 = %q, want lazygit", lazygit.Argv0)
	}
	if got := lazygit.Command(nil); got != "lazygit" {
		t.Errorf("Command(nil) = %q, want a bare lazygit", got)
	}
	// Paths are ignored rather than appended: lazygit shows a repository, and a
	// file name on the end would be a different program's idea.
	if got := lazygit.Command([]string{"/a/one.go", "/a/two.go"}); got != "lazygit" {
		t.Errorf("Command(paths) = %q, want the paths ignored", got)
	}
	if lazygit.Reuse != nil {
		t.Error("Reuse is set; one already open for this checkout is already showing it")
	}
	if !lazygit.NeedsRepo {
		t.Error("NeedsRepo = false; outside a checkout there is nothing to show")
	}
}

// Outside a checkout the key refuses rather than opening a tab that would only
// hold an error. It has to refuse before touching herdr, so a machine with no
// server still gets the useful message.
func TestLazygitRefusesOutsideACheckout(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftlg")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	loose := filepath.Join(base, "loose")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(base, "absent.sock"))

	err = runHerdrLazygit([]string{loose})
	if err == nil {
		t.Fatal("a loose directory was accepted, want a refusal")
	}
	if !strings.Contains(err.Error(), "needs a git repo") {
		t.Errorf("error = %q, want it to name the reason", err)
	}
	if strings.Contains(err.Error(), "herdr is not running") {
		t.Error("the refusal came from herdr; it should be decided before dialling")
	}
}

// Inside a checkout it gets as far as herdr, which here is not running.
func TestLazygitReachesHerdrInsideACheckout(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftlg")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	repo := repoAt(t, filepath.Join(base, "proj"))
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(base, "absent.sock"))

	err = runHerdrLazygit([]string{repo})
	if err == nil || !strings.Contains(err.Error(), "herdr is not running") {
		t.Errorf("error = %v, want it to have got past the repo check to herdr", err)
	}
}

// The reuse branch for a program with no Reuse hook must focus and send nothing.
// Sending a stray keystroke to a running lazygit would act on whatever it has
// selected, which is a destructive way to find a bug.
func TestLazygitReuseOnlyFocuses(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftlg")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	repo := repoAt(t, filepath.Join(base, "proj"))

	panes := []map[string]any{
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo},
	}
	procs := map[string][]map[string]any{
		"w1:p1": {{"name": "lazygit", "argv0": "lazygit", "pid": 300}},
	}
	sock, rec := serveHerdrProcs(t, panes, nil, procs)
	t.Setenv("HERDR_SOCKET_PATH", sock)

	if err := runHerdrLazygit([]string{repo}); err != nil {
		t.Fatalf("runHerdrLazygit: %v", err)
	}

	for _, m := range rec.methods() {
		if strings.HasPrefix(m, "pane.send") {
			t.Errorf("%s was called; a running lazygit must only be focused", m)
		}
		if m == "tab.create" || m == "workspace.create" {
			t.Errorf("%s was called; the running lazygit should have been reused", m)
		}
	}
	if !contains(rec.methods(), "pane.focus") {
		t.Errorf("calls = %v, want the running lazygit focused", rec.methods())
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func TestHerdrLazygitArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"the subcommand form", []string{"herdr-lazygit", "/a"}, true},
		{"a directory of that name still opens a tree", []string{"herdr-lazygit"}, false},
		{"too many arguments", []string{"herdr-lazygit", "/a", "/b"}, false},
		{"something else entirely", []string{"jump", "/a"}, false},
	}
	for _, tc := range tests {
		if got := herdrLazygitArgs(tc.args); got != tc.want {
			t.Errorf("%s: herdrLazygitArgs(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

// The scoping is shared, so a lazygit open for one checkout must not answer for
// another — the worktree case, in the key most likely to be pressed in one.
func TestLazygitDoesNotCrossCheckouts(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftlg")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	main := repoAt(t, filepath.Join(base, "proj"))
	tree := repoAt(t, filepath.Join(base, "worktrees", "proj", "feature"))

	panes := []map[string]any{
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": main},
	}
	procs := map[string][]map[string]any{
		"w1:p1": {{"name": "lazygit", "argv0": "lazygit", "pid": 300}},
	}
	sock, _ := serveHerdrProcs(t, panes, nil, procs)
	c := herdr.New(sock)
	all, _ := c.Panes()

	scoped := inScope(all, herdr.ScopeFor(tree, tree), "", "")
	if got := shellIDs(running(c, scoped, lazygitBin)); len(got) != 0 {
		t.Errorf("running(lazygit) in the worktree = %v, want nothing from the main checkout", got)
	}
	scoped = inScope(all, herdr.ScopeFor(main, main), "", "")
	if got := shellIDs(running(c, scoped, lazygitBin)); !equal(got, []string{"w1:p1"}) {
		t.Errorf("running(lazygit) in the main checkout = %v, want w1:p1", got)
	}
}
