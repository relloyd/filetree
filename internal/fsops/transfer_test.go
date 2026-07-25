package fsops

import (
	"os"
	"path/filepath"
	"testing"
)

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// recordingTrash removes the path (like the real Trash) and records calls.
func recordingTrash(calls *[]string) func(string) error {
	return func(p string) error {
		*calls = append(*calls, p)
		return os.RemoveAll(p)
	}
}

func noTrash(t *testing.T) func(string) error {
	return func(p string) error {
		t.Errorf("unexpected trash call for %s", p)
		return nil
	}
}

func TestCopyFileDirAndSymlink(t *testing.T) {
	root := t.TempDir()
	src, dstDir := filepath.Join(root, "src"), filepath.Join(root, "dst")
	mkfile(t, filepath.Join(src, "pkg", "main.go"), "package main")
	if err := os.Chmod(filepath.Join(src, "pkg", "main.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.go", filepath.Join(src, "pkg", "link.go")); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(src, "solo.txt"), "solo")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	res := Transfer(OpCopy, []string{filepath.Join(src, "pkg"), filepath.Join(src, "solo.txt")}, dstDir, PolicyNone, noTrash(t))
	if res.Done != 2 || res.Renamed != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := read(t, filepath.Join(dstDir, "pkg", "main.go")); got != "package main" {
		t.Errorf("copied content = %q", got)
	}
	fi, err := os.Stat(filepath.Join(dstDir, "pkg", "main.go"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("mode not preserved: %v %v", fi.Mode(), err)
	}
	if target, err := os.Readlink(filepath.Join(dstDir, "pkg", "link.go")); err != nil || target != "main.go" {
		t.Errorf("symlink not recreated: %q %v", target, err)
	}
	// Sources untouched by copy.
	if _, err := os.Stat(filepath.Join(src, "solo.txt")); err != nil {
		t.Error("copy removed the source")
	}
}

func TestMove(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a", "f.txt"), "hi")
	dst := filepath.Join(root, "b")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res := Transfer(OpMove, []string{filepath.Join(root, "a", "f.txt")}, dst, PolicyNone, noTrash(t))
	if res.Done != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Lstat(filepath.Join(root, "a", "f.txt")); !os.IsNotExist(err) {
		t.Error("source still exists after move")
	}
	if read(t, filepath.Join(dst, "f.txt")) != "hi" {
		t.Error("moved content wrong")
	}
}

func TestKeepBothSuffixChain(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "src", "foo.txt"), "new")
	mkfile(t, filepath.Join(root, "dst", "foo.txt"), "old")
	mkfile(t, filepath.Join(root, "dst", "foo-1.txt"), "older")
	src := []string{filepath.Join(root, "src", "foo.txt")}

	res := Transfer(OpCopy, src, filepath.Join(root, "dst"), PolicyKeepBoth, noTrash(t))
	if res.Renamed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if read(t, filepath.Join(root, "dst", "foo-2.txt")) != "new" {
		t.Error("expected foo-2.txt with new content")
	}
	if read(t, filepath.Join(root, "dst", "foo.txt")) != "old" {
		t.Error("existing file was touched")
	}

	// Directories and dotfiles suffix the whole name.
	if err := os.MkdirAll(filepath.Join(root, "src", "v1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dst", "v1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(root, "src2", ".env"), "x")
	mkfile(t, filepath.Join(root, "dst", ".env"), "y")
	res = Transfer(OpCopy,
		[]string{filepath.Join(root, "src", "v1.0"), filepath.Join(root, "src2", ".env")},
		filepath.Join(root, "dst"), PolicyKeepBoth, noTrash(t))
	if res.Renamed != 2 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "dst", "v1.0-1")); err != nil {
		t.Error("dir should arrive as v1.0-1")
	}
	if read(t, filepath.Join(root, "dst", ".env-1")) != "x" {
		t.Error("dotfile should arrive as .env-1")
	}
}

func TestOverwriteGoesViaTrash(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "src", "foo.txt"), "new")
	mkfile(t, filepath.Join(root, "dst", "foo.txt"), "old")
	var calls []string
	res := Transfer(OpCopy, []string{filepath.Join(root, "src", "foo.txt")}, filepath.Join(root, "dst"), PolicyOverwrite, recordingTrash(&calls))
	if res.Done != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(calls) != 1 || calls[0] != filepath.Join(root, "dst", "foo.txt") {
		t.Errorf("trash calls = %v", calls)
	}
	if read(t, filepath.Join(root, "dst", "foo.txt")) != "new" {
		t.Error("destination not replaced")
	}
}

func TestPolicyNoneMidRunConflictKeepsBoth(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "a", "same.txt"), "from-a")
	mkfile(t, filepath.Join(root, "b", "same.txt"), "from-b")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res := Transfer(OpCopy,
		[]string{filepath.Join(root, "a", "same.txt"), filepath.Join(root, "b", "same.txt")},
		dst, PolicyNone, noTrash(t))
	if res.Done != 1 || res.Renamed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if read(t, filepath.Join(dst, "same.txt")) != "from-a" || read(t, filepath.Join(dst, "same-1.txt")) != "from-b" {
		t.Error("same-basename pair not kept-both")
	}
}

func TestCopyIntoOwnDirNeverTrashesSource(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "foo.txt"), "orig")
	// Overwrite policy chosen, but dest == src: must keep both instead.
	res := Transfer(OpCopy, []string{filepath.Join(root, "foo.txt")}, root, PolicyOverwrite, noTrash(t))
	if res.Renamed != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if read(t, filepath.Join(root, "foo.txt")) != "orig" || read(t, filepath.Join(root, "foo-1.txt")) != "orig" {
		t.Error("expected original intact plus foo-1.txt duplicate")
	}
}

func TestMoveAlreadyInPlaceSkips(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "foo.txt"), "orig")
	res := Transfer(OpMove, []string{filepath.Join(root, "foo.txt")}, root, PolicyOverwrite, noTrash(t))
	if res.Skipped != 1 || res.Done != 0 {
		t.Fatalf("result = %+v", res)
	}
	if read(t, filepath.Join(root, "foo.txt")) != "orig" {
		t.Error("in-place move touched the file")
	}
}

func TestDirIntoItselfRefused(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "dir", "sub", "f.txt"), "x")
	res := Transfer(OpMove, []string{filepath.Join(root, "dir")}, filepath.Join(root, "dir", "sub"), PolicyNone, noTrash(t))
	if len(res.Errors) != 1 {
		t.Fatalf("expected refusal, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "dir", "sub", "f.txt")); err != nil {
		t.Error("tree was modified by refused transfer")
	}
}

func TestMissingSourceSkipped(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	res := Transfer(OpCopy, []string{filepath.Join(root, "gone.txt")}, dst, PolicyNone, noTrash(t))
	if res.Skipped != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestSummary(t *testing.T) {
	r := Result{Done: 2, Renamed: 1, Skipped: 1}
	if got := r.Summary(OpMove); got != "Moved 3, kept both for 1, skipped 1" {
		t.Errorf("summary = %q", got)
	}
}
