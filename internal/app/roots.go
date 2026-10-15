package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/state"
)

// rootRow is one row of the "<" view: a remembered root, and the text it is
// shown and matched as.
type rootRow struct {
	state.RecentRoot
	label string // what the row reads as; the query matches exactly this
	home  bool   // the launch root, which picking returns to the way Esc does
}

// launchRoot is the root this tree's history of roots is filed under: the one
// Esc comes home to. While a view is open that is homeRoot; otherwise the tree
// is standing on it.
//
// Keying by the launch root rather than the current one is what keeps the list
// still while you move between its entries — a subtree reached with ">" would
// otherwise have a history of its own, and the trip back would not be in it.
func (m *Model) launchRoot() string {
	if m.homeRoot != "" {
		return m.homeRoot
	}
	return m.tr.Root.Path
}

// recordRoot notes that the tree is now standing on its current root. loadRoot
// calls it, so it hears about every successful switch — ">", a pick from "<",
// Esc home — and about startup, which is what puts the launch root itself in
// the list: from a subtree, the first row is then the way back.
//
// The scratch and worktrees views are the exception. Both have keys of their
// own, and the worktrees view is a corridor you pass through on the way to a
// checkout — recording it would put it between every pair of worktrees you
// alternate between. A checkout rooted into with ">" from inside it is
// recorded as usual.
func (m *Model) recordRoot() {
	root := m.tr.Root.Path
	if m.cfg != nil && (root == m.scratchDirPath() || root == m.worktreesDirPath()) {
		return
	}
	now := time.Now()
	r := state.LoadRoots(m.stateDir, m.launchRoot())
	r.Visit(root, now, m.recentRootsMax())
	// A history is a convenience: failing to write it is not worth a red line
	// in the status bar on every re-root, which is the same call the file
	// history makes.
	//
	// No expiry here (a zero retention): only the visited root's sighting is
	// fresh at this point, so expiring would judge every other entry by when
	// "<" last looked rather than by whether its directory exists — and a month
	// without opening "<" would empty the list on the next re-root.
	_ = r.Save(m.stateDir, m.recentRootsMax(), now, 0)
}

// sightingStale is how old a root's last sighting may get before opening "<"
// writes a fresh one. Against a retention window measured in days, an hour of
// slack costs nothing, and it saves a disk write on almost every switch.
const sightingStale = time.Hour

func (m *Model) recentRootsMax() int {
	if m.cfg == nil || m.cfg.General.RecentRootsMax < 1 {
		return config.DefaultRecentRootsMax
	}
	return m.cfg.General.RecentRootsMax
}

func (m *Model) recentRootsRetention() time.Duration {
	if m.cfg == nil {
		return config.DefaultRecentRootsRetentionDays * 24 * time.Hour
	}
	return m.cfg.RootsRetentionWindow()
}

// startRecentRoots opens the finder over the roots this tree has stood on,
// most recent first, with the current one left out — so the row the cursor
// starts on is the root you were on before this one, and "<" then enter flips
// between the two the way alt-tab does.
//
// The query starts empty every time. Unlike "B" this is a switcher rather than
// a search you come back to: a query left over from the last switch would hide
// the very row you came for.
func (m *Model) startRecentRoots() (tea.Model, tea.Cmd) {
	m.finderSrc = srcRoots
	m.finderField = fieldQuery
	m.resumeWant = finderPick{}
	m.scopeDir = ""
	m.rootsInput.Reset()
	return m.enterFuzzy()
}

// loadRecentRoots re-reads the history and builds the rows. Re-reading on entry
// rather than caching is deliberate: another ft launched on the same root
// shares the file, and its visits belong in this list too.
//
// Each root is checked for on the way. One that exists has its sighting
// refreshed, which is what holds the retention clock back; one that does not is
// left out of the list but kept in the file, since a branch switch can bring it
// back. Only once it has gone unseen for the whole window is it dropped — and
// this is the only place that happens, because it is the only place every
// entry has just been looked for.
//
// The file is rewritten only when that changed something: a sighting more than
// an hour stale, or an entry to expire. Opening "<" is a switch, and most
// switches have nothing new to record.
func (m *Model) loadRecentRoots() {
	m.rootsAll, m.rootsRows, m.rootsMatched = nil, nil, nil
	home, current := m.launchRoot(), m.tr.Root.Path
	r := state.LoadRoots(m.stateDir, home)

	now := time.Now()
	dirty := false
	for i := range r.Roots {
		e := &r.Roots[i]
		fi, err := os.Stat(e.Path)
		if err != nil || !fi.IsDir() {
			continue
		}
		if now.Sub(e.LastSeen) > sightingStale {
			e.LastSeen, dirty = now, true
		}
		if e.Path == current {
			continue // picking it would do nothing
		}
		m.rootsAll = append(m.rootsAll, rootRow{
			RecentRoot: *e,
			label:      rootLabel(e.Path, home),
			home:       e.Path == home,
		})
	}
	// Expire after the loop, so what it judges is this visit's sightings. The
	// rows are already built, and only a missing root can expire, so none of
	// them goes with it.
	if r.Expire(now, m.recentRootsRetention()) > 0 {
		dirty = true
	}
	if dirty {
		_ = r.Save(m.stateDir, m.recentRootsMax(), now, m.recentRootsRetention())
	}
	m.rebuildRootRows()
}

