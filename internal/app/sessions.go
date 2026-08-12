package app

import (
	"path/filepath"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/tmux"
)

// sessionPrefix is what marks a tmux session as one of ours. Read at use time
// rather than cached so editing the config through "C" takes effect on the
// next press, and it falls back to the default for models built without a
// config (tests).
func (m *Model) sessionPrefix() string {
	if m.cfg == nil || m.cfg.Sessions.Prefix == "" {
		return tmux.DefaultPrefix
	}
	return m.cfg.Sessions.Prefix
}

// repoIdent is the repository a session belongs to, as seen from one
// directory: the checkout to run in, the repo name to file it under, and the
// branch.
//
// The repo name comes from the *main* repository even when the selection sits
// in a linked worktree, so every worktree of a project files under one name
// and the picker can be read down its first column. The checkout stays the
// worktree — that is where the agent should run.
type repoIdent struct {
	GitRoot string // the checkout: repo root, or linked worktree root
	Repo    string // basename of the main repo
	Branch  string // branch of the checkout, or a short hash when detached
}

// ok reports whether there is enough here to name a session.
func (r repoIdent) ok() bool { return r.GitRoot != "" && r.Repo != "" && r.Branch != "" }

// repoIdentFor resolves the repository containing dir. Everything is empty
// outside a repository, which is what lets a command that needs a session name
// be refused rather than run against half a name.
func (m *Model) repoIdentFor(dir string) repoIdent {
	root := m.repoRootFor(dir)
	if root == "" {
		return repoIdent{}
	}
	main := root
	if gitx.IsLinkedWorktree(root) {
		// A worktree directory is named after the branch, not the project, so
		// without this every worktree would file under a different repo.
		if mr, err := gitx.MainRepoOf(root); err == nil && mr != "" {
			main = mr
		}
	}
	// The branch is already cached for the status bar in the common case; the
	// fallback covers a repo whose first status read has not landed yet, which
	// is exactly the moment after startup when the first key is pressed.
	branch := m.branches[root]
	if branch == "" {
		branch = headBranch(root)
	}
	return repoIdent{GitRoot: root, Repo: filepath.Base(main), Branch: branch}
}

// sessionVars fills in the repo-derived half of a command's template
// variables. An identity that did not resolve leaves them empty.
func (m *Model) sessionVars(id repoIdent) config.Vars {
	if !id.ok() {
		return config.Vars{}
	}
	prefix := m.sessionPrefix()
	return config.Vars{
		GitRoot: id.GitRoot,
		Repo:    id.Repo,
		Branch:  id.Branch,
		Session: tmux.SessionName(prefix, id.Repo, id.Branch, ""),
	}
}

// sessionNameFor is the full session name for a directory and a tool, or ""
// when the directory is not in a repository.
func (m *Model) sessionNameFor(dir, tool string) string {
	id := m.repoIdentFor(dir)
	if !id.ok() {
		return ""
	}
	return tmux.SessionName(m.sessionPrefix(), id.Repo, id.Branch, tool)
}
