// Package config loads ~/.filetree/config.toml: general defaults, named
// commands with execution modes, and keybinding overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
// only a screenful is drawn. "ctrl+g" in the finder multiplies it for the
// session when a search turns out to need more.
const DefaultFuzzyMaxMatches = 1000

// DefaultFuzzyMaxCandidates caps how many paths the finder's walk records.
// With a type filter set the cap is rarely reached — filtering happens during
// the walk, so "*.hcl" indexes a tree far larger than this many entries.
const DefaultFuzzyMaxCandidates = 50000

// DefaultRecentMax caps how many opened files the per-root history keeps. The
// list is read whole and matched in memory, so the cost of raising it is the
// state file's size rather than anything the user would feel.
const DefaultRecentMax = 100

// Bookmark defaults. A bookmark whose file has not been seen for the retention
// window is dropped — long enough that a file hidden by a branch switch is
// still there when you switch back, short enough that a rename or a deletion
// eventually stops cluttering the list.
const (
	DefaultBookmarkMax           = 500
	DefaultBookmarkRetentionDays = 30
)

// DefaultFuzzyGrepMaxPerFile caps how many content matches the finder takes
// from any one file, so a generated or minified file cannot fill the list.
// A configured 0 means no limit.
const DefaultFuzzyGrepMaxPerFile = 5

// Command is one command run against the selection: either a built-in from
// the catalogue in catalogue.go, or one the config defines itself.
type Command struct {
	// Name is the command's identity — the catalogue entry's name, or the
	// [commands.<name>] table it was read from. It is what [keys] and
	// sessions.new_command refer to, so it never comes from the file's body.
	Name string `toml:"-"`

	Run  string `toml:"run"`  // template; see ExpandCommand
	Mode string `toml:"mode"` // "interactive" or "background" (default)
	Key  string `toml:"key"`  // optional dedicated keybinding

	// FinderKey runs the command from inside the "/" finder, against the
	// highlighted result row rather than the tree selection. Chords only:
	// a bare key would be swallowed by the finder's text inputs.
	FinderKey string `toml:"finder_key"`

	// Desc is the one-line description the "?" help shows. The catalogue
	// fills it in for every built-in; a command defined in the config may set
	// its own, and is listed by name alone if it does not.
	Desc string `toml:"desc"`
}

// finderReservedKeys are the keys the finder handles itself, and so cannot be
// given away to a command's finder_key. The source of truth is the modeFuzzy
// switch in internal/app/app.go — keep the two in step.
var finderReservedKeys = []string{
	"esc", "enter", "up", "down", "pgup", "pgdown",
	"ctrl+p", "ctrl+n", "ctrl+u", "ctrl+d",
	"tab", "shift+tab", "ctrl+g", "ctrl+y", "ctrl+o",
	"ctrl+s", "ctrl+x", // the bookmark view's scope and forget keys
	"ctrl+w", "alt+n", // the session list's switch-client and new-session keys
}

