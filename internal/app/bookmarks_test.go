package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/bookmark"
	"github.com/relloyd/filetree/internal/config"
)

const bmSample = "package main\n\nfunc alpha() {\n\tif x {\n\t\treturn nil\n\t}\n}\n"

// bmModel is a model rooted in a git repository of real files, with its
// bookmark store in a temp config dir and a default command that echoes what it
// opened.
//
// The root is a real repo on purpose: that is what keys a repository store with
// checkout-relative paths. A plain directory keys the *global* store instead,
// whose paths are absolute — a different code path, covered separately.
func bmModel(t *testing.T, files ...string) *Model {
	t.Helper()
	m := recentModel(t, files...)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if out, err := exec.Command("git", "init", "-q", m.tr.Root.Path).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	m.cfg.Commands = map[string]config.Command{
		"edit": {Run: "echo {paths}", Mode: config.ModeBackground, Key: "e", FinderKey: "ctrl+e"},
	}
	m.cfg.DefaultCommand = "edit"
	m.cfgPath = filepath.Join(t.TempDir(), "config.toml")
	m.buildBindings()
	return m
}

// bmWrite puts a file in the model's root, since every one of these tests needs
// real bytes to anchor against.
func bmWrite(t *testing.T, m *Model, rel, body string) string {
	t.Helper()
	abs := filepath.Join(m.tr.Root.Path, rel)
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// addBookmark stores one against the model's own root, capturing the anchor
// from the file the way the CLI does.
func addBookmark(t *testing.T, m *Model, rel string, line int, at time.Time) {
	t.Helper()
	root := m.tr.Root.Path
	ctx, off, err := bookmark.Capture(filepath.Join(root, rel), line, 2)
	if err != nil {
		t.Fatalf("capture %s:%d: %v", rel, line, err)
	}
	repo := bookmark.RepoFor(root)
	s := bookmark.Load(m.bookmarksDir(), repo)
	s.Add(bookmark.Bookmark{
		Path: rel, Line: line, Context: ctx, Offset: off, At: at, LastSeen: at,
	}, 500)
	if err := s.Save(m.bookmarksDir(), 500, at, 0); err != nil {
		t.Fatal(err)
	}
}

func bmPaths(m *Model) []string {
	out := make([]string, len(m.bmRows))
	for i, idx := range m.bmRows {
		out[i] = m.bmAll[idx].Bookmark.Path
	}
	return out
}

func TestBookmarkViewListsNewestFirst(t *testing.T) {
	m := bmModel(t, "a.go", "b.go", "c.go")
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		bmWrite(t, m, f, bmSample)
	}
	now := time.Now()
	addBookmark(t, m, "a.go", 3, now.Add(-2*time.Hour))
	addBookmark(t, m, "b.go", 5, now.Add(-time.Hour))
	addBookmark(t, m, "c.go", 1, now)

	m.startBookmarks()

	if m.finderSrc != srcBookmark || m.mode != modeFuzzy {
		t.Fatalf("src=%v mode=%v", m.finderSrc, m.mode)
	}
	if got := strings.Join(bmPaths(m), ","); got != "c.go,b.go,a.go" {
		t.Errorf("order = %v, want newest first", bmPaths(m))
	}
}

// The query has to reach the line's contents, not just its path — that is what
// makes it a list of places rather than of files.
func TestBookmarkQueryMatchesLineTextAsWellAsPath(t *testing.T) {
	m := bmModel(t, "a.go", "b.go")
	bmWrite(t, m, "a.go", "package main\n\nfunc theTarget() {}\n")
	bmWrite(t, m, "b.go", "package main\n\nfunc other() {}\n")
	now := time.Now()
	addBookmark(t, m, "a.go", 3, now.Add(-time.Hour))
	addBookmark(t, m, "b.go", 3, now)

	m.startBookmarks()
	if len(m.bmRows) != 2 {
		t.Fatalf("expected both, got %v", bmPaths(m))
	}

	// "theTarget" appears in neither path.
	for _, r := range "theTarget" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := bmPaths(m); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("query over line text gave %v, want just a.go", got)
	}
}

