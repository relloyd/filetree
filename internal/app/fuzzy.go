package app

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/search"
)

// The walk streams candidates to the model instead of delivering them in one
// message: a filtered walk of a large root can take seconds, and results have
// to appear as they are found. Chunks flush on size or on age, whichever comes
// first, so a filter that matches rarely still shows its first hits promptly.
const (
	fuzzyChunkSize  = 512
	fuzzyFlushEvery = 80 * time.Millisecond
)

// finderField identifies which of the finder's input lines has focus.
type finderField int

const (
	fieldQuery finderField = iota // fuzzy name query
	fieldType                     // file-type filter (globs)
	fieldGrep                     // content regex, run through ripgrep
	finderFieldCount
)

// finderPick is a row the finder was left on, so reopening can put the
// selection back. Best effort by nature: the tree, the file, or the search
// results may all have moved on while it was closed.
type finderPick struct {
	rel  string // root-relative path
	line int    // content mode: the matched line; 0 otherwise
}

// isZero reports whether there is no row to go back to.
func (p finderPick) isZero() bool { return p.rel == "" }

// fuzzyChunk is one batch of candidates from the walker goroutine. The final
// chunk of a walk has done set, and truncated when the candidate cap stopped
// it early.
type fuzzyChunk struct {
	cands     []string
	done      bool
	truncated bool
}

// fuzzyBaseLimit is the configured match cap. It is read at use time rather
// than cached so editing the config through "C" takes effect on the next
// query, and it falls back to the default for models built without a config
// (tests).
func (m *Model) fuzzyBaseLimit() int {
	if m.cfg == nil || m.cfg.General.FuzzyMaxMatches < 1 {
		return config.DefaultFuzzyMaxMatches
	}
	return m.cfg.General.FuzzyMaxMatches
}

// fuzzyLimit is how many matches the finder ranks and keeps: the configured
// cap times whatever the session has raised it by.
func (m *Model) fuzzyLimit() int {
	return m.fuzzyBaseLimit() * m.fuzzyFactor()
}

// fuzzyFactor is the session multiplier applied to the configured cap. It
// starts at one and only "ctrl+g" moves it, so the zero value is the default.
func (m *Model) fuzzyFactor() int {
	return max(1, m.fuzzyLimitFactor)
}

// raiseFuzzyLimit multiplies the match cap by one more, for the rest of the
// session — the finder said the list was capped and the user asked for more.
// The two modes recover the dropped results differently: name-mode candidates
// are still in hand, but content-mode hits past the old cap were discarded and
// have to be searched for again.
func (m *Model) raiseFuzzyLimit() tea.Cmd {
	m.fuzzyLimitFactor = m.fuzzyFactor() + 1
	if m.grepping() {
		return m.scheduleGrep()
	}
	m.rebuildFinderMatches()
	return nil
}

// rebuildFinderMatches recomputes the match list from the candidates already
// walked, keeping the selection where it is. It differs from refuzzy in not
// resetting the cursor: raising the limit adds rows below, it does not start a
// new search.
func (m *Model) rebuildFinderMatches() {
	sel, scroll := m.fuzzySel, m.fuzzyScroll
	m.fuzzyQuery = "" // force a full re-match rather than narrowing
	m.refuzzy()
	m.fuzzySel, m.fuzzyScroll = sel, scroll
	m.applyFuzzyLimit()
}

// fuzzyMaxCands caps how many candidates the walk records. With a type filter
// set the cap is rarely reached, which is the point: filtering during the walk
// is what lets a huge root be searched to its leaves.
func (m *Model) fuzzyMaxCands() int {
	if m.cfg == nil || m.cfg.General.FuzzyMaxCandidates < 1 {
		return config.DefaultFuzzyMaxCandidates
	}
	return m.cfg.General.FuzzyMaxCandidates
}

