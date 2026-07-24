package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/tree"
)

// repoRootFor memoizes git repo discovery per directory.
func (m *Model) repoRootFor(dir string) string {
	if r, ok := m.repoRoots[dir]; ok {
		return r
	}
	r := gitx.FindRepoRoot(dir)
	m.repoRoots[dir] = r
	return r
}

// nodeCode returns the git status code for a node, via the status of the
// repo that contains the node's parent directory.
func (m *Model) nodeCode(n *tree.Node) gitx.Code {
	if n == m.tr.Root {
		return gitx.None
	}
	root := m.repoRootFor(filepath.Dir(n.Path))
	if root == "" {
		return gitx.None
	}
	return m.statuses[root].CodeFor(gitx.RelPath(root, n.Path), n.IsDir)
}

func (m *Model) dirHasChanges(n *tree.Node) bool {
	if !n.IsDir {
		return false
	}
	root := m.repoRootFor(filepath.Dir(n.Path))
	if root == "" {
		return false
	}
	return m.statuses[root].DirContainsChanges(gitx.RelPath(root, n.Path))
}

// ensureStatusCmd starts a background git status read for the repo
// containing dir, unless one is loaded or already in flight.
func (m *Model) ensureStatusCmd(dir string) tea.Cmd {
	root := m.repoRootFor(dir)
	if root == "" || m.statusPending[root] {
		return nil
	}
	if _, ok := m.statuses[root]; ok {
		return nil
	}
	m.statusPending[root] = true
	return func() tea.Msg {
		s, err := gitx.ReadStatus(root)
		return statusLoadedMsg{root: root, status: s, err: err}
	}
}

func (m *Model) ensureStatusesForExpanded() []tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range m.rows {
		if r.Node.IsDir && r.Node.Expanded {
			if c := m.ensureStatusCmd(r.Node.Path); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

// invalidateStatusCmds re-reads git status for every repo touching dirs.
func (m *Model) invalidateStatusCmds(dirs []string) []tea.Cmd {
	roots := map[string]bool{}
	for _, d := range dirs {
		if r := m.repoRootFor(d); r != "" {
			roots[r] = true
		}
	}
	var cmds []tea.Cmd
	for root := range roots {
		if m.statusPending[root] {
			continue
		}
		m.statusPending[root] = true
		cmds = append(cmds, func() tea.Msg {
			s, err := gitx.ReadStatus(root)
			return statusLoadedMsg{root: root, status: s, err: err}
		})
	}
	return cmds
}

// refreshAllStatusCmds re-reads every repo we have a status for.
func (m *Model) refreshAllStatusCmds() []tea.Cmd {
	var dirs []string
	for root := range m.statuses {
		dirs = append(dirs, root)
	}
	for root := range m.statuses {
		delete(m.statuses, root)
	}
	return m.invalidateStatusCmds(dirs)
}
