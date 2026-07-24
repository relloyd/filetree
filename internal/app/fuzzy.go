package app

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/gitx"
)

const (
	fuzzyWalkCap    = 50000
	fuzzyMaxMatches = 100
)

func (m *Model) startFuzzy() (tea.Model, tea.Cmd) {
	m.mode = modeFuzzy
	m.input.Reset()
	m.fuzzyCands = nil
	m.fuzzyMatches = nil
	m.fuzzySel = 0
	return m, tea.Batch(m.input.Focus(), m.fuzzyWalkCmd())
}

// fuzzyWalkCmd gathers candidate paths in the background, honouring the
// current hidden/ignored toggles. The walk is breadth-first so that when the
// candidate cap is hit in a huge root, shallow entries are always indexed —
// a top-level dir can never be crowded out by a deep subtree. Only data
// captured here is touched from the goroutine; git lookups use a snapshot of
// loaded statuses.
func (m *Model) fuzzyWalkCmd() tea.Cmd {
	root := m.tr.Root.Path
	showHidden, showIgnored := m.showHidden, m.showIgnored
	statuses := make(map[string]*gitx.RepoStatus, len(m.statuses))
	for k, v := range m.statuses {
		statuses[k] = v
	}
	return func() tea.Msg {
		var cands []string
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
		queue := []qitem{{abs: root, rel: ""}}
	walk:
		for len(queue) > 0 {
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
				if !showHidden && strings.HasPrefix(name, ".") {
					continue
				}
				abs := filepath.Join(it.abs, name)
				if !showIgnored && repo != "" {
					if st := statuses[repo]; st != nil && st.CodeFor(gitx.RelPath(repo, abs), isDir) == gitx.Ignored {
						continue
					}
				}
				rel := path.Join(it.rel, name)
				cands = append(cands, rel)
				if len(cands) >= fuzzyWalkCap {
					break walk
				}
				if isDir {
					queue = append(queue, qitem{abs: abs, rel: rel})
				}
			}
		}
		return fuzzyCandsMsg{cands: cands}
	}
}

func (m *Model) refuzzy() {
	q := m.input.Value()
	if q == "" {
		// BFS candidate order: shallowest entries first.
		n := min(fuzzyMaxMatches, len(m.fuzzyCands))
		m.fuzzyMatches = make([]fuzzy.Match, n)
		for i := range n {
			m.fuzzyMatches[i] = fuzzy.Match{Str: m.fuzzyCands[i]}
		}
	} else {
		matches := rerankMatches(q, fuzzy.Find(q, m.fuzzyCands))
		if len(matches) > fuzzyMaxMatches {
			matches = matches[:fuzzyMaxMatches]
		}
		m.fuzzyMatches = matches
	}
	m.fuzzySel = 0
}

// rerankMatches re-scores fuzzy matches so shallow paths and basename hits
// beat equally-fuzzy deep ones (e.g. `filetree` outranks
// `go/pkg/mod/.../filetree@v2/main.go` when run from $HOME).
func rerankMatches(query string, matches []fuzzy.Match) []fuzzy.Match {
	if len(matches) == 0 {
		return matches
	}
	q := strings.ToLower(query)
	scores := make([]int, len(matches))
	for i, mt := range matches {
		s := mt.Score - 10*strings.Count(mt.Str, "/")
		base := strings.ToLower(path.Base(mt.Str))
		switch {
		case base == q:
			s += 120
		case strings.HasPrefix(base, q):
			s += 60
		case strings.Contains(base, q):
			s += 30
		}
		scores[i] = s
	}
	idx := make([]int, len(matches))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
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
	if m.fuzzySel < 0 || m.fuzzySel >= len(m.fuzzyMatches) {
		return m, nil
	}
	rel := m.fuzzyMatches[m.fuzzySel].Str
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