// startFuzzy opens the finder empty.
func (m *Model) startFuzzy() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.typeInput.Reset()
	m.grepInput.Reset()
	m.grepRaw = ""
	m.finderField = fieldQuery
	m.resumeWant = finderPick{}
	return m.enterFuzzy()
}

// resumeFuzzy reopens the finder with the fields as they were left, and aims
// to put the selection back on the row it was left on. The fields survive on
// their own — neither esc nor a jump clears them, only the next startFuzzy —
// so resuming is a matter of not resetting rather than of saving.
func (m *Model) resumeFuzzy() (tea.Model, tea.Cmd) {
	m.resumeWant = m.lastPick
	return m.enterFuzzy()
}

// enterFuzzy is the work both entry points share: take the screen snapshot,
// compile whatever the Type field holds, focus, walk, and search.
func (m *Model) enterFuzzy() (tea.Model, tea.Cmd) {
	m.mode = modeFuzzy
	m.stopGrep()
	m.grepHits, m.grepRows, m.grepErr = nil, nil, ""
	m.grepCapped, m.grepSkipped = false, 0
	m.fuzzyQuery = ""

	// Compile straight into the model rather than through applyTypeFilter,
	// which short-circuits when the text has not changed — on resume it has
	// not, and the walk still needs the filter.
	m.fuzzyFilterRaw = m.typeInput.Value()
	if f, err := search.CompileFilter(m.fuzzyFilterRaw); err != nil {
		m.fuzzyFilter, m.fuzzyFilterErr = search.Filter{}, err.Error()
	} else {
		m.fuzzyFilter, m.fuzzyFilterErr = f, ""
	}

	// Snapshot what is on screen: visible entries seed the candidate list
	// (in tree order, so an empty query browses exactly what you see) and
	// get a ranking bonus — if you can see it, typing its name finds it.
	// Resuming re-takes it, because jumping to a file expanded ancestors and
	// the old snapshot no longer describes the screen.
	m.fuzzyVisible = map[string]bool{}
	m.fuzzyVisOrder = nil
	for _, r := range m.rows {
		if r.Node == m.tr.Root {
			continue
		}
		rel := m.tr.Rel(r.Node.Path)
		if !m.fuzzyVisible[rel] {
			m.fuzzyVisible[rel] = true
			m.fuzzyVisOrder = append(m.fuzzyVisOrder, rel)
		}
	}

	cmds := []tea.Cmd{m.focusFinderField(), m.restartFuzzyWalk()}
	// Hits from before the trip away are stale, and stopGrep invalidated their
	// generation, so a resumed content search is re-run rather than restored.
	if m.grepping() {
		cmds = append(cmds, m.scheduleGrep())
	}
	return m, tea.Batch(cmds...)
}

// focusFinderField puts the cursor in the field finderField names and takes it
// out of the other two.
func (m *Model) focusFinderField() tea.Cmd {
	m.input.Blur()
	m.typeInput.Blur()
	m.grepInput.Blur()
	return m.finderInput().Focus()
}

// clearFinder empties all three fields without leaving the finder. Focus stays
// where it is: clearing is not moving, and the header keeps showing a focused
// field even when it is empty, so nothing shifts under the cursor.
func (m *Model) clearFinder() tea.Cmd {
	m.input.Reset()
	m.typeInput.Reset()
	m.grepInput.Reset()
	m.grepRaw, m.fuzzyQuery = "", ""
	m.resumeWant = finderPick{}
	m.stopGrep()
	m.grepHits, m.grepRows, m.grepErr = nil, nil, ""
	m.grepCapped, m.grepSkipped = false, 0
	m.fuzzyFilter, m.fuzzyFilterErr, m.fuzzyFilterRaw = search.Filter{}, "", ""
	return m.restartFuzzyWalk()
}

// walkParams is everything the walker goroutine needs, captured by value so it
// touches no model state. Git lookups use a snapshot of loaded statuses.
type walkParams struct {
	root        string
	visible     []string
	filter      search.Filter
	showHidden  bool
	showIgnored bool
	statuses    map[string]*gitx.RepoStatus
	max         int
}

