package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/state"
)

// rootsModel is rootModel as a freshly started ft: the launch root is already
// in its own history, which loadRoot does at startup and the fixture skips.
func rootsModel(t *testing.T) (m *Model, project string) {
	t.Helper()
	m, project = rootModel(t)
	m.rootsInput.Focus()
	m.recordRoot()
	return m, project
}

// openRoots presses "<" and returns the rows it offers, top first.
func openRoots(t *testing.T, m *Model) []string {
	t.Helper()
	pressRune(t, m, '<')
	if m.mode != modeFuzzy || m.finderSrc != srcRoots {
		t.Fatalf("< opened mode %v source %v, want the roots view", m.mode, m.finderSrc)
	}
	var out []string
	for i := range m.rootsRows {
		r, _ := m.rootRow(i)
		out = append(out, r.Path)
	}
	return out
}

// rootHereAt roots the tree at dir with ">", the way the user would.
func rootHereAt(t *testing.T, m *Model, dir string) {
	t.Helper()
	selectPathOrFail(t, m, dir)
	pressRune(t, m, '>')
	atRoot(t, m, dir, "after > on "+dir)
}

func pressEnter(m *Model) tea.Cmd {
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

// The point of the list: from a subtree, the first row — the one the cursor
// starts on — is where you were before, so "<" enter is the way back, and the
// same two keys from there are the way in again. Alt-tab, for roots.
func TestRecentRootsFlipBetweenTheLastTwo(t *testing.T) {
	m, project := rootsModel(t)
	sub := filepath.Join(project, "sub")
	rootHereAt(t, m, sub)

	if got := openRoots(t, m); len(got) != 1 || got[0] != project {
		t.Fatalf("from the subtree < offers %v, want just the project", got)
	}
	pressEnter(m)
	atRoot(t, m, project, "after < enter from the subtree")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want the tree back", m.mode)
	}
	// Picking the launch root is Esc: there is nothing left to return to. Going
	// through enterView instead would leave homeRoot naming the root we are
	// standing on.
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q after returning to the launch root, want it spent", m.homeRoot)
	}

	if got := openRoots(t, m); len(got) != 1 || got[0] != sub {
		t.Fatalf("from the project < offers %v, want just the subtree", got)
	}
	pressEnter(m)
	atRoot(t, m, sub, "after < enter from the project")
	// A detour like ">": one Esc still comes home.
	if m.homeRoot != project {
		t.Errorf("homeRoot = %q, want the project to come home to", m.homeRoot)
	}
	m.escKey()
	atRoot(t, m, project, "after esc")
}

// The current root is never offered — picking it would do nothing — and the
// rest are most recent first.
func TestRecentRootsAreNewestFirstWithoutTheCurrent(t *testing.T) {
	m, project := rootsModel(t)
	sub := filepath.Join(project, "sub")
	deep := filepath.Join(sub, "a")
	rootHereAt(t, m, sub)
	rootHereAt(t, m, deep)

	got := openRoots(t, m)
	want := []string{sub, project}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("< offers %v, want %v", got, want)
	}
	// The launch root is a row like any other, not pinned to the top — but it
	// is still the one Esc goes to, so picking it is Esc.
	m.moveFuzzySel(1)
	pressEnter(m)
	atRoot(t, m, project, "after picking the launch root")
	if m.homeRoot != "" {
		t.Errorf("homeRoot = %q, want it spent", m.homeRoot)
	}
}

