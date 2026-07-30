package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// viewModel is a model rooted in a project directory, with the scratch and
// worktrees views pointed at temp directories of their own. scratchDir and
// worktreesDir only MkdirAll whatever the config names, so the s/w/S keys run
// for real against these.
func viewModel(t *testing.T) (m *Model, project, scratch, worktrees string) {
	t.Helper()
	project = t.TempDir()
	scratch, worktrees = t.TempDir(), t.TempDir()
	m = rootedModel(t, project)
	m.mode = modeNormal
	m.cfg.Scratch.Dir = scratch
	m.cfg.Worktrees.Dir = worktrees
	return m, project, scratch, worktrees
}

func atRoot(t *testing.T, m *Model, want, what string) {
	t.Helper()
	if got := m.tr.Root.Path; got != want {
		t.Fatalf("%s: root is %q, want %q", what, got, want)
	}
}

// The reported bug: "s" then "w" used to overwrite the remembered project root
// with the scratch directory, so Esc returned to scratch and the project was
// unreachable for the rest of the session — the two views bouncing off each
// other with nothing pointing home.
func TestEscFromTheSecondViewReturnsToTheProject(t *testing.T) {
	m, project, scratch, worktrees := viewModel(t)

	m.toggleScratch()
	atRoot(t, m, scratch, "after s")
	m.toggleWorktrees()
	atRoot(t, m, worktrees, "after w")

	m.escKey()

	atRoot(t, m, project, "after esc")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want it spent", m.homeRoot)
	}
}

func TestEscFromOneViewReturnsToTheProject(t *testing.T) {
	m, project, scratch, _ := viewModel(t)

	m.toggleScratch()
	atRoot(t, m, scratch, "after s")
	m.escKey()

	atRoot(t, m, project, "after esc")
}

// The toggle key is the other way out of a view, and has to agree with Esc
// about where "out" is.
func TestTogglingTheSecondViewOffReturnsToTheProject(t *testing.T) {
	m, project, _, worktrees := viewModel(t)

	m.toggleScratch()
	m.toggleWorktrees()
	atRoot(t, m, worktrees, "after w")

	m.toggleWorktrees()

	atRoot(t, m, project, "after toggling worktrees off")
}

// Bouncing between the two siblings any number of times still leaves one way
// home, because the project root is remembered once and never rewritten.
func TestSwitchingBetweenViewsKeepsTheProjectRoot(t *testing.T) {
	m, project, scratch, worktrees := viewModel(t)

	m.toggleScratch()
	m.toggleWorktrees()
	m.toggleScratch()
	atRoot(t, m, scratch, "after s, w, s")
	m.toggleWorktrees()
	atRoot(t, m, worktrees, "after s, w, s, w")

	m.escKey()

	atRoot(t, m, project, "after esc")
}

// "S" is a fourth way into the scratch view and carries the same rule.
func TestScratchNewFromTheWorktreesViewKeepsTheProjectRoot(t *testing.T) {
	m, project, _, worktrees := viewModel(t)
	m.cfg.DefaultCommand = "edit"

	m.toggleWorktrees()
	atRoot(t, m, worktrees, "after w")
	m.scratchNew()

	if m.homeRoot != project {
		t.Errorf("homeRoot = %q, want the project %q", m.homeRoot, project)
	}
}

// Esc with nothing to leave says so. A silent no-op here is what made the
// original bug read as a broken key rather than as a dead end.
func TestEscAtTheProjectRootSaysSo(t *testing.T) {
	m, project, _, _ := viewModel(t)

	m.escKey()

	atRoot(t, m, project, "after esc at the root")
	if !strings.Contains(m.statusMsg, "Already at the project root") {
		t.Errorf("status = %q, want it to say there is nowhere to go", m.statusMsg)
	}
	if m.statusErr {
		t.Error("pressing esc with nothing to leave is not an error")
	}
}

// Esc is layered: marks first, the view second. One press must not do both.
func TestEscClearsMarksBeforeLeavingTheView(t *testing.T) {
	m, project, scratch, _ := viewModel(t)
	if err := os.WriteFile(filepath.Join(scratch, "note.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	m.toggleScratch()
	m.reflatten()
	marked := filepath.Join(scratch, "note.md")
	m.marked[marked] = true
	m.markOrder = append(m.markOrder, marked)

	m.escKey()
	atRoot(t, m, scratch, "first esc")
	if len(m.marked) != 0 {
		t.Fatalf("marks = %v, want them cleared", m.markOrder)
	}

	m.escKey()
	atRoot(t, m, project, "second esc")
}

// A view that fails to load must not leave homeRoot naming the root we are
// still standing in — the next Esc would be a switch to nowhere.
func TestAFailedViewSwitchLeavesNothingToReturnTo(t *testing.T) {
	m, project, _, _ := viewModel(t)
	// A regular file is not a tree root, so loadRoot fails.
	notADir := filepath.Join(project, "file.txt")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	m.enterView(notADir)

	atRoot(t, m, project, "after a failed switch")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want it left empty", m.homeRoot)
	}

	m.escKey()
	atRoot(t, m, project, "esc after a failed switch")
}

// switchToWorktree documents "the original root is remembered once, so Esc
// still returns to the project even after hopping between worktrees" — pin it,
// since nothing did.
func TestWorktreeJumpRemembersTheProjectRootOnce(t *testing.T) {
	m, project, _, worktrees := viewModel(t)
	dest := filepath.Join(worktrees, "repo", "branch")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	m.toggleScratch()
	m.switchToWorktree(dest, "ready")

	atRoot(t, m, worktrees, "after the worktree jump")
	if m.homeRoot != project {
		t.Errorf("homeRoot = %q, want the project %q", m.homeRoot, project)
	}
}
