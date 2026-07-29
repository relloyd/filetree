package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relloyd/filetree/internal/tmux"
)

func loadTOML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// The starter config we ship must always parse.
func TestLoadStarter(t *testing.T) {
	cfg, err := loadTOML(t, starterTOML)
	if err != nil {
		t.Fatalf("starter config failed to load: %v", err)
	}
	if cfg.DefaultCommand != "edit" {
		t.Errorf("default command = %q, want edit", cfg.DefaultCommand)
	}
	edit, ok := cfg.Commands["edit"]
	if !ok || edit.Mode != ModeInteractive {
		t.Errorf("edit command = %+v ok=%v, want interactive", edit, ok)
	}
	if h := cfg.Commands["handoff"]; h.Key != "t" || h.Mode != ModeBackground {
		t.Errorf("handoff = %+v, want key t, background", h)
	}
	// Command keys must not collide with an action default: actions win, so a
	// clash would silently shadow the command.
	if g := cfg.Commands["grep-here"]; g.Key != "r" {
		t.Errorf("grep-here key = %q, want r", g.Key)
	}
	if s := cfg.Commands["shell-here"]; s.Key != "n" || s.Mode != ModeBackground {
		t.Errorf("shell-here = %+v, want key n, background", s)
	}
	// "tab" is only free in the main view because finder-next-field is kept out
	// of buildBindings' actions map; see TestACommandCanOwnTab in internal/app.
	if f := cfg.Commands["focus-right"]; f.Key != "tab" || f.Mode != ModeBackground {
		t.Errorf("focus-right = %+v, want key tab, background", f)
	}
	if cfg.General.ShowHidden || !cfg.General.ShowIgnored {
		t.Errorf("general toggles = %+v", cfg.General)
	}
	// The starter leaves tmux commented out, so the default must be the one
	// that makes the tmux commands above work out of the box.
	if cfg.General.Tmux != tmux.ModeAuto {
		t.Errorf("general.tmux = %q, want %q", cfg.General.Tmux, tmux.ModeAuto)
	}
	// fuzzy_max_matches is commented out in the starter, so the default applies.
	if cfg.General.FuzzyMaxMatches != DefaultFuzzyMaxMatches {
		t.Errorf("general.fuzzy_max_matches = %d, want %d", cfg.General.FuzzyMaxMatches, DefaultFuzzyMaxMatches)
	}
	if cfg.General.RecentMax != DefaultRecentMax {
		t.Errorf("general.recent_max = %d, want %d", cfg.General.RecentMax, DefaultRecentMax)
	}
}

func TestLoadValidation(t *testing.T) {
	if _, err := loadTOML(t, "[commands]\ndefault = \"nope\"\n"); err == nil {
		t.Error("undefined default command should fail")
	}
	if _, err := loadTOML(t, "[commands.x]\nrun = \"true\"\nmode = \"weird\"\n[commands]\ndefault = \"x\"\n"); err == nil {
		t.Error("invalid mode should fail")
	}
	if _, err := loadTOML(t, "[general]\nicons = \"emoji\"\n"); err == nil {
		t.Error("invalid icons value should fail")
	}
	if _, err := loadTOML(t, "[general]\ntmux = \"always\"\n"); err == nil {
		t.Error("invalid tmux value should fail")
	}
	// A zero cap would leave the finder permanently empty; catch it at load.
	if _, err := loadTOML(t, "[general]\nfuzzy_max_matches = 0\n"); err == nil {
		t.Error("zero fuzzy_max_matches should fail")
	}
	if _, err := loadTOML(t, "[general]\nfuzzy_max_matches = -5\n"); err == nil {
		t.Error("negative fuzzy_max_matches should fail")
	}
	if cfg, err := loadTOML(t, "[general]\nfuzzy_max_matches = 500\n"); err != nil {
		t.Errorf("fuzzy_max_matches = 500 should load: %v", err)
	} else if cfg.General.FuzzyMaxMatches != 500 {
		t.Errorf("fuzzy_max_matches = %d, want 500", cfg.General.FuzzyMaxMatches)
	}
	// Unlike fuzzy_grep_max_per_file, zero is not "no limit" here — it is a
	// history that can never hold anything.
	if _, err := loadTOML(t, "[general]\nrecent_max = 0\n"); err == nil {
		t.Error("zero recent_max should fail")
	}
	if cfg, err := loadTOML(t, "[general]\nrecent_max = 25\n"); err != nil {
		t.Errorf("recent_max = 25 should load: %v", err)
	} else if cfg.General.RecentMax != 25 {
		t.Errorf("recent_max = %d, want 25", cfg.General.RecentMax)
	}
	if cfg, err := loadTOML(t, "[general]\ntmux = \"never\"\n"); err != nil {
		t.Errorf("tmux = never should load: %v", err)
	} else if cfg.General.Tmux != tmux.ModeNever {
		t.Errorf("general.tmux = %q, want %q", cfg.General.Tmux, tmux.ModeNever)
	}
}

