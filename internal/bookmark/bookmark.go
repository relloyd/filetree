// Package bookmark stores and resolves line bookmarks: a file, a line, and
// enough surrounding text to find that line again after the file has been
// edited.
//
// A store belongs to a repository rather than to a filetree root, and paths in
// it are repo-relative. That is what lets one list serve every worktree of a
// repo: the same bookmark resolves against whichever checkout is in front of
// you. Files belonging to no repository share a global store.
package bookmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/relloyd/filetree/internal/storekey"
)

// Bookmark is one remembered place.
type Bookmark struct {
	Path string `json:"path"` // repo-relative, slash-separated
	Line int    `json:"line"`

	// Context is the anchor: the bookmarked line plus a few either side, as
	// they read when the bookmark was taken. A single line is a poor anchor —
	// "}" and "return nil" occur all over a file — so resolution matches the
	// block and only falls back to the line alone when it is unambiguous.
	Context []string `json:"context"`
	// Offset is which element of Context is the bookmarked line. Not always the
	// middle: near the start or end of a file there are fewer lines to take.
	Offset int `json:"offset"`

	Label string `json:"label,omitempty"`

	At time.Time `json:"at"` // when it was captured
	// LastSeen is the last time the file was found. Expiry is measured from it
	// rather than from a "missing since" flag, so a file that disappears with a
	// branch switch and comes back simply stops and restarts the clock.
	LastSeen time.Time `json:"last_seen"`
}

// Text is the bookmarked line itself.
func (b Bookmark) Text() string {
	if b.Offset < 0 || b.Offset >= len(b.Context) {
		return ""
	}
	return b.Context[b.Offset]
}

// Store is one repository's bookmarks, newest first.
type Store struct {
	Version int    `json:"version"`
	Repo    string `json:"repo"` // absolute repo root; "" is the global store
	// Bookmarks is kept newest first, which is also the order the view browses
	// in before any sort is applied.
	Bookmarks []Bookmark `json:"bookmarks"`
}

func New(repo string) *Store { return &Store{Version: 1, Repo: repo} }

// Dir is where stores live, created on demand.
func Dir(cfgDir string) string { return filepath.Join(cfgDir, "bookmarks") }

// FileName is the store file for a repo. An empty repo is the global store.
func FileName(repo string) string { return storekey.Name(repo, ".json") }

// Load returns a repo's store, or an empty one when the file is missing,
// corrupt, or names a different repo. Bookmarks are a convenience, so a bad
// file is started over rather than reported.
func Load(dir, repo string) *Store {
	data, err := os.ReadFile(filepath.Join(dir, FileName(repo)))
	if err != nil {
		return New(repo)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil || s.Repo != repo {
		return New(repo)
	}
	return &s
}

// LoadAll reads every store in dir, newest-first within each. It backs the
// view's "widen to all projects" key, which is how a bookmark filed under a
// repo you are not currently in stays findable.
func LoadAll(dir string) []*Store {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*Store
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Store
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		// Trust the file's own name for itself: a store whose Repo disagrees
		// with where it is filed was renamed underneath us.
		if FileName(s.Repo) != e.Name() {
			continue
		}
		out = append(out, &s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// Add records a bookmark, newest first. Bookmarking the same line twice moves
// the existing entry rather than adding a second: a place is a place.
func (s *Store) Add(b Bookmark, maxLen int) {
	if b.Path == "" || b.Line < 1 {
		return
	}
	if b.LastSeen.IsZero() {
		b.LastSeen = b.At // it was just read off disk, so it was just seen
	}
	s.Bookmarks = slices.DeleteFunc(s.Bookmarks, func(e Bookmark) bool {
		return e.Path == b.Path && e.Line == b.Line
	})
	s.Bookmarks = append([]Bookmark{b}, s.Bookmarks...)
	s.truncate(maxLen)
}

// Overwrite writes the store as it stands, without merging what is on disk.
//
// Deletion needs this, and only deletion. Save merges so that two processes
// adding bookmarks cannot clobber each other — but that same merge would read a
// just-forgotten bookmark straight back out of the file and resurrect it. The
// cost is a narrow race: a bookmark captured between this load and this write
// is lost. That is a far better failure than a delete key that does nothing,
// and tombstones would be a lot of machinery for a list of places.
func (s *Store) Overwrite(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(dir, FileName(s.Repo))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Remove drops the bookmark at path:line, reporting whether it was there.
func (s *Store) Remove(path string, line int) bool {
	before := len(s.Bookmarks)
	s.Bookmarks = slices.DeleteFunc(s.Bookmarks, func(e Bookmark) bool {
		return e.Path == path && e.Line == line
	})
	return len(s.Bookmarks) != before
}

// Expire drops bookmarks whose file has not been seen inside the window. A
// zero or negative retention keeps everything, so a misconfigured window
// cannot silently eat the list.
func (s *Store) Expire(now time.Time, retention time.Duration) int {
	if retention <= 0 {
		return 0
	}
	before := len(s.Bookmarks)
	s.Bookmarks = slices.DeleteFunc(s.Bookmarks, func(e Bookmark) bool {
		return !e.LastSeen.IsZero() && now.Sub(e.LastSeen) > retention
	})
	return before - len(s.Bookmarks)
}

func (s *Store) truncate(maxLen int) {
	if maxLen > 0 && len(s.Bookmarks) > maxLen {
		s.Bookmarks = s.Bookmarks[:maxLen]
	}
}

// Save merges s with whatever is on disk and writes the result atomically.
//
// The merge matters more here than for any other store: "ft bookmark" runs as
// its own process, quite possibly while a filetree holding its own copy is
// open in another pane. A plain overwrite would drop whichever of them saved
// first. Where both know a bookmark, the newer capture and the later sighting
// each win on their own.
func (s *Store) Save(dir string, maxLen int, now time.Time, retention time.Duration) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type key struct {
		path string
		line int
	}
	merged := Load(dir, s.Repo)
	byKey := make(map[key]Bookmark, len(merged.Bookmarks)+len(s.Bookmarks))
	var order []key
	for _, b := range append(append([]Bookmark{}, merged.Bookmarks...), s.Bookmarks...) {
		k := key{b.Path, b.Line}
		prev, seen := byKey[k]
		if !seen {
			order = append(order, k)
			byKey[k] = b
			continue
		}
		if b.At.After(prev.At) {
			// Keep the newer capture, but not at the cost of a later sighting
			// the other copy knew about.
			if prev.LastSeen.After(b.LastSeen) {
				b.LastSeen = prev.LastSeen
			}
			byKey[k] = b
		} else if b.LastSeen.After(prev.LastSeen) {
			prev.LastSeen = b.LastSeen
			byKey[k] = prev
		}
	}
	out := New(s.Repo)
	for _, k := range order {
		out.Bookmarks = append(out.Bookmarks, byKey[k])
	}
	// Newest first, with path and line breaking ties so the file does not churn
	// between saves.
	sort.SliceStable(out.Bookmarks, func(i, j int) bool {
		a, b := out.Bookmarks[i], out.Bookmarks[j]
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	out.Expire(now, retention)
	out.truncate(maxLen)
	s.Bookmarks = out.Bookmarks

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(dir, FileName(s.Repo))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