// Rows inside the launch root read as paths from it; the launch root itself is
// marked, since picking it is a return rather than a detour.
func TestRecentRootsLabelsAndQuery(t *testing.T) {
	m, project := rootsModel(t)
	rootHereAt(t, m, filepath.Join(project, "sub"))
	m.escKey()
	rootHereAt(t, m, filepath.Join(project, "other"))

	openRoots(t, m)
	var labels []string
	for i := range m.rootsRows {
		r, _ := m.rootRow(i)
		labels = append(labels, r.label)
		if r.home != (r.Path == project) {
			t.Errorf("row %q home = %v", r.label, r.home)
		}
	}
	if len(labels) != 2 || labels[1] != "sub" {
		t.Fatalf("labels = %v, want the project then \"sub\"", labels)
	}

	for _, r := range "sub" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(m.rootsRows) != 1 {
		t.Fatalf("query \"sub\" left %d rows, want 1", len(m.rootsRows))
	}
	pressEnter(m)
	atRoot(t, m, filepath.Join(project, "sub"), "after filtering and picking")
}

// The query is emptied each time: a leftover filter would hide the row the
// switcher exists to put first.
func TestRecentRootsStartWithAnEmptyQuery(t *testing.T) {
	m, project := rootsModel(t)
	rootHereAt(t, m, filepath.Join(project, "sub"))
	openRoots(t, m)
	m.handleKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if got := openRoots(t, m); len(got) != 1 || m.rootsInput.Value() != "" {
		t.Errorf("reopened with query %q and rows %v, want it empty and the project offered", m.rootsInput.Value(), got)
	}
}

// The scratch and worktrees views have their own keys, and the worktrees view
// in particular is passed through on the way to every checkout. A checkout
// rooted into from it with ">" is what gets remembered — and Esc from there
// still goes to the project, not back to the worktrees view.
func TestRecentRootsSkipTheViewsButKeepACheckout(t *testing.T) {
	m, project, scratch, worktrees := viewModel(t)
	m.buildBindings()
	m.rootsInput.Focus()
	m.recordRoot()
	checkout := filepath.Join(worktrees, "repo", "my-branch")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}

	m.toggleScratch()
	atRoot(t, m, scratch, "after s")
	m.escKey()
	m.toggleWorktrees()
	atRoot(t, m, worktrees, "after w")
	rootHereAt(t, m, checkout)

	if got := openRoots(t, m); len(got) != 1 || got[0] != project {
		t.Fatalf("from the checkout < offers %v, want just the project", got)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.escKey()
	atRoot(t, m, project, "after esc from the checkout")

	if got := openRoots(t, m); len(got) != 1 || got[0] != checkout {
		t.Fatalf("from the project < offers %v, want just the checkout", got)
	}
}