// rootLabel is how a root reads in the list. Inside the launch root it is the
// path from there — "internal/app" says everything "~/code/ft/internal/app"
// does in a third of the width, and a sidebar has little to spare. Anything
// else, the launch root included, is the full path with the home directory
// abbreviated, so a worktree reads as "~/.filetree/worktrees/ft/my-branch".
func rootLabel(path, home string) string {
	if path != home && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return filepath.ToSlash(strings.TrimPrefix(path, home+string(filepath.Separator)))
	}
	return abbrevHome(path)
}

// rebuildRootRows applies the query over the same text the row is drawn from,
// so the highlight offsets fuzzy.Find reports land on the right characters.
func (m *Model) rebuildRootRows() {
	hay := make([]string, len(m.rootsAll))
	for i, r := range m.rootsAll {
		hay[i] = r.label
	}
	m.rootsRows, m.rootsMatched = applyFindQuery(parseFindQuery(m.rootsInput.Value()), hay)
	m.fuzzySel = clamp(m.fuzzySel, 0, max(0, len(m.rootsRows)-1))
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, len(m.rootsRows)-m.fuzzyVisibleRows()))
}

// rootRow is the highlighted root, or false when the list is empty.
func (m *Model) rootRow(i int) (rootRow, bool) {
	if i < 0 || i >= len(m.rootsRows) {
		return rootRow{}, false
	}
	return m.rootsAll[m.rootsRows[i]], true
}

// switchToRecentRoot is enter in this view: stand the tree on the highlighted
// root.
//
// It is a detour in the same sense ">" is, and goes through enterView for the
// same remember-once rule: wherever you go from here, one Esc still comes home
// to the launch root. The launch root itself is the exception, and has to be —
// arriving there through enterView would leave homeRoot naming the root you
// are standing on, and the next Esc would "return" to where you already are.
// So picking it is Esc.
//
// No expansion is seeded. Every root keeps its own in its state file, so a
// remembered root opens the way you left it.
func (m *Model) switchToRecentRoot() (tea.Model, tea.Cmd) {
	row, ok := m.rootRow(m.fuzzySel)
	if !ok {
		return m, nil // an empty list is not an error
	}
	m.mode = modeNormal
	if m.homeRoot != "" && row.Path == m.homeRoot {
		return m.leaveView()
	}
	return m.enterView(row.Path, nil)
}

// forgetRecentRoot is "ctrl+x": drop the highlighted root from the history.
// Forgetting the launch root lasts only until the next time the tree stands on
// it, which is as it should be — it is still where Esc goes.
func (m *Model) forgetRecentRoot() tea.Cmd {
	row, ok := m.rootRow(m.fuzzySel)
	if !ok {
		return nil
	}
	r := state.LoadRoots(m.stateDir, m.launchRoot())
	if !r.Remove(row.Path) {
		return nil
	}
	// Overwrite, not Save: Save merges with the file, which would read the root
	// we just dropped straight back in.
	if err := r.Overwrite(m.stateDir); err != nil {
		return m.note(err.Error(), true)
	}
	sel := m.fuzzySel
	m.loadRecentRoots()
	// Stay where you were rather than jumping to the top: forgetting several in
	// a row is the normal way to tidy a list.
	m.fuzzySel = clamp(sel, 0, max(0, len(m.rootsRows)-1))
	m.ensureFuzzyVisible()
	return m.note("Forgot "+row.label, false)
}

// forgetRootsUnder drops dir and everything beneath it from the history. A
// removed worktree is the caller: its checkout is gone for good, so there is
// no point keeping it for the retention window the way a root hidden by a
// branch switch is kept.
func (m *Model) forgetRootsUnder(dir string) {
	r := state.LoadRoots(m.stateDir, m.launchRoot())
	gone := false
	for _, e := range append([]state.RecentRoot(nil), r.Roots...) {
		if e.Path == dir || strings.HasPrefix(e.Path, dir+string(filepath.Separator)) {
			gone = r.Remove(e.Path) || gone
		}
	}
	if gone {
		_ = r.Overwrite(m.stateDir)
	}
}

// rootsStatusNote is the trailing note on the view's input line: how to read
// an empty list, which is the ordinary state until ">" has been used once.
// Short for the same reason as the session list's: it shares one line with
// the query and the counter, in what is often a sidebar.
func (m *Model) rootsStatusNote() string {
	if len(m.rootsAll) == 0 {
		return styleDim.Render("  none yet")
	}
	return ""
}
