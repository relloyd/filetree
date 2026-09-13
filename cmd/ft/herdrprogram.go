package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/relloyd/filetree/internal/herdr"
)

// program is something one of the herdr keys puts in front of you: what to look
// for among herdr's panes, what to run when there is none, and what to call the
// tab it lands in.
//
// The keys differ in less than they look. Each one finds the place under the
// selection, looks for its program there, reuses it or makes somewhere for it,
// and reports. Only the three fields below change, so they are the only thing
// each key has to say.
type program struct {
	// Argv0 is how the program appears in a pane's process list. It is matched
	// against argv0 rather than the process name on purpose — see
	// herdr.ProcessInfo.Runs.
	Argv0 string

	// Label is what a tab opened for it is called, and defaults to Argv0. Two
	// keys can share a binary and still want different labels: "lazygit" and
	// "blame view.go" are both lazygit.
	Label string

	// Match decides whether a pane is this program's, and defaults to running
	// Argv0 at all. A key whose binary is also another key's needs more than
	// the name — see lazygit and its blame view, which differ only by the
	// arguments they were started with.
	Match func(info herdr.ProcessInfo) bool

	// Command builds the shell command that starts it, from the paths the key
	// was given. A program that takes no paths ignores them.
	Command func(paths []string) string

	// Reuse hands paths to an instance that is already running. Nil means there
	// is nothing to say to one, and finding it is the whole answer: lazygit
	// shows a repository, and it is already showing it.
	Reuse func(c *herdr.Client, pane string, paths []string) error

	// NeedsRepo refuses the key outside a checkout, for a program that has
	// nothing to show there.
	NeedsRepo bool
}

// runHerdrProgram is the body every herdr key shares.
//
// Three outcomes, in order:
//
//  1. the program is already running for this place — reuse it, which for an
//     editor means handing it the files and for lazygit means nothing at all;
//  2. the place has a workspace but no such program — a tab of its own there;
//  3. herdr has never seen this place — a workspace for it.
//
// It always moves you. The tmux hand-off can afford to stay in the tree because
// the pane it talks to is in the same window, already on screen. Here the target
// may be a workspace away, and sending something somewhere invisible is not a
// hand-off, it is a disappearance.
func runHerdrProgram(p program, dir string, paths []string) error {
	dir = filepath.Clean(dir)
	home, _ := os.UserHomeDir()
	scope := herdr.ScopeFor(dir, checkoutFor(dir, home))

	if p.NeedsRepo && !scope.Repo {
		return fmt.Errorf("%s needs a git repo — %s is not in one", p.Argv0, dir)
	}

	c := herdr.New(herdr.DefaultSocket())
	panes, err := c.Panes()
	if err != nil {
		// A machine not running herdr is the ordinary case, and saying so
		// plainly is more use in a status bar than the dial error behind it.
		if errors.Is(err, herdr.ErrNotRunning) {
			return errors.New("herdr is not running")
		}
		return err
	}
	self, _ := herdr.Inside()
	scoped := inScope(panes, scope, self.Pane, home)

	if pane, ok := herdr.Pick(running(c, scoped, p.matches), dir, scope, self.Tab); ok {
		verb := "focused"
		if p.Reuse != nil {
			if err := p.Reuse(c, pane.PaneID, paths); err != nil {
				return err
			}
			verb = "opened in"
		}
		if err := c.FocusPane(pane.PaneID); err != nil {
			return err
		}
		nameTab(c, pane.TabID, p.label())
		report(verb, pane.PaneID, scope, pane.Dir, false)
		return nil
	}

	pane, tab, err := newPane(c, scoped, self, dir, p.label())
	if err != nil {
		return err
	}
	// The pane is a fresh shell; the program is the command it runs. Sending
	// rather than asking herdr to launch it, because a split carries no command
	// of its own — herdr's own guidance is to create the pane and then run in
	// it, and the text waits in the pty until the shell is ready for it.
	//
	// SendInput here rather than SendText: handing a command line to a shell is
	// exactly what it is for, and a shell reads a bracketed paste as the command
	// it spells. Driving a program's own key handling is the other case, and it
	// needs the other method — see openIn.
	if err := c.SendInput(pane, p.Command(paths), []string{"enter"}); err != nil {
		return err
	}
	nameTab(c, tab, p.label())
	report("opened in", pane, scope, dir, false)
	return nil
}

// label is what this program's tab is called: its own, or the program's name.
func (p program) label() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Argv0
}

// matches reports whether a pane is running this program. A key that does not
// say otherwise claims any pane running its binary.
func (p program) matches(info herdr.ProcessInfo) bool {
	if p.Match != nil {
		return p.Match(info)
	}
	return info.Runs(p.Argv0)
}

// running narrows scoped panes to the ones a program claims.
//
// The mirror of atPrompt, which wants panes with nothing on top of the shell
// where this wants the pane with something particular on top. Both pay one
// pane.process_info call per candidate, and only for panes already known to be
// in the right place.
func running(c *herdr.Client, scoped []scopedPane, match func(herdr.ProcessInfo) bool) []herdr.Shell {
	var out []herdr.Shell
	for _, s := range scoped {
		// An agent pane is never one of these, and asking saves a round trip.
		if s.pane.Agent != "" {
			continue
		}
		info, err := c.ProcessInfo(s.pane.ID)
		if err != nil || !match(info) {
			continue
		}
		out = append(out, herdr.Shell{
			PaneID:   s.pane.ID,
			TabID:    s.pane.TabID,
			Dir:      s.pane.Cwd(),
			Checkout: s.checkout,
		})
	}
	return out
}

// newPane makes somewhere for a program to run: a tab in the workspace that
// holds this place, or a workspace of its own when there is none.
//
// A tab rather than a split beside the tree, even when the code is in the
// workspace ft is looking at. These programs want the width, and the tree is one
// tab away rather than gone.
//
// The returned tab id is empty when the label was set at creation and there is
// nothing left to rename.
func newPane(c *herdr.Client, scoped []scopedPane, self herdr.Self, dir, label string) (pane, tab string, err error) {
	if ws, found := workspaceFor(scoped, self.Workspace); found {
		pane, err = c.CreateTab(ws, dir, label)
		return pane, "", err
	}
	return c.CreateWorkspace(dir)
}

// nameTab labels a tab for the program in it, unless it already carries a name
// that means something.
//
// Best effort throughout: a tab that cannot be listed or renamed is a cosmetic
// loss, and the program is already running. Failing the command over it would
// turn a working hand-off into an error.
func nameTab(c *herdr.Client, tabID, label string) {
	if tabID == "" || label == "" {
		return
	}
	tabs, err := c.Tabs()
	if err != nil {
		return
	}
	for _, t := range tabs {
		if t.ID == tabID && t.Unnamed() {
			_ = c.RenameTab(tabID, label)
			return
		}
	}
}
