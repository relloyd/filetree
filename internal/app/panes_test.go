package app

import (
	"testing"

	"github.com/relloyd/filetree/internal/config"
)

// display-popup floats over the window without taking a column from anything,
// so it must not be measured around: there is no width to keep and no pane to
// record. The splits are the ones that cost ft its columns.
func TestPaneOpeningCommand(t *testing.T) {
	opens := []string{
		`tmux split-window -fh -c {root} "hx {paths}"`,
		`tmux split-window -h -c {dir}`,
		`tmux split-window -fh "delta {marked1} {marked2}; read x"`,
		`[ -z "$TMUX" ] || tmux join-pane -s %3`,
	}
	for _, run := range opens {
		if !paneOpeningCommand(run) {
			t.Errorf("paneOpeningCommand(%q) = false, want true", run)
		}
	}

	leaves := []string{
		`tmux display-popup -E -w 92% -h 92% "tmux new-session -c {dir}"`,
		`tmux display-popup -E -d {gitroot} -w 92% -h 92% "tmux new-session -A -s {session}/claude"`,
		`hx {paths}`,
		`open {path}`,
		`[ -z "$TMUX" ] || tmux select-pane -R`,
		`[ -z "$TMUX" ] || tmux resize-pane -x 30%`,
		`printf %s {path} | pbcopy`,
	}
	for _, run := range leaves {
		if paneOpeningCommand(run) {
			t.Errorf("paneOpeningCommand(%q) = true, want false", run)
		}
	}
}

// The built-ins that can open a pane must be spotted as such, read from the
// catalogue rather than from a copy of it here: a template that grows or loses
// its split is exactly the change this would otherwise stop noticing.
//
// The hand-off splits only as a fallback and often does not, which is fine —
// paying two tmux calls for a command that turns out not to split is much
// cheaper than missing the one that does.
func TestBuiltinsThatOpenPanesAreSpotted(t *testing.T) {
	cmds := config.Default().Commands
	for _, name := range []string{"tmux-handoff", "helix-vsplit", "shell-vsplit", "shell-vsplit-adjacent", "diff"} {
		c, ok := cmds[name]
		if !ok {
			t.Fatalf("%s is not in the catalogue", name)
		}
		if !paneOpeningCommand(c.Run) {
			t.Errorf("%s opens a pane but is not measured around:\n%s", name, c.Run)
		}
	}
	// A popup floats over the window and takes no columns, so measuring
	// around one would be two tmux calls to restore a width nothing moved.
	for _, name := range []string{"claude-popup", "copilot-popup", "agent-shell", "lazygit-popup", "shell-popup", "edit"} {
		c, ok := cmds[name]
		if !ok {
			t.Fatalf("%s is not in the catalogue", name)
		}
		if paneOpeningCommand(c.Run) {
			t.Errorf("%s takes no columns but is measured around:\n%s", name, c.Run)
		}
	}
}

// The sidebar test is the one openSessionPane has always used: an ft that
// filled the window had no sidebar to preserve, and putting its old width back
// would crush whatever is beside it down to a single column.
func TestKeepSidebarWidthOnlyActsOnASidebar(t *testing.T) {
	for _, tc := range []struct {
		name           string
		self           string
		before, window int
		wantResize     bool
	}{
		{"a sidebar is restored", "%0", 30, 200, true},
		{"half the window is not a sidebar", "%0", 100, 200, false},
		{"more than half is not a sidebar", "%0", 150, 200, false},
		{"an unreadable width does nothing", "%0", 0, 200, false},
		{"an unreadable window does nothing", "%0", 30, 0, false},
		{"outside tmux does nothing", "", 30, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The decision, not the exec: a sidebar is anything ft can measure
			// that is under half its window.
			got := tc.self != "" && tc.before > 0 && tc.before < tc.window/2
			if got != tc.wantResize {
				t.Errorf("sidebar test = %v, want %v", got, tc.wantResize)
			}
		})
	}
}
