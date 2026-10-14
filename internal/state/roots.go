package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/relloyd/filetree/internal/storekey"
)

// Roots is the history of roots a tree has stood on, most recently visited
// first. It backs the "<" view, which switches the tree between them.
//
// It is filed under the *launch* root — the one Esc comes home to — rather than
// under whichever root is current: the whole point is a list that stays put
// while you move between its entries, and keying it by the current root would
// hand every subtree a list of its own. Paths are absolute, because entries
// routinely live outside the launch root (a worktree checkout under
// ~/.filetree/worktrees, say).
type Roots struct {
	Version int          `json:"version"`
	Home    string       `json:"home"`  // the launch root this history belongs to
	Roots   []RecentRoot `json:"roots"` // most recently visited first
}

// RecentRoot is one remembered root.
type RecentRoot struct {
	Path string    `json:"path"` // absolute and cleaned
	At   time.Time `json:"at"`   // the last time the tree was rooted here
	// LastSeen is the last time the directory was found to exist. Expiry runs
	// from it rather than from At, so a root that disappears with a branch
	// switch and comes back simply stops and restarts the clock — and one that
	// is merely not visited for a while is never at risk.
	LastSeen time.Time `json:"last_seen"`
}

func NewRoots(home string) *Roots { return &Roots{Version: 1, Home: home} }

// RootsFileName is the history file for a launch root, beside that root's
// state and recent-files history and sharing their hash.
func RootsFileName(home string) string { return storekey.Name(home, ".roots.json") }

// LoadRoots returns the saved history for home, or an empty one if it is
// missing, corrupt, or belongs to a different launch root. Like the file
// history it is a convenience, so a bad file is started over, not reported.
func LoadRoots(stateDir, home string) *Roots {
	data, err := os.ReadFile(filepath.Join(stateDir, RootsFileName(home)))
	if err != nil {
		return NewRoots(home)
	}
	var r Roots
	if err := json.Unmarshal(data, &r); err != nil || r.Home != home {
		return NewRoots(home)
	}
	return &r
}

// Visit records that the tree was rooted at path at now, moving an existing
// entry to the front rather than adding a second: the list is of places, not
// of visits.
func (r *Roots) Visit(path string, now time.Time, maxLen int) {
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	r.Roots = slices.DeleteFunc(r.Roots, func(e RecentRoot) bool { return e.Path == path })
	r.Roots = append([]RecentRoot{{Path: path, At: now, LastSeen: now}}, r.Roots...)
	r.truncate(maxLen)
}

// Remove drops path, reporting whether it was there.
func (r *Roots) Remove(path string) bool {
	path = filepath.Clean(path)
	before := len(r.Roots)
	r.Roots = slices.DeleteFunc(r.Roots, func(e RecentRoot) bool { return e.Path == path })
	return len(r.Roots) != before
}

// Expire drops roots that have not been seen inside the window. A zero or
// negative retention keeps everything, so a misconfigured window cannot
// silently empty the list.
func (r *Roots) Expire(now time.Time, retention time.Duration) int {
	if retention <= 0 {
		return 0
	}
	before := len(r.Roots)
	r.Roots = slices.DeleteFunc(r.Roots, func(e RecentRoot) bool {
		return !e.LastSeen.IsZero() && now.Sub(e.LastSeen) > retention
	})
	return before - len(r.Roots)
}

func (r *Roots) truncate(maxLen int) {
	if maxLen > 0 && len(r.Roots) > maxLen {
		r.Roots = r.Roots[:maxLen]
	}
}

// Save merges r with whatever is on disk, expires and caps the result, and
// writes it atomically.
//
// The merge is what lets two ft sessions launched on one root share a history
// without either silently dropping the other's visits. Where both know a root,
// the later visit and the later sighting each win independently: one session
// may have been there more recently while the other has merely seen it exist.
func (r *Roots) Save(stateDir string, maxLen int, now time.Time, retention time.Duration) error {
	merged := LoadRoots(stateDir, r.Home)
	byPath := make(map[string]RecentRoot, len(merged.Roots)+len(r.Roots))
	for _, e := range append(append([]RecentRoot{}, merged.Roots...), r.Roots...) {
		prev, ok := byPath[e.Path]
		if !ok {
			byPath[e.Path] = e
			continue
		}
		if e.At.After(prev.At) {
			prev.At = e.At
		}
		if e.LastSeen.After(prev.LastSeen) {
			prev.LastSeen = e.LastSeen
		}
		byPath[e.Path] = prev
	}
	out := NewRoots(r.Home)
	for _, e := range byPath {
		out.Roots = append(out.Roots, e)
	}
	// Newest first, with the path breaking ties so the file does not churn
	// between saves when two entries share a timestamp.
	sort.Slice(out.Roots, func(i, j int) bool {
		a, b := out.Roots[i], out.Roots[j]
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		return a.Path < b.Path
	})
	out.Expire(now, retention)
	out.truncate(maxLen)
	r.Roots = out.Roots
	return out.Overwrite(stateDir)
}

// Overwrite writes the history as it stands, without merging what is on disk.
//
// Forgetting needs this, and only forgetting: Save's merge would read a
// just-forgotten root straight back out of the file. The cost is a narrow race
// — a visit another session records between this load and this write is lost —
// which is a far better failure than a forget key that does nothing.
func (r *Roots) Overwrite(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(stateDir, RootsFileName(r.Home))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