// restartFuzzyWalk cancels any walk in flight and starts a fresh one for the
// current filter, returning the command that receives its first chunk. Every
// walk carries a generation so chunks from an abandoned one can be dropped.
func (m *Model) restartFuzzyWalk() tea.Cmd {
	m.stopFuzzyWalk()
	m.fuzzyGen++
	m.fuzzyCands = nil
	m.fuzzyAll = nil
	m.fuzzyMatches = nil
	m.fuzzySel, m.fuzzyScroll = 0, 0
	m.fuzzyTrunc = false
	m.fuzzyWalking = true

	statuses := make(map[string]*gitx.RepoStatus, len(m.statuses))
	for k, v := range m.statuses {
		statuses[k] = v
	}
	p := walkParams{
		root:        m.tr.Root.Path,
		visible:     append([]string(nil), m.fuzzyVisOrder...),
		filter:      m.fuzzyFilter,
		showHidden:  m.showHidden,
		showIgnored: m.showIgnored,
		statuses:    statuses,
		max:         m.fuzzyMaxCands(),
	}

	ch := make(chan fuzzyChunk, 4)
	cancel := make(chan struct{})
	m.fuzzyWalk, m.fuzzyCancel = ch, cancel
	go walkCandidates(p, ch, cancel)
	return waitFuzzyWalk(ch, m.fuzzyGen)
}

// stopFuzzyWalk tells the walker goroutine to stop. Abandoning a search of a
// huge root must stop the syscalls, not just ignore their results.
func (m *Model) stopFuzzyWalk() {
	if m.fuzzyCancel != nil {
		close(m.fuzzyCancel)
		m.fuzzyCancel = nil
	}
	m.fuzzyWalk = nil
	m.fuzzyWalking = false
}

// waitFuzzyWalk receives one chunk, mirroring waitFs: the Update handler
// re-arms it until the walk reports done.
func waitFuzzyWalk(ch <-chan fuzzyChunk, gen int) tea.Cmd {
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return nil
		}
		return fuzzyCandsMsg{gen: gen, cands: c.cands, done: c.done, truncated: c.truncated}
	}
}

// walkCandidates gathers candidate paths breadth-first, honouring the current
// hidden/ignored toggles and the type filter. Breadth-first means that when
// the candidate cap is hit in a huge root, shallow entries are always indexed
// — a top-level dir can never be crowded out by a deep subtree. Directories
// are descended regardless of the filter; only what gets *recorded* is
// filtered, so "*.hcl" reaches the leaves of a tree it could never index whole.
func walkCandidates(p walkParams, out chan<- fuzzyChunk, cancel <-chan struct{}) {
	defer close(out)

	seen := make(map[string]bool, 1024)
	buf := make([]string, 0, fuzzyChunkSize)
	total := 0
	truncated := false
	last := time.Now()

	// emit blocks until the chunk is taken or the walk is cancelled; it
	// reports whether the walk should continue.
	emit := func(done bool) bool {
		select {
		case out <- fuzzyChunk{cands: buf, done: done, truncated: truncated}:
			buf = make([]string, 0, fuzzyChunkSize)
			last = time.Now()
			return true
		case <-cancel:
			return false
		}
	}
	add := func(rel string) {
		if seen[rel] || !p.filter.Match(rel) {
			return
		}
		seen[rel] = true
		buf = append(buf, rel)
		total++
	}

	// Visible entries first (tree order), and guaranteed present even if the
	// cap truncates the rest.
	for _, rel := range p.visible {
		add(rel)
	}

	repoRoots := map[string]string{}
	rootFor := func(dir string) string {
		if r, ok := repoRoots[dir]; ok {
			return r
		}
		r := gitx.FindRepoRoot(dir)
		repoRoots[dir] = r
		return r
	}
	type qitem struct {
		abs string
		rel string
	}
	queue := []qitem{{abs: p.root, rel: ""}}
	for len(queue) > 0 {
		select {
		case <-cancel:
			return
		default:
		}
		it := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(it.abs)
		if err != nil {
			continue
		}
		repo := rootFor(it.abs)
		for _, de := range entries {
			name := de.Name()
			isDir := de.IsDir() // symlinked dirs report false and are not descended
			if name == ".git" && isDir {
				continue
			}
			if !p.showHidden && strings.HasPrefix(name, ".") {
				continue
			}
			abs := filepath.Join(it.abs, name)
			if !p.showIgnored && repo != "" {
				if st := p.statuses[repo]; st != nil && st.CodeFor(gitx.RelPath(repo, abs), isDir) == gitx.Ignored {
					continue
				}
			}
			rel := path.Join(it.rel, name)
			add(rel)
			if isDir {
				queue = append(queue, qitem{abs: abs, rel: rel})
			}
		}
		// The cap is checked between directories, never mid-listing: a
		// half-indexed directory is worse than a smaller complete one.
		if total >= p.max {
			truncated = true
			break
		}
		if len(buf) >= fuzzyChunkSize || (len(buf) > 0 && time.Since(last) >= fuzzyFlushEvery) {
			if !emit(false) {
				return
			}
		}
	}
	emit(true)
}