// A root whose directory has gone is not offered, but it is not forgotten
// either: a branch switch can take a directory away and bring it back.
func TestRecentRootsHideAMissingDirButKeepIt(t *testing.T) {
	m, project := rootsModel(t)
	other := filepath.Join(project, "other")
	rootHereAt(t, m, other)
	m.escKey()

	moved := other + ".away"
	if err := os.Rename(other, moved); err != nil {
		t.Fatal(err)
	}
	if got := openRoots(t, m); len(got) != 0 {
		t.Errorf("with the dir gone < offers %v, want nothing", got)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	if err := os.Rename(moved, other); err != nil {
		t.Fatal(err)
	}
	if got := openRoots(t, m); len(got) != 1 || got[0] != other {
		t.Errorf("with the dir back < offers %v, want it again", got)
	}
}

// ctrl+x forgets for good — it must not be merged straight back in from disk.
func TestRecentRootsForget(t *testing.T) {
	m, project := rootsModel(t)
	rootHereAt(t, m, filepath.Join(project, "sub"))
	m.escKey()
	rootHereAt(t, m, filepath.Join(project, "other"))
	m.escKey()

	if got := openRoots(t, m); len(got) != 2 {
		t.Fatalf("< offers %v, want two subtrees", got)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if m.mode != modeFuzzy {
		t.Fatalf("mode = %v, want to stay in the list after forgetting", m.mode)
	}
	if len(m.rootsRows) != 1 {
		t.Fatalf("after ctrl+x %d rows, want 1", len(m.rootsRows))
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := openRoots(t, m); len(got) != 1 || got[0] != filepath.Join(project, "sub") {
		t.Errorf("reopened: %v, want only sub left", got)
	}
}

// A removed worktree is gone for good, so it and anything rooted beneath it
// leave the history at once rather than lingering for the retention window.
func TestForgetRootsUnderDropsTheSubtree(t *testing.T) {
	m, project := rootsModel(t)
	r := state.LoadRoots(m.stateDir, project)
	for _, p := range []string{"/wt/x", "/wt/x/deep", "/wt/xy", "/elsewhere"} {
		r.Visit(p, r.Roots[0].At, 20)
	}
	if err := r.Overwrite(m.stateDir); err != nil {
		t.Fatal(err)
	}
	m.forgetRootsUnder("/wt/x")

	var left []string
	for _, e := range state.LoadRoots(m.stateDir, project).Roots {
		left = append(left, e.Path)
	}
	got := strings.Join(left, ",")
	if strings.Contains(got, "/wt/x,") || strings.Contains(got, "/wt/x/deep") || !strings.Contains(got, "/wt/xy") || !strings.Contains(got, "/elsewhere") {
		t.Errorf("left %v, want /wt/x and its subtree gone and its sibling kept", left)
	}
}

// Nothing remembered yet is the ordinary state on a first run, and it says so
// rather than showing an empty list that looks broken.
func TestRecentRootsEmptySaysSo(t *testing.T) {
	m, _ := rootsModel(t)
	if got := openRoots(t, m); len(got) != 0 {
		t.Fatalf("< on a fresh start offers %v, want nothing", got)
	}
	if !strings.Contains(m.rootsStatusNote(), "none yet") {
		t.Errorf("note = %q, want it to say there is nothing yet", m.rootsStatusNote())
	}
	pressEnter(m) // an empty list is not an error
	if m.statusErr {
		t.Errorf("enter on an empty list reported %q", m.statusMsg)
	}
}

// seedRoot writes one entry into the launch root's history directly, with the
// sighting a test needs — which no key press can produce.
func seedRoot(t *testing.T, m *Model, home, path string, lastSeen time.Time) {
	t.Helper()
	r := state.LoadRoots(m.stateDir, home)
	r.Roots = append(r.Roots, state.RecentRoot{Path: path, At: lastSeen, LastSeen: lastSeen})
	if err := r.Overwrite(m.stateDir); err != nil {
		t.Fatal(err)
	}
}

func historyHas(m *Model, home, path string) (state.RecentRoot, bool) {
	for _, e := range state.LoadRoots(m.stateDir, home).Roots {
		if e.Path == path {
			return e, true
		}
	}
	return state.RecentRoot{}, false
}

// A re-root sees only the root it lands on, so it must not expire anything:
// judging the rest by a sighting only "<" refreshes would empty the list after
// a month of not opening "<", directories and all still there. Expiry belongs
// to opening "<", which has just looked for every entry.
func TestRecentRootsExpireOnlyWhenEveryEntryWasLookedFor(t *testing.T) {
	m, project := rootsModel(t)
	m.cfg.General.RecentRootsRetentionDays = config.DefaultRecentRootsRetentionDays
	other := filepath.Join(project, "other")
	gone := filepath.Join(project, "no-such-dir")
	longAgo := time.Now().Add(-40 * 24 * time.Hour)
	seedRoot(t, m, project, other, longAgo)
	seedRoot(t, m, project, gone, longAgo)

	rootHereAt(t, m, filepath.Join(project, "sub"))
	if _, ok := historyHas(m, project, other); !ok {
		t.Fatal("a re-root expired a root whose directory exists")
	}
	if _, ok := historyHas(m, project, gone); !ok {
		t.Fatal("a re-root expired a root it never looked for")
	}

	got := openRoots(t, m)
	if !slices.Contains(got, other) || slices.Contains(got, gone) {
		t.Errorf("< offers %v, want the existing root and not the missing one", got)
	}
	if e, ok := historyHas(m, project, other); !ok || time.Since(e.LastSeen) > time.Minute {
		t.Errorf("existing root after < = %+v (kept %v), want its sighting refreshed", e, ok)
	}
	if _, ok := historyHas(m, project, gone); ok {
		t.Error("a root missing for longer than the window survived opening <")
	}
}

// Opening "<" is a switch, and a switch with nothing new to record must not
// rewrite the file. A rename is what a write is, so the file's identity says
// whether one happened.
func TestOpeningRecentRootsTwiceWritesOnce(t *testing.T) {
	m, project := rootsModel(t)
	rootHereAt(t, m, filepath.Join(project, "sub"))
	file := filepath.Join(m.stateDir, state.RootsFileName(project))

	openRoots(t, m)
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	openRoots(t, m)
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("opening < with every sighting fresh rewrote the history")
	}
}

// A finder_key command on a root row is about that directory: {dir} is the
// root itself, as it is for a directory selected in the tree, not its parent.
func TestRecentRootsCommandDirIsTheRoot(t *testing.T) {
	m, project := rootsModel(t)
	sub := filepath.Join(project, "sub")
	rootHereAt(t, m, sub)
	m.escKey()

	m.cfg.Commands = map[string]config.Command{
		"show": {Run: "printf '%s' {dir}", Mode: config.ModeBackground},
	}
	m.finderCmds = map[string]string{"ctrl+e": "show"}
	if got := openRoots(t, m); len(got) != 1 || got[0] != sub {
		t.Fatalf("< offers %v, want the subtree", got)
	}
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if done := drainCmdDone(t, cmd); done.out != sub {
		t.Errorf("{dir} = %q, want the root %q", done.out, sub)
	}
}

// What the tree search left in its Grep and Type fields stays there for "f" to
// resume, and none of it may reach "<": no ripgrep run behind the list, no
// stale counter markers, no rg command for ctrl+y, and no ctrl+g moving the
// cursor by rebuilding the tree's matches underneath it.
func TestRecentRootsIgnoreTheTreeSearchFields(t *testing.T) {
	m, project := rootsModel(t)
	rootHereAt(t, m, filepath.Join(project, "sub"))
	m.escKey()
	rootHereAt(t, m, filepath.Join(project, "other"))
	m.grepInput.SetValue("TODO")
	m.typeInput.SetValue("*.go")
	m.grepCapped = true

	if got := openRoots(t, m); len(got) != 2 {
		t.Fatalf("< offers %v, want two roots", got)
	}
	if m.grepping() || m.grepRunning {
		t.Error("the tree's Grep pattern started a search under <")
	}
	if cmd := m.finderCommand(); cmd != "" {
		t.Errorf("finderCommand = %q under <, want nothing for ctrl+y to copy", cmd)
	}
	if m.finderStacks() {
		t.Error("rows stack as if showing grep hits")
	}
	m.moveFuzzySel(1)
	m.handleKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.fuzzySel != 1 {
		t.Errorf("ctrl+g moved the cursor to %d", m.fuzzySel)
	}
	if c := m.renderFinderCounter(); strings.Contains(c, "max") || strings.Contains(c, "…") || strings.Contains(c, "×") {
		t.Errorf("counter = %q, want a plain position", c)
	}
	// And the fields are still there for "f".
	if m.grepInput.Value() != "TODO" || m.typeInput.Value() != "*.go" {
		t.Error("opening < cleared the tree search's fields")
	}
}

// "~" stands for the home directory only at a path boundary.
func TestAbbrevHomeNeedsAPathBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		home:                 "~",
		home + "/code/proj":  "~/code/proj",
		home + "2/proj":      home + "2/proj",
		"/elsewhere/project": "/elsewhere/project",
	} {
		if got := abbrevHome(in); got != want {
			t.Errorf("abbrevHome(%q) = %q, want %q", in, got, want)
		}
	}
}