func TestLoadPartialOverridesKeepDefaults(t *testing.T) {
	cfg, err := loadTOML(t, "[general]\nshow_hidden = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.General.ShowHidden {
		t.Error("show_hidden override lost")
	}
	if cfg.General.WatchDebounceMs != 150 || cfg.General.Icons != "nerd" {
		t.Errorf("defaults not preserved: %+v", cfg.General)
	}
	if _, ok := cfg.Commands["edit"]; !ok {
		t.Error("default edit command missing when [commands] absent")
	}
}

func TestExpandCommand(t *testing.T) {
	v := Vars{
		Path:    "/home/rl/my repo/file's.go",
		RelPath: "src/main.go",
		Dir:     "/home/rl/proj",
		Root:    "/home/rl",
		Name:    "main.go",
	}
	got := ExpandCommand(`tmux send-keys -t "{last}" "hx {relpath}" Enter`, v)
	want := `tmux send-keys -t "{last}" "hx src/main.go" Enter`
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	got = ExpandCommand("hx {path}", v)
	want = `hx '/home/rl/my repo/file'\''s.go'`
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// {line} carries a finder Grep hit's line through to the editor. It falls back
// to 1 rather than 0 so that one template works everywhere: "hx f.go:0" is an
// error, "hx f.go:1" is just the top of the file.
func TestExpandCommandLine(t *testing.T) {
	v := Vars{Path: "/src/main.go", Line: 42}
	if got, want := ExpandCommand("hx {path}:{line}", v), "hx /src/main.go:42"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	v.Line = 0
	if got, want := ExpandCommand("hx {path}:{line}", v), "hx /src/main.go:1"; got != want {
		t.Errorf("no line: got %q, want %q", got, want)
	}
}

// A finder_key the finder handles itself would never fire, so it is rejected
// at load rather than silently ignored.
func TestFinderKeyReserved(t *testing.T) {
	const tmpl = `
[commands]
default = "edit"
[commands.edit]
run = "hx {path}"
finder_key = %q
`
	if _, err := loadTOML(t, fmt.Sprintf(tmpl, "enter")); err == nil {
		t.Error("finder_key = enter loaded; want an error")
	} else if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error = %v, want it to mention the key is reserved", err)
	}

	cfg, err := loadTOML(t, fmt.Sprintf(tmpl, "ctrl+e"))
	if err != nil {
		t.Fatalf("finder_key = ctrl+e: %v", err)
	}
	if got := cfg.Commands["edit"].FinderKey; got != "ctrl+e" {
		t.Errorf("finder_key = %q, want ctrl+e", got)
	}
}

func TestScratchConfig(t *testing.T) {
	cfg, err := loadTOML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scratch.Dir != "~/.filetree/scratch" || cfg.Scratch.Extension != "md" {
		t.Errorf("defaults = %+v", cfg.Scratch)
	}

	cfg, err = loadTOML(t, "[scratch]\ndir = \"~/notes\"\nextension = \".txt\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scratch.Dir != "~/notes" {
		t.Errorf("dir override = %q", cfg.Scratch.Dir)
	}
	if cfg.Scratch.Extension != "txt" {
		t.Errorf("leading dot should be stripped, got %q", cfg.Scratch.Extension)
	}

	if _, err := loadTOML(t, "[scratch]\ndir = \"\"\n"); err == nil {
		t.Error("empty scratch.dir should fail")
	}
}

