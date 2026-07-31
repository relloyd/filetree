package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/state"
)

// recentModel is a model over a root holding the named files, with an empty
// history and a default command that prints the path it was given — so a test
// can assert what enter actually opened, not just that it ran something.
func recentModel(t *testing.T, files ...string) *Model {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		abs := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("package app\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := rootedModel(t, root)
	m.mode = modeNormal
	m.cfg.DefaultCommand = "edit"
	m.cfg.Commands = map[string]config.Command{
		"edit": {Run: "printf '%s' {path}", Mode: config.ModeBackground},
	}
	m.buildBindings()
	return m
}

// recordOpen puts rel in the history at a chosen time, so a test can lay out a
// recency order without waiting for one.
func recordOpen(m *Model, rel string, at time.Time) {
	m.recent.Add(rel, at, m.recentMax())
}

// runCmd runs one command with a deadline, reporting whether it answered.
//
// The deadline is not paranoia: a batch routinely carries timers alongside real
// work — a status note re-arms itself seconds later — and calling one of those
// inline blocks the test for its whole duration.
func runCmd(c tea.Cmd, d time.Duration) (tea.Msg, bool) {
	if c == nil {
		return nil, false
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(d):
		return nil, false
	}
}

// drainCmdDone finds the cmdDoneMsg a key produced. Enter in the history and
// bookmark views batches the reveal's status refresh with the open, so the
// message is one level down inside a BatchMsg rather than returned directly.
func drainCmdDone(t *testing.T, cmd tea.Cmd) cmdDoneMsg {
	t.Helper()
	const budget = 5 * time.Second
	msg, ok := runCmd(cmd, budget)
	if !ok {
		t.Fatal("command did not answer")
	}
	switch msg := msg.(type) {
	case cmdDoneMsg:
		return msg
	case tea.BatchMsg:
		for _, c := range msg {
			if done, ok := runCmd(c, budget); ok {
				if d, isDone := done.(cmdDoneMsg); isDone {
					return d
				}
			}
		}
	}
	t.Fatalf("no cmdDoneMsg in %T", msg)
	return cmdDoneMsg{}
}

func recentPaths(m *Model) []string {
	out := make([]string, len(m.fuzzyMatches))
	for i, mt := range m.fuzzyMatches {
		out[i] = mt.Str
	}
	return out
}

// What lands in the history is decided by whether the command names the file,
// not by which key was pressed. A command that never substitutes the selection
// did not open it.
func TestOnlyCommandsThatNameTheFileAreRecorded(t *testing.T) {
	m := recentModel(t, "a.go", "sub/b.go")
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.tr.Root.Path, "sub")

	for _, tc := range []struct {
		name string
		tmpl string
		path string
		want string // "" when nothing should be recorded
	}{
		{"path template", "hx {path}", filepath.Join(m.tr.Root.Path, "a.go"), "a.go"},
		{"relpath template", "echo {relpath}", filepath.Join(m.tr.Root.Path, "sub/b.go"), "sub/b.go"},
		{"dir template", "tmux split-window -c {dir}", filepath.Join(m.tr.Root.Path, "a.go"), ""},
		{"no placeholders", `[ -z "$TMUX" ] || tmux select-pane -R`, filepath.Join(m.tr.Root.Path, "a.go"), ""},
		{"marked template", "delta {marked1} {marked2}", filepath.Join(m.tr.Root.Path, "a.go"), ""},
		{"a directory", "hx {path}", dir, ""},
		{"outside the root", "hx {path}", outside, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.recent.Files = nil
			m.recordRecent(tc.tmpl, config.Vars{Path: tc.path})

			var got string
			if len(m.recent.Files) > 0 {
				got = m.recent.Files[0].Path
			}
			if got != tc.want {
				t.Errorf("recorded %q, want %q", got, tc.want)
			}
		})
	}
}

// Recording rides on execCommand, so every route to a command is covered by
// the one hook — including scratchNew's direct call, which has no key of its own.
func TestRecordingHappensForAnyCommandRoute(t *testing.T) {
	m := recentModel(t, "a.go")
	m.selectPath(filepath.Join(m.tr.Root.Path, "a.go"))

	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		cmd()
	}
	if len(m.recent.Files) != 1 || m.recent.Files[0].Path != "a.go" {
		t.Fatalf("history after enter = %+v, want a.go", m.recent.Files)
	}
	// And it survives the trip to disk, so the next session sees it.
	reloaded := state.LoadRecent(m.stateDir, m.tr.Root.Path)
	if len(reloaded.Files) != 1 || reloaded.Files[0].Path != "a.go" {
		t.Errorf("persisted history = %+v, want a.go", reloaded.Files)
	}
}

