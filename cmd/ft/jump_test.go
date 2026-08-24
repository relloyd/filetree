package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relloyd/filetree/internal/ipc"
)

func TestRunJumpUsage(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{nil, {}, {"a", "b"}} {
		if err := runJump(dir, args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("runJump(%v) err = %v, want a usage error", args, err)
		}
	}
}

// Nothing listening is the ordinary state before the first ft starts, and the
// message has to say so rather than blaming the path.
func TestRunJumpWithNothingRunning(t *testing.T) {
	err := runJump(t.TempDir(), []string{"whatever.go"})
	if err == nil {
		t.Fatal("runJump with no instances succeeded")
	}
	if !strings.Contains(err.Error(), "no filetree is running") {
		t.Errorf("err = %v", err)
	}
}

func TestChooseExactMatch(t *testing.T) {
	cands := []ipc.Candidate{{PID: 7, Root: "/a/proj"}}
	win, path, ok := choose(cands, nil, ipc.Location{}, "/a/proj/x.go")
	if !ok {
		t.Fatal("choose() not ok")
	}
	if win.PID != 7 {
		t.Errorf("pid = %d, want 7", win.PID)
	}
	if path != "/a/proj/x.go" {
		t.Errorf("path = %q, want it sent unchanged", path)
	}
}

func TestChooseNoMatch(t *testing.T) {
	cands := []ipc.Candidate{{PID: 7, Root: "/a/proj"}}
	if _, _, ok := choose(cands, nil, ipc.Location{}, "/etc/hosts"); ok {
		t.Error("choose() matched an unrelated path")
	}
}

// The macOS /tmp case: the instance was given the unresolved spelling of its
// root and the editor reports the resolved one. They have to find each other,
// and the path that goes over the wire has to be in the instance's own terms —
// its tree only knows the root it was handed.
func TestChooseMatchesThroughASymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(real, "sub", "x.go")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The instance is rooted through the link; the editor reports the real
	// path. A straight comparison finds nothing.
	cands := []ipc.Candidate{{PID: 7, Root: link}}
	if _, ok := ipc.Pick(cands, nil, ipc.Location{}, file); ok {
		t.Fatal("Pick matched without resolving — this test proves nothing")
	}

	win, path, ok := choose(cands, nil, ipc.Location{}, file)
	if !ok {
		t.Fatal("choose() did not match through the symlink")
	}
	if win.PID != 7 {
		t.Errorf("pid = %d, want 7", win.PID)
	}
	if want := filepath.Join(link, "sub", "x.go"); path != want {
		t.Errorf("path = %q, want %q — rebuilt in the instance's own terms", path, want)
	}
}

// The mirror image: the instance holds the resolved root and the editor
// reports the linked spelling.
func TestChooseMatchesASymlinkedTarget(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "sub", "x.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cands := []ipc.Candidate{{PID: 7, Root: real}}
	win, path, ok := choose(cands, nil, ipc.Location{}, filepath.Join(link, "sub", "x.go"))
	if !ok {
		t.Fatal("choose() did not match a linked target")
	}
	if win.PID != 7 {
		t.Errorf("pid = %d, want 7", win.PID)
	}
	if want := filepath.Join(real, "sub", "x.go"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// No tmux at all must leave routing to containment rather than failing.
func TestPaneLayoutWithoutACallerPane(t *testing.T) {
	panes, caller := paneLayout("")
	if caller != (ipc.Location{}) {
		t.Errorf("caller = %+v, want the zero location", caller)
	}
	_ = panes // may or may not be populated depending on the test host
}