func TestBookmarkTabCyclesTheSort(t *testing.T) {
	m := bmModel(t, "z.go", "a.go")
	for _, f := range []string{"z.go", "a.go"} {
		bmWrite(t, m, f, bmSample)
	}
	now := time.Now()
	addBookmark(t, m, "z.go", 1, now) // newest
	addBookmark(t, m, "a.go", 1, now.Add(-time.Hour))

	m.startBookmarks()
	if got := bmPaths(m); got[0] != "z.go" {
		t.Fatalf("recency order = %v", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if m.bmSort != sortPath {
		t.Fatalf("sort = %v, want path", m.bmSort)
	}
	if got := bmPaths(m); got[0] != "a.go" {
		t.Errorf("path order = %v, want a.go first", got)
	}
	// And back again.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.bmSort != sortRecent {
		t.Errorf("sort = %v, want it to cycle back", m.bmSort)
	}
}

// A bookmark filed under another repository is correct but invisible here, so
// the header says how many there are and ctrl+s reveals them.
func TestBookmarkScopeWidensToEveryProject(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 3, time.Now())

	// One belonging to a different project entirely.
	other := otherProject(t)
	s := bookmark.Load(m.bookmarksDir(), other)
	s.Add(bookmark.Bookmark{
		Path: "x.go", Line: 1, Context: []string{"package other"}, Offset: 0,
		At: time.Now(), LastSeen: time.Now(),
	}, 500)
	if err := s.Save(m.bookmarksDir(), 500, time.Now(), 0); err != nil {
		t.Fatal(err)
	}

	m.startBookmarks()
	if len(m.bmRows) != 1 {
		t.Fatalf("narrowed list = %v, want just this project's", bmPaths(m))
	}
	if m.bmHidden != 1 {
		t.Errorf("hidden count = %d, want 1", m.bmHidden)
	}
	if got := plainText(m.renderBookmarkStatus()); !strings.Contains(got, "+1 elsewhere") {
		t.Errorf("header = %q, want it to mention what is hidden", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	if !m.bmAllRepos || len(m.bmRows) != 2 {
		t.Errorf("widened list = %v (allRepos=%v), want both", bmPaths(m), m.bmAllRepos)
	}
}

// otherProject is a second directory that is not this tree, so it keys a store
// of its own — standing in for a bookmark taken in another repository.
func otherProject(t *testing.T) string {
	t.Helper()
	return bookmark.Canonical(t.TempDir())
}

func TestBookmarkForgetRemovesItFromTheStore(t *testing.T) {
	m := bmModel(t, "a.go", "b.go")
	for _, f := range []string{"a.go", "b.go"} {
		bmWrite(t, m, f, bmSample)
	}
	now := time.Now()
	addBookmark(t, m, "a.go", 3, now.Add(-time.Hour))
	addBookmark(t, m, "b.go", 3, now)

	m.startBookmarks()
	if got := bmPaths(m); got[0] != "b.go" {
		t.Fatalf("order = %v", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})

	if got := bmPaths(m); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("after forgetting = %v, want just a.go", got)
	}
	// And it is gone from disk, not just from the view.
	s := bookmark.Load(m.bookmarksDir(), bookmark.RepoFor(m.tr.Root.Path))
	if len(s.Bookmarks) != 1 || s.Bookmarks[0].Path != "a.go" {
		t.Errorf("store holds %+v", s.Bookmarks)
	}
}

// Enter opens at the bookmarked line, which is the whole point of the feature.
func TestBookmarkEnterOpensAtTheLine(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 5, time.Now())

	m.startBookmarks()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}

	got := strings.TrimRight(drainCmdDone(t, cmd).out, "\n")
	// Canonical: a bookmark is stored against the checkout as git reports it,
	// so what it opens is the resolved path, not whatever spelling the tree
	// root happens to use.
	want := filepath.Join(bookmark.Canonical(m.tr.Root.Path), "a.go") + ":5"
	if got != want {
		t.Errorf("opened %q, want %q", got, want)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
}

// A line that has drifted is opened where it is now, not where it was stored.
func TestBookmarkEnterFollowsADriftedLine(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	abs := filepath.Join(bookmark.Canonical(m.tr.Root.Path), "a.go")
	addBookmark(t, m, "a.go", 5, time.Now())

	// Two lines inserted above it.
	bmWrite(t, m, "a.go", "// one\n// two\n"+bmSample)

	m.startBookmarks()
	if row, ok := m.bookmarkRow(0); !ok || row.AtLine != 7 {
		t.Fatalf("resolved to line %d, want 7", row.AtLine)
	}

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if want := abs + ":7"; strings.TrimRight(drainCmdDone(t, cmd).out, "\n") != want {
		t.Errorf("opened at the stored line rather than the resolved one")
	}
}

// A bookmark whose file is gone still lists — with its stored text, so it stays
// searchable — but refuses to open rather than launching an editor on nothing.
func TestBookmarkMissingFileListsButWillNotOpen(t *testing.T) {
	m := bmModel(t, "a.go")
	abs := bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 5, time.Now())
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	m.startBookmarks()

	row, ok := m.bookmarkRow(0)
	if !ok {
		t.Fatal("a missing file should still be listed")
	}
	if row.State != bookmark.Missing || strings.TrimSpace(row.Text) != "return nil" {
		t.Errorf("row = %v %q, want the stored anchor kept", row.State, row.Text)
	}

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("expected a note")
	}
	if !strings.Contains(m.statusMsg, "gone") {
		t.Errorf("status = %q, want it to explain", m.statusMsg)
	}
	if m.mode != modeFuzzy {
		t.Error("a refused open should leave you in the list")
	}
}

// ctrl+t and friends act on the row, at its resolved line.
func TestBookmarkFinderCommandUsesTheResolvedLine(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	abs := filepath.Join(bookmark.Canonical(m.tr.Root.Path), "a.go")
	addBookmark(t, m, "a.go", 5, time.Now())
	bmWrite(t, m, "a.go", "// one\n"+bmSample)

	m.startBookmarks()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+e produced no command")
	}

	if got, want := strings.TrimRight(drainCmdDone(t, cmd).out, "\n"), abs+":6"; got != want {
		t.Errorf("ran on %q, want %q", got, want)
	}
	if m.mode != modeFuzzy {
		t.Error("a finder command should stay in the list")
	}
}

