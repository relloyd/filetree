// Package config loads ~/.filetree/config.toml: general defaults, named
// commands with execution modes, and keybinding overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/relloyd/filetree/internal/tmux"
)

// ExpandHome resolves a leading ~ to the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

const (
	ModeInteractive = "interactive" // suspend the TUI while the command runs
	ModeBackground  = "background"  // fire and forget, errors to status bar
)

// DefaultFuzzyMaxMatches caps how many fuzzy-find results are ranked and kept.
// The cost of raising it is the sort in rerankMatches, not the render, since
// only a screenful is drawn.
const DefaultFuzzyMaxMatches = 200

// DefaultFuzzyMaxCandidates caps how many paths the finder's walk records.
// With a type filter set the cap is rarely reached — filtering happens during
// the walk, so "*.hcl" indexes a tree far larger than this many entries.
const DefaultFuzzyMaxCandidates = 50000

// Command is a named, user-configured command run against the selection.
type Command struct {
	Run  string `toml:"run"`  // template; see ExpandCommand
	Mode string `toml:"mode"` // "interactive" or "background" (default)
	Key  string `toml:"key"`  // optional dedicated keybinding
}

type General struct {
	ShowHidden         bool   `toml:"show_hidden"`
	ShowIgnored        bool   `toml:"show_ignored"`
	Icons              string `toml:"icons"`    // "nerd" or "plain"
	LinkRef            string `toml:"link_ref"` // web links pin to "commit" or "branch"
	Tmux               string `toml:"tmux"`     // "auto" (relaunch inside tmux) or "never"
	FuzzyMaxMatches    int    `toml:"fuzzy_max_matches"`
	FuzzyMaxCandidates int    `toml:"fuzzy_max_candidates"`
	WatchDebounceMs    int    `toml:"watch_debounce_ms"`
}

// Scratch configures the scratch-file directory ("n" / "S" keys).
type Scratch struct {
	Dir       string `toml:"dir"`       // supports ~; created on demand
	Extension string `toml:"extension"` // without dot; "" for none
}

// Worktrees configures where git worktrees live ("W" / "w" keys); they are
// laid out as <dir>/<repo basename>/<branch or pr-N>.
type Worktrees struct {
	Dir string `toml:"dir"` // supports ~; created on demand
}

type Config struct {
	General        General
	Scratch        Scratch
	Worktrees      Worktrees
	DefaultCommand string // name in Commands that Enter runs
	Commands       map[string]Command
	Keys           map[string]string // action name -> key
}

func Default() *Config {
	return &Config{
		General: General{
			ShowHidden:         false,
			ShowIgnored:        true,
			Icons:              "nerd",
			LinkRef:            "commit",
			Tmux:               tmux.ModeAuto,
			FuzzyMaxMatches:    DefaultFuzzyMaxMatches,
			FuzzyMaxCandidates: DefaultFuzzyMaxCandidates,
			WatchDebounceMs:    150,
		},
		Scratch: Scratch{
			Dir:       "~/.filetree/scratch",
			Extension: "md",
		},
		Worktrees: Worktrees{
			Dir: "~/.filetree/worktrees",
		},
		DefaultCommand: "edit",
		Commands: map[string]Command{
			"edit": {Run: "hx {path}", Mode: ModeInteractive},
		},
		Keys: map[string]string{},
	}
}

// Dir returns ~/.filetree, creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".filetree")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// EnsureAndLoad loads dir/config.toml, writing the commented starter config
// first if none exists yet.
func EnsureAndLoad(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(starterTOML), 0o644); err != nil {
			return nil, fmt.Errorf("write starter config: %w", err)
		}
	}
	return Load(path)
}

func Load(path string) (*Config, error) {
	cfg := Default()
	var raw struct {
		General   *General                  `toml:"general"`
		Scratch   *Scratch                  `toml:"scratch"`
		Worktrees *Worktrees                `toml:"worktrees"`
		Commands  map[string]toml.Primitive `toml:"commands"`
		Keys      map[string]string         `toml:"keys"`
	}
	raw.General = &cfg.General // decode over defaults
	raw.Scratch = &cfg.Scratch
	raw.Worktrees = &cfg.Worktrees
	md, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if raw.Keys != nil {
		cfg.Keys = raw.Keys
	}
	if cfg.General.Icons != "nerd" && cfg.General.Icons != "plain" {
		return nil, fmt.Errorf("%s: general.icons must be \"nerd\" or \"plain\"", path)
	}
	if cfg.General.LinkRef != "commit" && cfg.General.LinkRef != "branch" {
		return nil, fmt.Errorf("%s: general.link_ref must be \"commit\" or \"branch\"", path)
	}
	if cfg.General.Tmux != tmux.ModeAuto && cfg.General.Tmux != tmux.ModeNever {
		return nil, fmt.Errorf("%s: general.tmux must be %q or %q", path, tmux.ModeAuto, tmux.ModeNever)
	}
	// A non-positive cap would mean the finder can never show a result, which
	// is always a mistake rather than an intent worth honouring.
	if cfg.General.FuzzyMaxMatches < 1 {
		return nil, fmt.Errorf("%s: general.fuzzy_max_matches must be at least 1", path)
	}
	if cfg.General.FuzzyMaxCandidates < 1 {
		return nil, fmt.Errorf("%s: general.fuzzy_max_candidates must be at least 1", path)
	}
	cfg.Scratch.Extension = strings.TrimPrefix(cfg.Scratch.Extension, ".")
	if cfg.Scratch.Dir == "" {
		return nil, fmt.Errorf("%s: scratch.dir must not be empty", path)
	}
	if cfg.Worktrees.Dir == "" {
		return nil, fmt.Errorf("%s: worktrees.dir must not be empty", path)
	}

	// [commands] mixes `default = "name"` with per-command sub-tables, so it
	// is decoded in two passes via toml.Primitive.
	if len(raw.Commands) > 0 {
		cfg.Commands = map[string]Command{}
		for name, prim := range raw.Commands {
			if name == "default" {
				if err := md.PrimitiveDecode(prim, &cfg.DefaultCommand); err != nil {
					return nil, fmt.Errorf("%s: commands.default: %w", path, err)
				}
				continue
			}
			var c Command
			if err := md.PrimitiveDecode(prim, &c); err != nil {
				return nil, fmt.Errorf("%s: commands.%s: %w", path, name, err)
			}
			if c.Run == "" {
				return nil, fmt.Errorf("%s: commands.%s: missing run", path, name)
			}
			if c.Mode == "" {
				c.Mode = ModeBackground
			}
			if c.Mode != ModeInteractive && c.Mode != ModeBackground {
				return nil, fmt.Errorf("%s: commands.%s: mode must be %q or %q", path, name, ModeInteractive, ModeBackground)
			}
			cfg.Commands[name] = c
		}
	}
	if _, ok := cfg.Commands[cfg.DefaultCommand]; !ok {
		return nil, fmt.Errorf("%s: commands.default %q is not a defined command", path, cfg.DefaultCommand)
	}
	return cfg, nil
}
