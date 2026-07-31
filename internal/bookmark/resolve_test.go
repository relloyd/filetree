package bookmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file the interesting cases are about: "return nil" and "}" each occur
// twice, which is what makes a single-line anchor unreliable and a block
// anchor worth its disk space.
const sample = `package main

func alpha() {
	if x {
		return nil
	}
}

func beta() {
	if y {
		return nil
	}
}
`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// capture builds a bookmark the way the CLI does, from a file on disk.
func capture(t *testing.T, root, rel string, line int) Bookmark {
	t.Helper()
	ctx, off, err := Capture(filepath.Join(root, rel), line, 2)
	if err != nil {
		t.Fatalf("capture %s:%d: %v", rel, line, err)
	}
	return Bookmark{Path: rel, Line: line, Context: ctx, Offset: off}
}

func TestCaptureTakesTheLineAndItsSurroundings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)

	b := capture(t, root, "code.go", 5) // the first "return nil"

	if got := b.Text(); got != "\t\treturn nil" {
		t.Errorf("Text() = %q", got)
	}
	if len(b.Context) != 5 || b.Offset != 2 {
		t.Errorf("context = %d lines, offset %d; want 5 and 2", len(b.Context), b.Offset)
	}
	if b.Context[0] != "func alpha() {" {
		t.Errorf("context starts at %q", b.Context[0])
	}
}

// A bookmark on line 1 has nothing above it, so the offset is not the middle.
// Getting this wrong puts every such bookmark on the wrong line.
func TestCaptureNearTheStartOfAFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)

	b := capture(t, root, "code.go", 1)

	if b.Offset != 0 {
		t.Errorf("offset = %d, want 0 for a bookmark on line 1", b.Offset)
	}
	if b.Text() != "package main" {
		t.Errorf("Text() = %q", b.Text())
	}
	if r := Resolve(root, b); r.State != Exact || r.AtLine != 1 {
		t.Errorf("resolve = %v at %d, want exact at 1", r.State, r.AtLine)
	}
}

func TestResolveExact(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)
	b := capture(t, root, "code.go", 5)

	r := Resolve(root, b)

	if r.State != Exact || r.AtLine != 5 {
		t.Errorf("state = %v at %d, want exact at 5", r.State, r.AtLine)
	}
	if r.Text != "\t\treturn nil" {
		t.Errorf("text = %q", r.Text)
	}
}

// The whole point of the block anchor: the file gains lines above the
// bookmark, and it follows the *right* "return nil" rather than the one in the
// other function.
func TestResolveFollowsTheBlockPastAnIdenticalLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)
	b := capture(t, root, "code.go", 5)

	writeFile(t, root, "code.go", "// a new header\n// and another\n"+sample)

	r := Resolve(root, b)

	if r.State != Moved {
		t.Fatalf("state = %v, want moved", r.State)
	}
	if r.AtLine != 7 {
		t.Errorf("landed on line %d, want 7 (the first return nil, shifted by 2)", r.AtLine)
	}
}

// Its mirror image: bookmark the *second* occurrence, and it must not snap
// back to the first.
func TestResolveKeepsTheRightOccurrenceOfADuplicateLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)
	b := capture(t, root, "code.go", 11) // the second "return nil"

	writeFile(t, root, "code.go", "// header\n"+sample)

	r := Resolve(root, b)

	if r.State != Moved || r.AtLine != 12 {
		t.Errorf("state = %v at %d, want moved to 12", r.State, r.AtLine)
	}
	// Proof it is the second one: line 6 of the shifted file is the first.
	if r.AtLine == 6 {
		t.Error("followed the wrong occurrence")
	}
}

// When the surroundings are what changed, the block no longer matches — but a
// line that appears once can still be followed.
func TestResolveFallsBackToAUniqueLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", "one\ntwo\nTARGET\nfour\nfive\n")
	b := capture(t, root, "code.go", 3)

	writeFile(t, root, "code.go", "one\nCHANGED\nTARGET\nALSO CHANGED\nfive\n")

	r := Resolve(root, b)

	if r.State != Drifted || r.AtLine != 3 {
		t.Errorf("state = %v at %d, want drifted at 3", r.State, r.AtLine)
	}
}

// ...but not when it would be guessing between two candidates.
func TestResolveRefusesAnAmbiguousLine(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", "one\ntwo\nTARGET\nfour\n")
	b := capture(t, root, "code.go", 3)

	// Both the block and the uniqueness of the line are destroyed.
	writeFile(t, root, "code.go", "TARGET\nX\nY\nZ\nTARGET\n")

	if r := Resolve(root, b); r.State != Lost {
		t.Errorf("state = %v, want lost rather than a guess at line %d", r.State, r.AtLine)
	}
}

func TestResolveMissingFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "code.go", sample)
	b := capture(t, root, "code.go", 5)
	if err := os.Remove(filepath.Join(root, "code.go")); err != nil {
		t.Fatal(err)
	}

	r := Resolve(root, b)

	if r.State != Missing {
		t.Errorf("state = %v, want missing", r.State)
	}
	// The stored anchor is what keeps the row readable — and searchable — when
	// the file is not there.
	if r.Text != "\t\treturn nil" || r.AtLine != 5 {
		t.Errorf("fell back to %q at %d", r.Text, r.AtLine)
	}
	if r.State.Found() {
		t.Error("a missing bookmark should not count as found")
	}
}

// The claim that one repo-relative list serves every worktree.
func TestResolveAgainstADifferentCheckout(t *testing.T) {
	main, worktree := t.TempDir(), t.TempDir()
	writeFile(t, main, "internal/app/code.go", sample)
	writeFile(t, worktree, "internal/app/code.go", "// branch header\n"+sample)

	b := capture(t, main, "internal/app/code.go", 5)

	if r := Resolve(main, b); r.State != Exact || r.AtLine != 5 {
		t.Errorf("main checkout: %v at %d", r.State, r.AtLine)
	}
	if r := Resolve(worktree, b); r.State != Moved || r.AtLine != 6 {
		t.Errorf("worktree: %v at %d, want moved to 6", r.State, r.AtLine)
	}
}

// A file that exists on one branch and not another is the case the retention
// window exists for, so it must resolve as Missing rather than as an error.
func TestResolveWhenTheFileIsOnlyOnOneBranch(t *testing.T) {
	withFile, without := t.TempDir(), t.TempDir()
	writeFile(t, withFile, "code.go", sample)
	b := capture(t, withFile, "code.go", 5)

	if r := Resolve(withFile, b); !r.State.Found() {
		t.Errorf("present: %v", r.State)
	}
	if r := Resolve(without, b); r.State != Missing {
		t.Errorf("absent: %v, want missing", r.State)
	}
}

func TestReadLinesRefusesHugeFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "big.txt", strings.Repeat("x\n", 10))
	// Rewrite it past the cap.
	big := filepath.Join(root, "big.txt")
	if err := os.WriteFile(big, make([]byte, MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readLines(big); err == nil {
		t.Error("a file past the size cap should not be read")
	}
	b := Bookmark{Path: "big.txt", Line: 1, Context: []string{"x"}}
	if r := Resolve(root, b); r.State != Missing {
		t.Errorf("state = %v, want missing for an unreadable file", r.State)
	}
}

func TestCaptureRejectsALineOutsideTheFile(t *testing.T) {
	root := t.TempDir()
	p := writeFile(t, root, "code.go", "one\ntwo\n")
	if _, _, err := Capture(p, 99, 2); err == nil {
		t.Error("capturing past the end of the file should fail")
	}
	if _, _, err := Capture(p, 0, 2); err == nil {
		t.Error("capturing line 0 should fail")
	}
}
