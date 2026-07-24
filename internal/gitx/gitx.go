// Package gitx provides git repository discovery and working-tree status
// (including gitignore detection) by shelling out to git once per repo.
package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Code classifies how git sees a path, in increasing display priority.
type Code int

const (
	None Code = iota
	Ignored
	Untracked
	Staged
	Modified
	Conflict
)

// FindRepoRoot walks up from dir looking for .git (a directory, or a file
// for worktrees/submodules). Returns "" when dir is not inside a repo.
func FindRepoRoot(dir string) string {
	d := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// RelPath returns abs relative to repoRoot, slash-separated.
func RelPath(repoRoot, abs string) string {
	r, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// RepoStatus is a parsed snapshot of `git status --porcelain -z --ignored`.
type RepoStatus struct {
	Root        string
	files       map[string]Code     // repo-relative file path -> code
	dirs        map[string]Code     // whole-subtree codes from "dir/" entries
	changedDirs map[string]struct{} // dirs with tracked changes underneath
}

// ReadStatus runs git for the repo rooted at root. Call it from a background
// command; parsing is deterministic and covered by tests via parseStatus.
func ReadStatus(root string) (*RepoStatus, error) {
	cmd := exec.Command("git", "-C", root, "status", "--porcelain", "-z", "--ignored")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status in %s: %w", root, err)
	}
	return parseStatus(root, out), nil
}

func parseStatus(root string, out []byte) *RepoStatus {
	rs := &RepoStatus{
		Root:        root,
		files:       map[string]Code{},
		dirs:        map[string]Code{},
		changedDirs: map[string]struct{}{},
	}
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 4 || tok[2] != ' ' {
			continue
		}
		x, y := tok[0], tok[1]
		p := tok[3:]
		if x == 'R' || x == 'C' {
			i++ // the following token is the rename/copy source
		}
		code := codeFor(x, y)
		if strings.HasSuffix(p, "/") {
			rs.dirs[strings.TrimSuffix(p, "/")] = code
		} else {
			rs.files[p] = code
		}
		if code != Ignored {
			for d := path.Dir(strings.TrimSuffix(p, "/")); ; d = path.Dir(d) {
				rs.changedDirs[d] = struct{}{}
				if d == "." || d == "/" {
					break
				}
			}
		}
	}
	return rs
}

func codeFor(x, y byte) Code {
	switch {
	case x == '?':
		return Untracked
	case x == '!':
		return Ignored
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return Conflict
	case y != ' ':
		return Modified // unstaged worktree changes (possibly on top of staged)
	default:
		return Staged
	}
}

// CodeFor reports the code for a repo-relative path, inheriting from
// untracked/ignored ancestor directories (git lists those collapsed).
func (rs *RepoStatus) CodeFor(rel string, isDir bool) Code {
	if rs == nil {
		return None
	}
	if isDir {
		if c, ok := rs.dirs[rel]; ok {
			return c
		}
	} else if c, ok := rs.files[rel]; ok {
		return c
	}
	for d := rel; d != "." && d != "/"; {
		d = path.Dir(d)
		if c, ok := rs.dirs[d]; ok {
			return c
		}
	}
	return None
}

// DirContainsChanges reports whether tracked changes (or untracked files)
// exist anywhere beneath the repo-relative directory.
func (rs *RepoStatus) DirContainsChanges(rel string) bool {
	if rs == nil {
		return false
	}
	_, ok := rs.changedDirs[rel]
	return ok
}