func TestWorktreesConfig(t *testing.T) {
	cfg, err := loadTOML(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worktrees.Dir != "~/.filetree/worktrees" {
		t.Errorf("default = %+v", cfg.Worktrees)
	}

	cfg, err = loadTOML(t, "[worktrees]\ndir = \"~/code/wt\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worktrees.Dir != "~/code/wt" {
		t.Errorf("dir override = %q", cfg.Worktrees.Dir)
	}

	if _, err := loadTOML(t, "[worktrees]\ndir = \"\"\n"); err == nil {
		t.Error("empty worktrees.dir should fail")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/x/y"); got != filepath.Join(home, "x", "y") {
		t.Errorf("ExpandHome(~/x/y) = %q", got)
	}
	if got := ExpandHome("/abs/path"); got != "/abs/path" {
		t.Errorf("absolute path should pass through, got %q", got)
	}
	if got := ExpandHome("relative/~x"); got != "relative/~x" {
		t.Errorf("mid-string tilde should pass through, got %q", got)
	}
}

func TestExpandMarked(t *testing.T) {
	v := Vars{Marked: []string{"/a/old.go", "/b/has space.go", "/c/new.go"}}
	got := ExpandCommand("diff {marked1} {marked2}", v)
	if got != `diff '/b/has space.go' /c/new.go` {
		t.Errorf("got %q", got)
	}
	got = ExpandCommand("open {marked}", v)
	if got != `open /a/old.go '/b/has space.go' /c/new.go` {
		t.Errorf("got %q", got)
	}

	// Fewer than two marks: marked1/marked2 become empty quotes.
	got = ExpandCommand("diff {marked1} {marked2}", Vars{Marked: []string{"/only.go"}})
	if got != "diff '' ''" {
		t.Errorf("got %q", got)
	}
	if got := ExpandCommand("x {marked}", Vars{}); got != "x " {
		t.Errorf("got %q", got)
	}
}

// {paths} is what a command acts on, and it carries the position itself
// because a list cannot take one ":42" suffix between them.
func TestExpandPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    Vars
		want string
	}{{
		name: "a list is plain, in the order given",
		v:    Vars{Paths: []string{"/a/one.go", "/b/has space.go", "/c/three.go"}},
		want: `hx /a/one.go '/b/has space.go' /c/three.go`,
	}, {
		name: "a single target carries the line it was found on",
		v:    Vars{Paths: []string{"/a/one.go"}, Line: 42},
		want: "hx /a/one.go:42",
	}, {
		name: "no line means no suffix, unlike {line}'s own 0 to 1 fallback",
		v:    Vars{Paths: []string{"/a/one.go"}},
		want: "hx /a/one.go",
	}, {
		// A list with a line would otherwise hang ":42" off the last path
		// alone, which reads as though it applied to all of them.
		name: "a line is dropped for a list",
		v:    Vars{Paths: []string{"/a/one.go", "/b/two.go"}, Line: 42},
		want: "hx /a/one.go /b/two.go",
	}, {
		name: "empty Paths falls back to the single Path",
		v:    Vars{Path: "/a/one.go", Line: 7},
		want: "hx /a/one.go:7",
	}, {
		name: "nothing at all expands to nothing",
		v:    Vars{},
		want: "hx ",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpandCommand("hx {paths}", tc.v); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// {paths} is listed before {path} in the Replacer; the other order would
	// substitute {path} inside it and leave a stray "s}".
	got := ExpandCommand("{path} then {paths}", Vars{Path: "/a/one.go", Paths: []string{"/b/two.go"}})
	if got != "/a/one.go then /b/two.go" {
		t.Errorf("got %q", got)
	}
}

// UsesMarks decides whether running a command consumes the marked set, so it
// has to agree with what ExpandCommand actually substitutes.
func TestUsesMarks(t *testing.T) {
	for tmpl, want := range map[string]bool{
		"hx {paths}":                            true,
		"delta {marked1} {marked2}":             true,
		"open {marked}":                         true,
		"hx {path}:{line}":                      false,
		"tmux split-window -c {dir}":            false,
		`[ -z "$TMUX" ] || tmux select-pane -R`: false,
	} {
		if got := UsesMarks(tmpl); got != want {
			t.Errorf("UsesMarks(%q) = %v, want %v", tmpl, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":               "''",
		"/plain/path.go": "/plain/path.go",
		"has space":      "'has space'",
		"a'b":            `'a'\''b'`,
		"$HOME":          "'$HOME'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
