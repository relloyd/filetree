package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if cfg.General.ShowHidden || !cfg.General.ShowIgnored {
		t.Errorf("general toggles = %+v", cfg.General)
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