// addFuzzyCands folds a streamed chunk into the current results without
// re-matching what has already been seen.
func (m *Model) addFuzzyCands(chunk []string) {
	if len(chunk) == 0 {
		return
	}
	m.fuzzyCands = append(m.fuzzyCands, chunk...)
	q := m.input.Value()
	if q == "" {
		// Browse order: the candidate order itself. Only a screenful is ever
		// needed, so stop building the list once it is past the limit.
		if limit := m.fuzzyLimit(); len(m.fuzzyAll) < limit {
			for _, c := range chunk[:min(len(chunk), limit-len(m.fuzzyAll))] {
				m.fuzzyAll = append(m.fuzzyAll, fuzzy.Match{Str: c})
			}
		}
	} else {
		m.fuzzyAll = rerankMatches(q, append(m.fuzzyAll, fuzzy.Find(q, chunk)...), m.fuzzyVisible)
	}
	m.applyFuzzyLimit()
}

// refuzzy recomputes the match list for the current query. A query that
// extends the previous one can only shrink the match set (fuzzy matching is
// subsequence matching), so it is re-run over the previous matches rather than
// over every candidate — which is what keeps typing responsive on a large
// corpus.
func (m *Model) refuzzy() {
	q, prev := m.input.Value(), m.fuzzyQuery
	m.fuzzyQuery = q
	if m.grepping() {
		// In content mode the query narrows the hits ripgrep found, not the
		// candidate list.
		m.rebuildGrepRows()
		return
	}
	switch {
	case q == "":
		n := min(m.fuzzyLimit(), len(m.fuzzyCands))
		m.fuzzyAll = make([]fuzzy.Match, n)
		for i := range n {
			m.fuzzyAll[i] = fuzzy.Match{Str: m.fuzzyCands[i]}
		}
	case prev != "" && strings.HasPrefix(q, prev):
		pool := make([]string, len(m.fuzzyAll))
		for i, mt := range m.fuzzyAll {
			pool[i] = mt.Str
		}
		m.fuzzyAll = rerankMatches(q, fuzzy.Find(q, pool), m.fuzzyVisible)
	default:
		m.fuzzyAll = rerankMatches(q, fuzzy.Find(q, m.fuzzyCands), m.fuzzyVisible)
	}
	m.applyFuzzyLimit()
	m.fuzzySel, m.fuzzyScroll = 0, 0
}

