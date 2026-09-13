package app

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
)

// clipRecorder is a platform whose clipboard is a field, so a test can read
// back what an action copied. Nothing else on it is expected to be called.
type clipRecorder struct{ text string }

func (c *clipRecorder) CopyToClipboard(s string) error { c.text = s; return nil }
func (*clipRecorder) Reveal(string) error              { return nil }
func (*clipRecorder) OpenURL(string) error             { return nil }
func (*clipRecorder) Trash(string) error               { return nil }

// "r" copies the rg rather than typing it into a tmux pane, so it reaches any
// shell. It aims at a directory's own path and at a file's parent, and the path
// is quoted, since a shell is the only place the command is going.
func TestCopyGrepAimsAtTheSelectedDir(t *testing.T) {
	m, project := rootModel(t)
	spaced := filepath.Join(project, "has space")
	if err := os.Mkdir(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = m.tr.Refresh(m.tr.Root)
	m.reflatten()
	clip := &clipRecorder{}
	m.plat = clip

	for _, tc := range []struct {
		name, selection, want string
	}{
		{"a directory is its own target", spaced, "rg -n '" + spaced + "' -e "},
		{"a file searches its parent", filepath.Join(project, "top.txt"), "rg -n " + config.ShellQuote(project) + " -e "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clip.text = ""
			m.selectPath(tc.selection)
			if sel := m.selected(); sel == nil || sel.Path != tc.selection {
				t.Fatalf("selection = %v, want %s", sel, tc.selection)
			}
			m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
			if clip.text != tc.want {
				t.Errorf("copied %q, want %q", clip.text, tc.want)
			}
		})
	}
}
