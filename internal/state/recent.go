package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Recent is the per-root history of files that have been opened, newest first.
// It lives beside that root's State rather than inside it: State is written on
// nearly every interaction, and a history is worth merging with what another
// process wrote rather than overwriting (see Save).
type Recent struct {
	Version int          `json:"version"`
	Root    string       `json:"root"`
	Files   []RecentFile `json:"files"` // newest first
}

// RecentFile is one entry. Path is root-relative and slash-separated, the same
// convention the fuzzy finder uses, so the history survives the root being
// moved and cannot name a file outside the tree it belongs to.
type RecentFile struct {
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

func NewRecent(root string) *Recent { return &Recent{Version: 1, Root: root} }

// RecentFileName is the history file for a root, sitting next to FileName's
// state file and sharing its hash so the pair are recognisably one root's.
func RecentFileName(root string) string {
	return strings.TrimSuffix(FileName(root), ".json") + ".recent.json"
}

// LoadRecent returns the saved history for root, or an empty one if it is
// missing, corrupt, or belongs to a different root. A history is a convenience,
// so a bad file is silently started over rather than reported.
func LoadRecent(stateDir, root string) *Recent {
	data, err := os.ReadFile(filepath.Join(stateDir, RecentFileName(root)))
	if err != nil {
		return NewRecent(root)
	}
	var r Recent
	if err := json.Unmarshal(data, &r); err != nil || r.Root != root {
		return NewRecent(root)
	}
	return &r
}

// Add records rel as opened at now, keeping the list newest-first and no longer
// than maxLen. Opening a file again moves its existing entry to the front
// instead of adding a second one — the list is of files, not of openings.
func (r *Recent) Add(rel string, now time.Time, maxLen int) {
	if rel == "" {
		return
	}
	r.Files = slices.DeleteFunc(r.Files, func(f RecentFile) bool { return f.Path == rel })
	r.Files = append([]RecentFile{{Path: rel, At: now}}, r.Files...)
	r.truncate(maxLen)
}

func (r *Recent) truncate(maxLen int) {
	if maxLen > 0 && len(r.Files) > maxLen {
		r.Files = r.Files[:maxLen]
	}
}

// Save merges r with whatever is on disk and writes the result atomically.
//
// The merge is what makes two ft sessions on one root workable: each holds its
// own copy in memory, so a plain overwrite would silently drop everything the
// other session had opened. Where both know a path, the newer timestamp wins.
func (r *Recent) Save(stateDir string, maxLen int) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	merged := LoadRecent(stateDir, r.Root)
	at := make(map[string]time.Time, len(merged.Files)+len(r.Files))
	for _, f := range append(append([]RecentFile{}, merged.Files...), r.Files...) {
		if prev, ok := at[f.Path]; !ok || f.At.After(prev) {
			at[f.Path] = f.At
		}
	}
	out := &Recent{Version: 1, Root: r.Root, Files: make([]RecentFile, 0, len(at))}
	for p, t := range at {
		out.Files = append(out.Files, RecentFile{Path: p, At: t})
	}
	// Newest first, with the path breaking ties so the file does not churn
	// between saves when two entries share a timestamp.
	sort.Slice(out.Files, func(i, j int) bool {
		if !out.Files[i].At.Equal(out.Files[j].At) {
			return out.Files[i].At.After(out.Files[j].At)
		}
		return out.Files[i].Path < out.Files[j].Path
	})
	out.truncate(maxLen)
	r.Files = out.Files

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(stateDir, RecentFileName(r.Root))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
