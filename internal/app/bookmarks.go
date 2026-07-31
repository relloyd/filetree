package app

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/bookmark"
	"github.com/relloyd/filetree/internal/config"
)

// bookmarkSort is the order the list is shown in; "tab" cycles it.
type bookmarkSort int

const (
	sortRecent bookmarkSort = iota // newest capture first
	sortPath                       // by path, then line — reading order
	bookmarkSortCount
)

func (s bookmarkSort) String() string {
	if s == sortPath {
		return "path"
	}
	return "recent"
}

// resolvedBookmark is one row: the stored bookmark, located against a checkout.
// The store it came from is carried along so a widened list can say which
// project each row belongs to, and so forgetting one writes to the right file.
type resolvedBookmark struct {
	bookmark.Resolved
	repo string
}

// bookmarksDir is where the stores live.
func (m *Model) bookmarksDir() string { return bookmark.Dir(filepath.Dir(m.cfgPath)) }

// bookmarkRepo is the store this tree belongs to, and checkout the working copy
// its bookmarks are resolved against.
//
// The tree root answers both when it is inside a repository. In the worktrees
// view it is not — the root there is ~/.filetree/worktrees — so the selection
// answers instead, and "B" shows the bookmarks of whichever worktree the cursor
// is on rather than nothing at all.
//
// Falling through to ("", "") is not a failure: that is the global store, whose
// paths are absolute and therefore need no checkout to resolve against.
func (m *Model) bookmarkRepo() (repo, checkout string) {
	dirs := []string{m.tr.Root.Path}
	if sel := m.selected(); sel != nil {
		dir := sel.Path
		if !sel.IsDir {
			dir = filepath.Dir(sel.Path)
		}
		dirs = append(dirs, dir)
	}
	for _, dir := range dirs {
		if c := bookmark.CheckoutFor(dir); c != "" {
			return bookmark.RepoFor(dir), c
		}
	}
	return "", ""
}

// startBookmarks opens the finder over this repo's bookmarks, where you left
// it: the query, the sort and the scope all survive for the session.
//
// It deliberately does not go through startFuzzyIn. That resets the three tree
// fields, which would mean every press of "B" quietly threw away the search "f"
// exists to bring back. The two views own separate state and neither entry key
// disturbs the other's.
func (m *Model) startBookmarks() (tea.Model, tea.Cmd) {
	m.finderSrc = srcBookmark
	m.finderField = fieldQuery
	m.resumeWant = finderPick{}
	m.scopeDir = ""
	return m.enterFuzzy()
}

// loadBookmarks re-reads the stores and resolves every bookmark against the
// checkout in front of us.
//
// Re-reading on entry rather than caching is deliberate: "ft bookmark" is a
// separate process, so the store on disk is the only shared truth between the
// editor and a running filetree. Resolving here rather than at render time
// means the query can match on a line's *current* contents, and that a line
// that has drifted is corrected once per visit rather than per keystroke.
func (m *Model) loadBookmarks() {
	m.bmAll, m.bmRows, m.bmHidden = nil, nil, 0
	repo, checkout := m.bookmarkRepo()

	stores := []*bookmark.Store{bookmark.Load(m.bookmarksDir(), repo)}
	if m.bmAllRepos {
		stores = bookmark.LoadAll(m.bookmarksDir())
	}

	now := time.Now()
	var dirty []*bookmark.Store
	for _, st := range stores {
		// Each store resolves against its own repository, except the current
		// one, whose checkout may be a different worktree of it. The global
		// store resolves against nothing — its paths are already absolute.
		against := st.Repo
		if st.Repo == repo && checkout != "" {
			against = checkout
		}
		changed := false
		for i := range st.Bookmarks {
			b := &st.Bookmarks[i]
			r := bookmark.Resolve(against, *b)
			if r.State.Found() {
				// Seeing the file is what holds the retention clock back, and
				// re-anchoring pays for the drift once rather than every visit.
				b.LastSeen = now
				if r.AtLine != b.Line {
					b.Line, changed = r.AtLine, true
				}
				changed = true
			}
			m.bmAll = append(m.bmAll, resolvedBookmark{Resolved: r, repo: st.Repo})
		}
		if changed {
			dirty = append(dirty, st)
		}
	}
	for _, st := range dirty {
		_ = st.Save(m.bookmarksDir(), m.bookmarkMax(), now, m.bookmarkRetention())
	}

	if !m.bmAllRepos {
		for _, st := range bookmark.LoadAll(m.bookmarksDir()) {
			if st.Repo != repo {
				m.bmHidden += len(st.Bookmarks)
			}
		}
	}
	m.sortBookmarks()
}

func (m *Model) bookmarkMax() int {
	if m.cfg == nil || m.cfg.General.BookmarkMax < 1 {
		return config.DefaultBookmarkMax
	}
	return m.cfg.General.BookmarkMax
}

func (m *Model) bookmarkRetention() time.Duration {
	if m.cfg == nil {
		return config.DefaultBookmarkRetentionDays * 24 * time.Hour
	}
	return m.cfg.RetentionWindow()
}

// sortBookmarks orders the resolved list, then rebuilds the visible rows.
func (m *Model) sortBookmarks() {
	sort.SliceStable(m.bmAll, func(i, j int) bool {
		a, b := m.bmAll[i], m.bmAll[j]
		if m.bmSort == sortPath {
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			return a.AtLine < b.AtLine
		}
		return a.Bookmark.At.After(b.Bookmark.At)
	})
	m.rebuildBookmarkRows()
}

// cycleBookmarkSort is "tab" in this view: it has no second input field to move
// to, and both orderings are wanted often enough to deserve the easy key.
func (m *Model) cycleBookmarkSort() {
	m.bmSort = (m.bmSort + 1) % bookmarkSortCount
	m.sortBookmarks()
}