// applyFuzzyLimit publishes the capped view of the match list, keeping the
// selection valid as results stream in underneath it.
func (m *Model) applyFuzzyLimit() {
	limit := m.fuzzyLimit()
	if len(m.fuzzyAll) > limit {
		m.fuzzyMatches = m.fuzzyAll[:limit]
	} else {
		m.fuzzyMatches = m.fuzzyAll
	}
	// With an empty query addFuzzyCands stops building fuzzyAll at the limit,
	// so the candidate count is what says whether anything was left out.
	m.fuzzyCapped = len(m.fuzzyAll) > limit ||
		(m.input.Value() == "" && len(m.fuzzyCands) > limit)
	m.fuzzySel = clamp(m.fuzzySel, 0, max(0, len(m.fuzzyMatches)-1))
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, len(m.fuzzyMatches)-m.fuzzyVisibleRows()))
	m.applyResumeTarget()
}

// applyTypeFilter recompiles the type filter and restarts the walk, since the
// filter is applied while walking. A malformed glob leaves the previous
// results in place and reports itself in the finder header. Keys that only
// move the cursor leave the text alone and must not restart anything.
func (m *Model) applyTypeFilter() tea.Cmd {
	if m.typeInput.Value() == m.fuzzyFilterRaw {
		return nil
	}
	m.fuzzyFilterRaw = m.typeInput.Value()
	f, err := search.CompileFilter(m.typeInput.Value())
	if err != nil {
		m.fuzzyFilterErr = err.Error()
		return nil
	}
	m.fuzzyFilterErr = ""
	m.fuzzyFilter = f
	// The filter scopes the content search too, so a running one is re-run
	// against the new set of files.
	if m.grepping() {
		return tea.Batch(m.restartFuzzyWalk(), m.scheduleGrep())
	}
	return m.restartFuzzyWalk()
}

// cycleFinderField moves focus between the finder's input lines.
func (m *Model) cycleFinderField(delta int) tea.Cmd {
	n := int(finderFieldCount)
	m.finderField = finderField((int(m.finderField) + delta + n) % n)
	return m.focusFinderField()
}

// finderCanDeleteForward reports whether the focused input has a character to
// the right of the cursor — that is, whether a forward delete would do
// anything. textinput applies the same test before deleting.
func (m *Model) finderCanDeleteForward() bool {
	in := m.finderInput()
	return in.Position() < len([]rune(in.Value()))
}

// finderInput is the input line that currently has focus.
func (m *Model) finderInput() *textinput.Model {
	switch m.finderField {
	case fieldType:
		return &m.typeInput
	case fieldGrep:
		return &m.grepInput
	default:
		return &m.input
	}
}

// fuzzyVisibleRows is how many match lines fit under the finder's input lines.
func (m *Model) fuzzyVisibleRows() int {
	return max(1, m.treeHeight()-m.finderHeaderLines())
}

// finderHeaderLines is how many rows the input area occupies. The type and
// grep fields each get a line of their own, shown once focused or non-empty,
// so an unused field costs no screen space.
func (m *Model) finderHeaderLines() int {
	n := 1
	if m.finderField == fieldType || m.typeInput.Value() != "" {
		n++
	}
	if m.finderField == fieldGrep || m.grepInput.Value() != "" {
		n++
	}
	return n
}

// moveFuzzySel moves the fuzzy selection by delta, scrolling the list so
// the selection stays on screen.
func (m *Model) moveFuzzySel(delta int) {
	m.fuzzySel = clamp(m.fuzzySel+delta, 0, max(0, m.finderLen()-1))
	m.ensureFuzzyVisible()
}

// ensureFuzzyVisible scrolls the result list so the selection is on screen.
func (m *Model) ensureFuzzyVisible() {
	h := m.fuzzyVisibleRows()
	if m.fuzzySel < m.fuzzyScroll {
		m.fuzzyScroll = m.fuzzySel
	}
	if m.fuzzySel >= m.fuzzyScroll+h {
		m.fuzzyScroll = m.fuzzySel - h + 1
	}
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, m.finderLen()-h))
}