// The view has one input line, and B is what opens it.
func TestBookmarkViewShape(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 3, time.Now())

	m.mode = modeNormal
	m.handleKey(tea.KeyPressMsg{Code: 'B', Text: "B"})

	if m.finderSrc != srcBookmark {
		t.Fatalf("B did not open the bookmarks view (src=%v)", m.finderSrc)
	}
	if got := m.finderHeaderLines(); got != 1 {
		t.Errorf("header is %d lines, want 1", got)
	}
	if got := plainText(strings.Join(m.renderFinderHeader(), "\n")); !strings.Contains(got, "Marks") {
		t.Errorf("header = %q, want it labelled", got)
	}
}

// Typing must show what it matched, the way the tree finder does — the query
// reaches the line's text as well as its path, so the highlight has to land in
// either half depending on what was typed.
func TestBookmarkRowsHighlightWhatTheQueryMatched(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", "package main\n\nfunc theTarget() {}\n")
	addBookmark(t, m, "a.go", 3, time.Now())
	m.width = 80

	m.startBookmarks()

	// Comparing against the same row drawn with no match positions is what
	// makes this a real check: the row carries dim and italic styling anyway,
	// so merely containing escape codes proves nothing.
	type render struct{ withMatch, without string }
	draw := func() render {
		b := m.bmAll[m.bmRows[0]]
		return render{
			withMatch: m.renderBookmarkRow(b, m.bmMatched[0], false),
			without:   m.renderBookmarkRow(b, nil, false),
		}
	}
	typeQuery := func(q string) {
		m.clearFinder()
		for _, r := range q {
			m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}

	for _, q := range []string{
		"a.go",      // the path half
		"theTarget", // the line-contents half, past the location in searchText
		"3",         // the line number, between the two
	} {
		t.Run(q, func(t *testing.T) {
			typeQuery(q)
			if len(m.bmRows) != 1 {
				t.Fatalf("query %q matched %d rows", q, len(m.bmRows))
			}
			if len(m.bmMatched[0]) == 0 {
				t.Fatal("no match positions recorded")
			}
			got := draw()
			if got.withMatch == got.without {
				t.Errorf("query %q changed nothing about how the row is drawn:\n %q", q, got.withMatch)
			}
			if plainText(got.withMatch) != plainText(got.without) {
				t.Error("highlighting altered the row's text, not just its styling")
			}
		})
	}
}

// "B" and "f" remember separately. Sharing one query field made pressing either
// one throw away where the other had got to.
func TestBookmarkAndFinderRememberIndependently(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 3, time.Now())

	// Leave a tree search in place.
	m.startFuzzy()
	for _, r := range "tree" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.typeInput.SetValue("*.go")

	// Use the bookmark view with a query of its own.
	m.startBookmarks()
	if got := m.bmInput.Value(); got != "" {
		t.Fatalf("bookmark query started at %q", got)
	}
	for _, r := range "alpha" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := m.bmInput.Value(); got != "alpha" {
		t.Fatalf("bookmark query = %q", got)
	}

	// "f" comes back to the tree search, untouched by the detour.
	m.resumeFuzzy()
	if m.finderSrc != srcTree {
		t.Fatal("f did not return to the tree finder")
	}
	if got := m.input.Value(); got != "tree" {
		t.Errorf("tree Find = %q, want it kept across the bookmark view", got)
	}
	if got := m.typeInput.Value(); got != "*.go" {
		t.Errorf("tree Type = %q, want it kept", got)
	}

	// ...and "B" comes back to the bookmark query, likewise untouched.
	m.startBookmarks()
	if got := m.bmInput.Value(); got != "alpha" {
		t.Errorf("bookmark query = %q, want it kept across the tree finder", got)
	}
}

// ctrl+o in the bookmark view clears its own field only.
func TestBookmarkClearLeavesTheTreeFieldsAlone(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 3, time.Now())

	m.startFuzzy()
	for _, r := range "keep" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	m.startBookmarks()
	for _, r := range "drop" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})

	if got := m.bmInput.Value(); got != "" {
		t.Errorf("bookmark query = %q, want it cleared", got)
	}
	if got := m.input.Value(); got != "keep" {
		t.Errorf("tree Find = %q, want it untouched", got)
	}
}

// Leaving for the tree finder and coming back must not strand the source.
func TestBookmarkSourceResetsForTheTreeFinder(t *testing.T) {
	m := bmModel(t, "a.go")
	bmWrite(t, m, "a.go", bmSample)
	addBookmark(t, m, "a.go", 3, time.Now())

	m.startBookmarks()
	m.startFuzzy()
	if m.finderSrc != srcTree {
		t.Error("/ did not return to the tree")
	}
	m.startBookmarks()
	m.startRecent()
	if m.finderSrc != srcRecent {
		t.Error("b did not switch to the history")
	}
}