// toggleBookmarkScope is "ctrl+s": widen to every project's store and back.
// A bookmark is filed under the repository it points into, so one taken in
// another project is correct but invisible here — this is how it is found.
func (m *Model) toggleBookmarkScope() {
	m.bmAllRepos = !m.bmAllRepos
	m.loadBookmarks()
	m.fuzzySel, m.fuzzyScroll = 0, 0
}

// forgetBookmark is "ctrl+x": drop the highlighted row from its own store.
func (m *Model) forgetBookmark() tea.Cmd {
	if m.fuzzySel < 0 || m.fuzzySel >= len(m.bmRows) {
		return nil
	}
	row := m.bmAll[m.bmRows[m.fuzzySel]]
	st := bookmark.Load(m.bookmarksDir(), row.repo)
	if !st.Remove(row.Bookmark.Path, row.Bookmark.Line) {
		return nil
	}
	// Overwrite, not Save: Save merges with the file, which would read the
	// bookmark we just dropped straight back in.
	if err := st.Overwrite(m.bookmarksDir()); err != nil {
		return m.note(err.Error(), true)
	}
	sel := m.fuzzySel
	m.loadBookmarks()
	// Stay where you were rather than jumping to the top: forgetting several in
	// a row is the normal way to tidy a list.
	m.fuzzySel = clamp(sel, 0, max(0, len(m.bmRows)-1))
	m.ensureFuzzyVisible()
	return m.note("Forgot "+path.Base(row.Bookmark.Path), false)
}

// rebuildBookmarkRows applies the query. It matches over the path, the line
// number and the line's text together, so typing a fragment of the *code* finds
// the bookmark as readily as typing part of its path — which is the point of a
// list of places rather than of files.
//
// The match positions are kept, not just the row numbers, so the list can show
// what it matched on the way the tree finder does.
func (m *Model) rebuildBookmarkRows() {
	m.bmRows, m.bmMatched = m.bmRows[:0], m.bmMatched[:0]
	if q := m.bmInput.Value(); q == "" {
		for i := range m.bmAll {
			m.bmRows = append(m.bmRows, i)
			m.bmMatched = append(m.bmMatched, nil)
		}
	} else {
		hay := make([]string, len(m.bmAll))
		for i, b := range m.bmAll {
			hay[i] = b.searchText(m.bmAllRepos)
		}
		for _, mt := range fuzzy.Find(q, hay) {
			m.bmRows = append(m.bmRows, mt.Index)
			m.bmMatched = append(m.bmMatched, mt.MatchedIndexes)
		}
	}
	m.fuzzySel = clamp(m.fuzzySel, 0, max(0, len(m.bmRows)-1))
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, len(m.bmRows)-m.fuzzyVisibleRows()))
	m.applyResumeTarget()
}

// location is the "path:line" a row leads with, prefixed by the project when
// the list has been widened to all of them.
func (b resolvedBookmark) location(allRepos bool) string {
	name := b.Bookmark.Path
	if allRepos && b.repo != "" {
		name = filepath.Base(b.repo) + "/" + name
	}
	return name + ":" + fmt.Sprint(b.AtLine)
}

// searchText is what the query matches against. It is built from the same
// pieces the row is drawn from, in the same order, so that the byte offsets
// fuzzy.Find reports can be mapped straight back onto the rendered line — which
// is what lets the highlighting be correct rather than approximate.
func (b resolvedBookmark) searchText(allRepos bool) string {
	s := b.location(allRepos) + " " + strings.TrimSpace(b.Text)
	if b.Label != "" {
		s += " " + b.Label
	}
	return s
}

// bookmarkRow is the highlighted row, or false when the list is empty.
func (m *Model) bookmarkRow(i int) (resolvedBookmark, bool) {
	if i < 0 || i >= len(m.bmRows) {
		return resolvedBookmark{}, false
	}
	return m.bmAll[m.bmRows[i]], true
}

// bookmarkJumpAndOpen is enter in this view: reveal the file in the tree when
// it happens to be in it, and open it at the bookmarked line either way.
//
// The line is the whole point of a bookmark, so it goes through Vars.Line and
// {paths} appends it — the same route a Grep hit already takes.
func (m *Model) bookmarkJumpAndOpen() (tea.Model, tea.Cmd) {
	row, ok := m.bookmarkRow(m.fuzzySel)
	if !ok {
		return m, nil
	}
	if !row.State.Found() {
		return m, m.note("That file is gone — ctrl+x forgets it", true)
	}
	m.mode = modeNormal
	m.stopFuzzyWalk()
	m.stopGrep()

	abs := row.Abs
	var cmds []tea.Cmd
	// Only a path inside this tree can be revealed; one from another project,
	// or from a worktree, simply opens.
	if rel := m.tr.Rel(abs); rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		m.tr.ExpandRel(path.Dir(rel))
		m.reflatten()
		m.selectPath(abs)
		m.syncWatches()
		m.saveState()
		cmds = append(cmds, m.ensureStatusesForExpanded()...)
	}

	c, ok := m.cfg.Commands[m.cfg.DefaultCommand]
	if !ok {
		return m, tea.Batch(append(cmds, m.note("no default command configured", true))...)
	}
	_, open := m.execCommand(m.cfg.DefaultCommand, c, config.Vars{
		Path:    abs,
		RelPath: m.gitRelPathFor(abs),
		Dir:     filepath.Dir(abs),
		Root:    m.tr.Root.Path,
		Name:    path.Base(abs),
		Line:    row.AtLine,
		Paths:   []string{abs},
		Marked:  append([]string(nil), m.markOrder...),
	}, false)
	return m, tea.Batch(append(cmds, open)...)
}