// The view browses the history in recency order on an empty query, and narrows
// on typing like the tree finder does.
func TestRecentViewListsNewestFirstAndNarrows(t *testing.T) {
	m := recentModel(t, "a.go", "sub/b.go", "sub/deep/c.go")
	now := time.Now()
	recordOpen(m, "a.go", now.Add(-2*time.Hour))
	recordOpen(m, "sub/b.go", now.Add(-time.Hour))
	recordOpen(m, "sub/deep/c.go", now)

	m.startRecent()

	if got := recentPaths(m); strings.Join(got, ",") != "sub/deep/c.go,sub/b.go,a.go" {
		t.Errorf("browse order = %v, want newest first", got)
	}
	if m.finderSrc != srcRecent || m.mode != modeFuzzy {
		t.Errorf("src=%v mode=%v", m.finderSrc, m.mode)
	}

	// "c" is a subsequence of no other path here — "b" would match sub/deep/c.go
	// too, through the "b" in "sub".
	m.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if got := recentPaths(m); len(got) != 1 || got[0] != "sub/deep/c.go" {
		t.Errorf("after typing \"c\": %v, want just sub/deep/c.go", got)
	}
}

// A file deleted since it was opened is not offered — it would only fail on
// enter. It stays in the history, because a branch switch can bring it back.
func TestRecentViewSkipsFilesThatAreGone(t *testing.T) {
	m := recentModel(t, "a.go", "b.go")
	now := time.Now()
	recordOpen(m, "a.go", now.Add(-time.Minute))
	recordOpen(m, "b.go", now)
	if err := os.Remove(filepath.Join(m.tr.Root.Path, "b.go")); err != nil {
		t.Fatal(err)
	}

	m.startRecent()

	if got := recentPaths(m); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("candidates = %v, want only the file that still exists", got)
	}
	if len(m.recent.Files) != 2 {
		t.Errorf("history = %+v, want the missing file kept", m.recent.Files)
	}
}

// Enter reveals the row in the tree and opens it. The open names the path
// outright: a hidden file has no row to move to, so anything routed through the
// tree cursor would open whatever was selected before.
func TestRecentEnterOpensTheChosenRowEvenWhenItCannotBeRevealed(t *testing.T) {
	m := recentModel(t, "visible.go", ".hidden.go")
	now := time.Now()
	recordOpen(m, "visible.go", now.Add(-time.Minute))
	recordOpen(m, ".hidden.go", now)
	m.showHidden = false
	m.reflatten()

	m.startRecent()
	if got := m.finderPath(0); got != ".hidden.go" {
		t.Fatalf("first row = %q, want the hidden file", got)
	}
	before := m.cursor

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	done := drainCmdDone(t, cmd)
	if want := filepath.Join(m.tr.Root.Path, ".hidden.go"); done.out != want {
		t.Errorf("opened %q, want %q", done.out, want)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal after enter", m.mode)
	}
	// It is filtered out of the tree, so the cursor has nowhere to go — the
	// reveal is best effort, the open is not.
	if m.cursor != before {
		t.Errorf("cursor moved to %d for a row that is not in the tree", m.cursor)
	}
}

func TestRecentEnterRevealsAFileThatIsInTheTree(t *testing.T) {
	m := recentModel(t, "sub/deep/c.go")
	recordOpen(m, "sub/deep/c.go", time.Now())

	m.startRecent()
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		drainCmdDone(t, cmd)
	}

	sel := m.selected()
	if sel == nil || sel.Name != "c.go" {
		t.Fatalf("selection = %v, want the revealed file", sel)
	}
}

// The history has one field. Cycling would otherwise land on a Type filter that
// narrows a hundred paths a second way, and on a Grep that has no single
// directory for ripgrep to search.
func TestRecentViewOffersOnlyTheQueryField(t *testing.T) {
	m := recentModel(t, "a.go")
	recordOpen(m, "a.go", time.Now())
	m.startRecent()

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if m.finderField != fieldQuery {
		t.Errorf("tab moved focus to field %v", m.finderField)
	}
	if got := m.finderHeaderLines(); got != 1 {
		t.Errorf("header is %d lines, want 1", got)
	}
}

// Each entry key owns a view: "/" and "F" search the tree, "b" the history,
// "B" the bookmarks — and "f" resumes the tree search whatever was open last.
//
// The alternative, which this used to do, is for "f" to reopen whichever list
// you were in. That reads fine until you use one of the others: pressing "b" or
// "B" then silently decides where "f" takes you next, and the tree search you
// wanted back is unreachable.
func TestFinderSourceFollowsTheEntryKey(t *testing.T) {
	m := recentModel(t, "a.go")
	recordOpen(m, "a.go", time.Now())

	m.startRecent()
	if m.finderSrc != srcRecent {
		t.Fatal("b did not open the history")
	}
	m.resumeFuzzy()
	if m.finderSrc != srcTree {
		t.Error("f should resume the tree search, not the history")
	}
	m.startFuzzy()
	if m.finderSrc != srcTree {
		t.Error("/ did not return to the tree")
	}
	m.startRecent()
	m.startFuzzyHere()
	if m.finderSrc != srcTree {
		t.Error("F did not return to the tree")
	}
}
