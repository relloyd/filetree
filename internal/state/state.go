// Package state persists per-root UI state (expanded dirs, selection,
// toggles) as one JSON file per top-level root under ~/.filetree/state/.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/relloyd/filetree/internal/storekey"
)

type State struct {
	Version      int       `json:"version"`
	Root         string    `json:"root"`
	Expanded     []string  `json:"expanded"` // root-relative dir paths; "." is the root
	Selected     string    `json:"selected,omitempty"`
	ScrollOffset int       `json:"scroll_offset"`
	ShowHidden   *bool     `json:"show_hidden,omitempty"`  // nil = use config default
	ShowIgnored  *bool     `json:"show_ignored,omitempty"` // nil = use config default
	LastUsed     time.Time `json:"last_used"`
}

func New(root string) *State {
	return &State{Version: 1, Root: root, Expanded: []string{"."}}
}

// FileName derives a stable, human-findable file name for a root:
// <sanitised-basename>-<8 hex of sha256(root)>.json
func FileName(root string) string { return storekey.Name(root, ".json") }

// Load returns the saved state for root, or a fresh one if missing/corrupt.
func Load(stateDir, root string) *State {
	data, err := os.ReadFile(filepath.Join(stateDir, FileName(root)))
	if err != nil {
		return New(root)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil || s.Root != root {
		return New(root)
	}
	return &s
}

// Save writes the state atomically (temp file + rename).
func (s *State) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	s.LastUsed = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(stateDir, FileName(s.Root))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
