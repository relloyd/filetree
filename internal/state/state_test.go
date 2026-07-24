package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New("/some/root")
	s.Expanded = []string{".", "cmd", "internal/app"}
	s.Selected = "internal/app/model.go"
	s.ScrollOffset = 7
	hidden := true
	s.ShowHidden = &hidden
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	got := Load(dir, "/some/root")
	if got.Selected != s.Selected || got.ScrollOffset != 7 {
		t.Errorf("got %+v", got)
	}
	if len(got.Expanded) != 3 || got.Expanded[2] != "internal/app" {
		t.Errorf("expanded = %v", got.Expanded)
	}
	if got.ShowHidden == nil || !*got.ShowHidden || got.ShowIgnored != nil {
		t.Errorf("toggles: hidden=%v ignored=%v", got.ShowHidden, got.ShowIgnored)
	}
}

func TestLoadMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	if s := Load(dir, "/nope"); len(s.Expanded) != 1 || s.Expanded[0] != "." {
		t.Errorf("fresh state = %+v", s)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName("/bad")), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Load(dir, "/bad"); s.Root != "/bad" || len(s.Expanded) != 1 {
		t.Errorf("corrupt file should yield fresh state, got %+v", s)
	}
}

func TestFileName(t *testing.T) {
	a := FileName("/Users/rl/My Project!")
	if !strings.HasPrefix(a, "My_Project_-") || !strings.HasSuffix(a, ".json") {
		t.Errorf("FileName = %q", a)
	}
	if a != FileName("/Users/rl/My Project!") {
		t.Error("not stable")
	}
	// Same basename, different parents must not collide.
	if FileName("/a/proj") == FileName("/b/proj") {
		t.Error("collision for same basename under different parents")
	}
}
