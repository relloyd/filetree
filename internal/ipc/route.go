package ipc

import (
	"path/filepath"
	"strings"
)

// Candidate is one running instance, as its status reply described it.
type Candidate struct {
	PID  int
	Root string // absolute
	Pane string // tmux pane id, "" outside tmux
}

// Location is where a pane lives. Ids rather than names: a session can be
// renamed while ft is running, and "$8"/"@8" cannot.
type Location struct {
	Session string
	Window  string
}

// Contains reports whether target is root or sits underneath it.
//
// filepath.Rel rather than a string prefix, which would let root "/a/proj"
// swallow "/a/proj2".
func Contains(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	root, target = filepath.Clean(root), filepath.Clean(target)
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Pick chooses the instance that should jump to target.
//
// Containment is a *filter*, not a bonus, and that is the whole design of this
// function. An instance whose root does not cover the path cannot show it, so
// scoring tmux locality first would let the pane next to the editor win every
// time and then fail — and an instance that could have shown the file would
// never be reached. Filter first, then let locality choose between the ones
// that can actually do the job.
//
// Among survivors: the caller's own window beats its own session, which beats
// neither; and the deepest root wins, being the most specific project pane for
// that file. Comparing the terms in order rather than summing weights means no
// amount of path depth can outrank being in the right window.
//
// The pid breaks ties so that two identical instances resolve the same way
// every time — a jump that lands somewhere different on each press would be
// worse than one that lands somewhere arguable.
//
// panes maps a pane id to where it lives; caller is where the request came
// from, and is the zero Location outside tmux (in which case containment and
// depth decide alone).
func Pick(cands []Candidate, panes map[string]Location, caller Location, target string) (Candidate, bool) {
	var best Candidate
	var bestScore score
	found := false
	for _, c := range cands {
		if !Contains(c.Root, target) {
			continue
		}
		s := scoreOf(c, panes, caller)
		if !found || s.better(bestScore) || (s == bestScore && c.PID < best.PID) {
			best, bestScore, found = c, s, true
		}
	}
	return best, found
}

// score ranks one candidate. The fields are compared in order of declaration,
// which is what keeps depth from ever outweighing locality.
type score struct {
	sameWindow  bool
	sameSession bool
	depth       int
}

func (a score) better(b score) bool {
	if a.sameWindow != b.sameWindow {
		return a.sameWindow
	}
	if a.sameSession != b.sameSession {
		return a.sameSession
	}
	return a.depth > b.depth
}

func scoreOf(c Candidate, panes map[string]Location, caller Location) score {
	s := score{depth: depth(c.Root)}
	loc, ok := panes[c.Pane]
	if !ok || c.Pane == "" {
		return s
	}
	// An empty caller field must never match an instance that also has none:
	// outside tmux every candidate would read as "same window as me".
	s.sameWindow = caller.Window != "" && loc.Window == caller.Window
	s.sameSession = caller.Session != "" && loc.Session == caller.Session
	return s
}

// depth counts path segments, so /a/b/c outranks /a when both contain the
// target.
func depth(root string) int {
	root = filepath.Clean(root)
	if root == string(filepath.Separator) {
		return 0
	}
	return strings.Count(root, string(filepath.Separator))
}
