package herdr

import (
	"path/filepath"
	"strings"
)

// Shell is one herdr pane that has already passed the checks needing the world:
// it holds no agent, it is sitting at an idle prompt, and Checkout is the
// repository — or linked worktree — its working directory resolves to, empty
// for a pane sitting outside any checkout.
//
// Resolving the checkout is the caller's job precisely so that everything below
// stays pure and table-testable, the same split internal/ipc uses between
// Collect and Pick.
type Shell struct {
	PaneID   string
	TabID    string
	Dir      string // the pane's working directory
	Checkout string // repo or linked-worktree root containing Dir, "" outside one
}

// Scope is the boundary a pane has to sit inside to belong to the selection.
//
// Inside a checkout the boundary is the checkout root, and that is the whole of
// the worktree-awareness: a linked worktree has a root of its own, so a shell in
// the main repo cannot answer for a branch worktree or the other way round.
//
// Outside a checkout there is no such boundary to inherit, so the selection's
// own directory becomes one and membership is "on the same branch of the tree" —
// a shell above the selection or below it, but not off to one side. Both
// directions are needed, and the upward one especially: without it, walking one
// directory deeper would put the shell you just opened out of scope and earn you
// a second workspace for the same place, then a third.
type Scope struct {
	Root string // checkout root, or the selection's directory outside one
	Repo bool   // whether Root is a checkout
}

// ScopeFor is the scope of a selection sitting in dir, whose checkout root is
// root — empty when the selection is not in one.
func ScopeFor(dir, root string) Scope {
	if root != "" {
		return Scope{Root: filepath.Clean(root), Repo: true}
	}
	if dir == "" {
		return Scope{}
	}
	return Scope{Root: filepath.Clean(dir)}
}

// Covers reports whether a pane working in dir, whose own checkout root is
// checkout, belongs to this scope.
//
// A pane inside a checkout of its own never answers for a loose directory that
// merely contains it: a shell in ~/src/someproject belongs to someproject, not
// to ~/src. Without that, opening a shell for a directory full of repositories
// would hand you whichever repository happened to have one open.
func (s Scope) Covers(checkout, dir string) bool {
	if s.Root == "" || dir == "" {
		return false
	}
	if s.Repo {
		return checkout != "" && filepath.Clean(checkout) == s.Root
	}
	return checkout == "" && (within(s.Root, dir) || within(dir, s.Root))
}

// within reports whether p is root or sits underneath it.
//
// Whole segments, so "/a/proj" does not swallow "/a/proj2" — the same reason
// sharedSegments counts segments rather than characters.
func within(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	return sharedSegments(root, p) == len(segments(root))
}

// Pick chooses the shell to focus for a selection sitting in dir, within scope,
// asked for by a tree in tab.
//
// The scope is a *filter* rather than a bonus, and that is the design. A shell
// outside it is not a worse answer, it is a wrong one: the whole point of asking
// for "a shell for this code" is that a worktree of the same project, on another
// branch, must not answer for the main repo or the other way round.
//
// Among the survivors, terms are compared in order rather than summed, so no
// amount of path proximity can outrank the wrong scope and no tab locality
// can outrank a nearer shell:
//
//  1. a shell whose directory *holds* the selection, so that "cd" into it is
//     all that separates the two. A sibling directory shares just as many path
//     segments as the parent does, and is a dead end where the parent is on the
//     way to the file, so shared depth alone cannot separate them;
//  2. then the shell whose directory shares the most with the selection's,
//     which is what "nearest" means once you have to say it precisely;
//  3. then the one in the tree's own tab, so a tie does not send the focus to
//     another workspace;
//  4. then the lowest pane id, so that two equally good answers resolve the
//     same way on every press. A key that landed somewhere different each time
//     would be worse than one that lands somewhere arguable.
//
// Note herdr offers nothing to rank recency on. Its per-pane revision counts
// how much output a pane has produced, not when it last did anything, so "the
// one I used last" is not available and proximity carries the whole weight.
func Pick(shells []Shell, dir string, scope Scope, tab string) (Shell, bool) {
	var best Shell
	var bestScore score
	found := false
	for _, s := range shells {
		if !scope.Covers(s.Checkout, s.Dir) {
			continue
		}
		sc := score{
			holds:   within(s.Dir, dir),
			shared:  sharedSegments(s.Dir, dir),
			sameTab: tab != "" && s.TabID == tab,
		}
		if !found || sc.better(bestScore) || (sc == bestScore && s.PaneID < best.PaneID) {
			best, bestScore, found = s, sc, true
		}
	}
	return best, found
}

// score ranks one shell. The fields are compared in order of declaration, which
// is what stops tab locality from outweighing being nearer the file, and what
// stops a nearer dead end from outweighing a directory the file lives in.
type score struct {
	holds   bool
	shared  int
	sameTab bool
}

func (a score) better(b score) bool {
	if a.holds != b.holds {
		return a.holds
	}
	if a.shared != b.shared {
		return a.shared > b.shared
	}
	return a.sameTab && !b.sameTab
}

// sharedSegments counts the leading path segments two paths have in common.
//
// Whole segments, not characters: a prefix count would score "/a/proj2" against
// "/a/proj" as though they overlapped, and they share nothing but a parent.
func sharedSegments(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	as := segments(a)
	bs := segments(b)
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// segments splits a cleaned path into its parts, dropping the empty leading
// one an absolute path produces.
func segments(p string) []string {
	parts := strings.Split(filepath.Clean(p), string(filepath.Separator))
	if len(parts) > 0 && parts[0] == "" {
		parts = parts[1:]
	}
	return parts
}