// applyResumeTarget re-selects the row the finder was left on, once it shows
// up in the results. Results stream in, so this runs each time the list
// changes rather than once. It is best effort by nature — the file may be
// gone, or the search may no longer match it — and gives up quietly: on the
// first match, on the first keystroke (updateFinderInput), and when a walk or
// search finishes without turning it up.
func (m *Model) applyResumeTarget() {
	if m.resumeWant.rel == "" {
		return
	}
	for i := range m.finderLen() {
		if m.finderPath(i) != m.resumeWant.rel {
			continue
		}
		// In content mode the same file appears once per matching line, so
		// the line number picks the occurrence.
		if m.grepping() && m.resumeWant.line > 0 &&
			m.grepHits[m.grepRows[i]].Line != m.resumeWant.line {
			continue
		}
		m.fuzzySel = i
		m.ensureFuzzyVisible()
		m.resumeWant = finderPick{}
		return
	}
}

// rerankMatches orders fuzzy matches in two tiers: entries visible in the
// tree first (you typed the name of something you can see), then the rest.
// Within a tier, shallow paths and basename hits beat equally-fuzzy deep
// ones (e.g. `filetree` outranks `go/pkg/mod/.../filetree@v2/main.go` when
// run from $HOME). Adjustments scale with the best raw score because
// sahilm/fuzzy scores are query-dependent in magnitude and its greedy
// matcher makes raw scores unreliable to compare across candidates.
func rerankMatches(query string, matches []fuzzy.Match, visible map[string]bool) []fuzzy.Match {
	if len(matches) == 0 {
		return matches
	}
	maxScore := 0
	for _, mt := range matches {
		maxScore = max(maxScore, mt.Score)
	}
	unit := max(1, maxScore/20)

	q := strings.ToLower(query)
	tiers := make([]int, len(matches))
	scores := make([]int, len(matches))
	for i, mt := range matches {
		if !visible[mt.Str] {
			tiers[i] = 1
		}
		s := mt.Score - unit*strings.Count(mt.Str, "/")
		base := strings.ToLower(path.Base(mt.Str))
		switch {
		case base == q:
			s += 8 * unit
		case strings.HasPrefix(base, q):
			s += 4 * unit
		case strings.Contains(base, q):
			s += 2 * unit
		}
		scores[i] = s
	}
	idx := make([]int, len(matches))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if tiers[idx[a]] != tiers[idx[b]] {
			return tiers[idx[a]] < tiers[idx[b]]
		}
		return scores[idx[a]] > scores[idx[b]]
	})
	out := make([]fuzzy.Match, len(matches))
	for i, j := range idx {
		out[i] = matches[j]
	}
	return out
}

// fuzzyJump reveals the chosen path: expands its ancestors, selects it, and
// centres the view on it.
func (m *Model) fuzzyJump() (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	m.stopFuzzyWalk()
	m.stopGrep()
	rel := m.finderPath(m.fuzzySel)
	if rel == "" {
		return m, nil
	}
	// Remember where we left, so the finder can be resumed on this row.
	m.lastPick = finderPick{rel: rel}
	if m.grepping() && m.fuzzySel < len(m.grepRows) {
		m.lastPick.line = m.grepHits[m.grepRows[m.fuzzySel]].Line
	}
	m.tr.ExpandRel(path.Dir(rel))
	m.reflatten()
	abs := filepath.Join(m.tr.Root.Path, filepath.FromSlash(rel))
	if n := m.tr.FindByPath(abs); n != nil {
		for i, r := range m.rows {
			if r.Node == n {
				m.cursor = i
				m.scroll = clamp(i-m.treeHeight()/2, 0, max(0, len(m.rows)-m.treeHeight()))
				break
			}
		}
	}
	m.syncWatches()
	m.saveState()
	return m, tea.Batch(m.ensureStatusesForExpanded()...)
}