type General struct {
	ShowHidden  bool `toml:"show_hidden"`
	ShowIgnored bool `toml:"show_ignored"`

	// StickyParents pins the parent directories of the topmost visible row
	// above the tree, the way an editor's sticky scroll pins the enclosing
	// scopes. On by default: a subtree scrolled away from its parents is the
	// case a narrow pane is worst at. The block takes its own space rather
	// than covering a row, so nothing is ever hidden underneath it, and it is
	// capped at a third of the pane so a deep tree cannot crowd itself out.
	StickyParents bool `toml:"sticky_parents"`

	Icons               string `toml:"icons"`    // "nerd" or "plain"
	LinkRef             string `toml:"link_ref"` // web links pin to "commit" or "branch"
	Tmux                string `toml:"tmux"`     // "auto" (relaunch inside tmux) or "never"
	FuzzyMaxMatches     int    `toml:"fuzzy_max_matches"`
	FuzzyMaxCandidates  int    `toml:"fuzzy_max_candidates"`
	FuzzyGrepMaxPerFile int    `toml:"fuzzy_grep_max_per_file"`
	RecentMax           int    `toml:"recent_max"`
	WatchDebounceMs     int    `toml:"watch_debounce_ms"`

	BookmarkMax           int `toml:"bookmark_max"`
	BookmarkRetentionDays int `toml:"bookmark_retention_days"`

	// ClearMarksAfterCommand drops the marked set once a command has acted on
	// it. Off by default: opening files is not destructive, so the marks are
	// worth keeping for the next command. Turn it on to match d/p/m, which do
	// clear.
	ClearMarksAfterCommand bool `toml:"clear_marks_after_command"`
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

// Sessions configures the named tmux sessions agent tools run in ("T"), which
// are laid out as <prefix><repo>/<branch>/<tool>.
type Sessions struct {
	// Prefix is what marks a session as belonging to this feature, and is the
	// only thing the picker filters on. Everything ft creates for itself — the
	// self-relaunch, the splits, the popups — is unnamed, so the prefix is what
	// keeps the picker to sessions worth coming back to.
	Prefix string `toml:"prefix"`

	// NewCommand is the command "alt+n" runs from inside the picker to start a
	// session for the current selection. Naming it is what makes that key
	// predictable: several commands create sessions, and picking between them
	// by any rule of ft's own would be a guess. Empty falls back to whichever
	// session-creating command sorts first, so the key still does something.
	NewCommand string `toml:"new_command"`
}

type Config struct {
	General        General
	Scratch        Scratch
	Worktrees      Worktrees
	Sessions       Sessions
	DefaultCommand string // name in Commands that Enter runs
	Commands       map[string]Command

	// CommandOrder is every command in Commands, catalogue order first and
	// any the config added after it. The "?" help reads it so the page keeps
	// a deliberate order rather than an alphabetical one.
	CommandOrder []string

	// Keys overrides the key of an action or of a command, by name. Both live
	// in one namespace: a key belongs to one thing, and resolveActionKeys
	// settles the whole set together so a clash between a command and an
	// action is reported like any other.
	Keys map[string]string

	// Unknown is every setting in the file that decoded into nothing, in
	// dotted form ("commands.diff.worktree-new"). Not an error — the rest of
	// the config is perfectly usable — but never silent either: see Load.
	Unknown []string
}

func Default() *Config {
	commands, order := builtinCommands()
	return &Config{
		General: General{
			ShowHidden:    false,
			ShowIgnored:   true,
			StickyParents: true,

			Icons:                 "nerd",
			LinkRef:               "commit",
			Tmux:                  tmux.ModeAuto,
			FuzzyMaxMatches:       DefaultFuzzyMaxMatches,
			FuzzyMaxCandidates:    DefaultFuzzyMaxCandidates,
			FuzzyGrepMaxPerFile:   DefaultFuzzyGrepMaxPerFile,
			RecentMax:             DefaultRecentMax,
			BookmarkMax:           DefaultBookmarkMax,
			BookmarkRetentionDays: DefaultBookmarkRetentionDays,
			WatchDebounceMs:       150,
		},
		Scratch: Scratch{
			Dir:       "~/.filetree/scratch",
			Extension: "md",
		},
		Worktrees: Worktrees{
			Dir: "~/.filetree/worktrees",
		},
		Sessions: Sessions{
			Prefix: tmux.DefaultPrefix,
			// The catalogue guarantees this exists, so "alt+n" in the session
			// list works with nothing configured at all.
			NewCommand: "claude-popup",
		},
		DefaultCommand: DefaultBuiltinCommand,
		Commands:       commands,
		CommandOrder:   order,
		Keys:           map[string]string{},
	}
}

// RetentionWindow is how long a bookmark survives without its file being
// found. A configured zero means never expire, which the bookmark store reads
// from a non-positive duration.
func (c *Config) RetentionWindow() time.Duration {
	return time.Duration(c.General.BookmarkRetentionDays) * 24 * time.Hour
}

// mergeCommands folds the file's [commands] table into the catalogue already
// sitting in cfg.
//
// This is the whole difference between a config that ages well and one that
// does not. The built-ins are the starting point, and a [commands.<name>]
// table *overlays* only the fields it actually sets rather than replacing the
// command — so `key = "C"` moves a binding and keeps the run, and a command
// added to a later ft appears without the file being touched at all. This used
// to replace the entire set with whatever the file listed, which meant a newly
// shipped command was invisible to anyone who had run ft even once.
//
// Two keys in the table are not commands: `default` names the command Enter
// runs, and `disabled` removes built-ins outright, freeing their keys.
func mergeCommands(cfg *Config, md toml.MetaData, raw map[string]toml.Primitive, path string) error {
	if len(raw) == 0 {
		return nil
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	// Sorted so that a file with two mistakes in it always reports the same
	// one first, whatever order Go's map iteration happens to take.
	slices.Sort(names)

	var disabled []string
	for _, name := range names {
		prim := raw[name]
		switch name {
		case "default":
			if err := md.PrimitiveDecode(prim, &cfg.DefaultCommand); err != nil {
				return fmt.Errorf("%s: commands.default: %w", path, err)
			}
			continue
		case "disabled":
			if err := md.PrimitiveDecode(prim, &disabled); err != nil {
				return fmt.Errorf("%s: commands.disabled: %w", path, err)
			}
			continue
		}

		// Starting from the built-in is what makes this an overlay:
		// PrimitiveDecode assigns only the fields the table mentions and
		// leaves the rest alone. An explicit `key = ""` still comes through,
		// which is how a built-in is unbound without being removed.
		c, known := cfg.Commands[name]
		if err := md.PrimitiveDecode(prim, &c); err != nil {
			return fmt.Errorf("%s: commands.%s: %w", path, name, err)
		}
		c.Name = name
		if c.Run == "" {
			return fmt.Errorf("%s: commands.%s: missing run", path, name)
		}
		if c.Mode == "" {
			c.Mode = ModeBackground
		}
		if c.Mode != ModeInteractive && c.Mode != ModeBackground {
			return fmt.Errorf("%s: commands.%s: mode must be %q or %q", path, name, ModeInteractive, ModeBackground)
		}
		if slices.Contains(finderReservedKeys, c.FinderKey) {
			return fmt.Errorf("%s: commands.%s: finder_key %q is reserved by the finder", path, name, c.FinderKey)
		}
		cfg.Commands[name] = c
		if !known {
			cfg.CommandOrder = append(cfg.CommandOrder, name)
		}
	}

	for _, name := range disabled {
		if _, ok := cfg.Commands[name]; !ok {
			return fmt.Errorf("%s: commands.disabled: %q is not a command", path, name)
		}
		delete(cfg.Commands, name)
		cfg.CommandOrder = slices.DeleteFunc(cfg.CommandOrder, func(n string) bool { return n == name })
	}
	return nil
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
		if err := os.WriteFile(path, []byte(Starter()), 0o644); err != nil {
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
		Sessions  *Sessions                 `toml:"sessions"`
		Commands  map[string]toml.Primitive `toml:"commands"`
		Keys      map[string]string         `toml:"keys"`
	}
	raw.General = &cfg.General // decode over defaults
	raw.Scratch = &cfg.Scratch
	raw.Worktrees = &cfg.Worktrees
	raw.Sessions = &cfg.Sessions
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
	// Zero is meaningful here — it means "every match in a file" — so only a
	// negative value is a mistake.
	if cfg.General.FuzzyGrepMaxPerFile < 0 {
		return nil, fmt.Errorf("%s: general.fuzzy_grep_max_per_file must not be negative", path)
	}
	// Unlike the grep cap, zero is not a useful setting here: it would keep a
	// history that can never hold anything.
	if cfg.General.RecentMax < 1 {
		return nil, fmt.Errorf("%s: general.recent_max must be at least 1", path)
	}
	if cfg.General.BookmarkMax < 1 {
		return nil, fmt.Errorf("%s: general.bookmark_max must be at least 1", path)
	}
	// Zero is meaningful: it turns ageing out off, so a bookmark survives until
	// it is forgotten by hand.
	if cfg.General.BookmarkRetentionDays < 0 {
		return nil, fmt.Errorf("%s: general.bookmark_retention_days must not be negative", path)
	}
	cfg.Scratch.Extension = strings.TrimPrefix(cfg.Scratch.Extension, ".")
	if cfg.Scratch.Dir == "" {
		return nil, fmt.Errorf("%s: scratch.dir must not be empty", path)
	}
	if cfg.Worktrees.Dir == "" {
		return nil, fmt.Errorf("%s: worktrees.dir must not be empty", path)
	}
	// An empty prefix would match every session on the server — ft's own
	// wrapper session, every popup, everything the user has open outside ft —
	// which is exactly the pollution the naming convention exists to avoid.
	if cfg.Sessions.Prefix == "" {
		return nil, fmt.Errorf("%s: sessions.prefix must not be empty", path)
	}

	if err := mergeCommands(cfg, md, raw.Commands, path); err != nil {
		return nil, err
	}
	if _, ok := cfg.Commands[cfg.DefaultCommand]; !ok {
		return nil, fmt.Errorf("%s: commands.default %q is not a defined command", path, cfg.DefaultCommand)
	}
	// Checked here rather than at the [sessions] block above because it names
	// a command, and the commands are only known once the pass above has run.
	if n := cfg.Sessions.NewCommand; n != "" {
		if _, ok := cfg.Commands[n]; !ok {
			return nil, fmt.Errorf("%s: sessions.new_command %q is not a defined command", path, n)
		}
	}

	// Whatever the decode above never reached. A mistyped setting is one way to
	// get here; the commoner one is a keybinding written under a [keys] header
	// that is still commented out, which TOML files under whichever table came
	// last — a command, usually — where it is a field that does not exist. Both
	// look exactly like a setting that works and does nothing at all.
	//
	// Collected last, after the PrimitiveDecode pass over [commands]: md only
	// counts a key as decoded once something has actually asked for it.
	//
	// It is a warning rather than an error because the config is still usable,
	// and because refusing to start would take away the one comfortable way of
	// fixing it — "C" opens this file in the editor and reloads on the way out.
	for _, key := range md.Undecoded() {
		cfg.Unknown = append(cfg.Unknown, key.String())
	}
	slices.Sort(cfg.Unknown)
	return cfg, nil
}
