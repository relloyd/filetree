package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/relloyd/filetree/internal/herdr"
	"github.com/relloyd/filetree/internal/platform"
)

// runHerdrShell puts the cursor in a herdr shell belonging to the place under
// ft's selection, opening one where there is none.
//
// It is the first thing in ft to drive herdr rather than tmux, and it is
// deliberately one key's worth: a shell, found or made. The tmux commands are
// untouched, so the two can be pressed side by side and compared.
//
// A subcommand rather than a shell template, for the same reason "ft jump" is
// one: choosing between the open shells takes a list, a process check per
// candidate and a ranking, which in a template would mean jq and no tests. The
// catalogue entry is then a single line, and everything worth getting right is
// ordinary Go with a table behind it.
//
// dir is the selection's directory, and the only argument. The checkout is
// worked out from it here rather than passed in as {gitroot}, because a
// template carrying {gitroot} is refused outside a repository — and this has to
// work in a loose directory too.
func runHerdrShell(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ft herdr-shell <dir>")
	}
	dir := filepath.Clean(args[0])
	home, _ := os.UserHomeDir()
	scope := herdr.ScopeFor(dir, checkoutFor(dir, home))

	// Before anything that can fail, so the path is on the clipboard whatever
	// happens next — including "herdr is not running". The shell this lands you
	// in is often a parent of the selection, and pasting is how you close the
	// gap; a copy that only happened on the happy path would be one you could
	// not rely on.
	copied := platform.New().CopyToClipboard(dir) == nil

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

	// A shell already sitting in this place is the whole point of the key.
	if shell, ok := herdr.Pick(atPrompt(c, scoped), dir, scope, self.Tab); ok {
		if err := c.FocusPane(shell.PaneID); err != nil {
			return err
		}
		report("focused", shell.PaneID, scope, shell.Dir, copied && shell.Dir != dir)
		return nil
	}

	id, err := openShell(c, scoped, self, dir)
	if err != nil {
		return err
	}
	report("opened", id, scope, dir, false)
	return nil
}

// openShell makes a shell for a place that has none, putting it where the rest
// of that place already lives.
//
// Three cases, narrowest first. Beside the tree when the code is in the
// workspace ft is looking at, so the tree stays on screen next to the shell it
// just opened. A tab in whichever workspace does hold the code when that is
// somewhere else, rather than carving up a layout nobody is looking at. And a
// workspace of its own when herdr has never seen this place at all — the
// alternative, dropping a tab into whatever ft happened to be sitting in, is
// what makes a workspace list stop meaning anything.
func openShell(c *herdr.Client, scoped []scopedPane, self herdr.Self, dir string) (string, error) {
	ws, found := workspaceFor(scoped, self.Workspace)
	switch {
	case found && self.Pane != "" && ws == self.Workspace:
		return c.SplitPane(self.Pane, dir)
	case found:
		return c.CreateTab(ws, dir, "")
	default:
		pane, _, err := c.CreateWorkspace(dir)
		return pane, err
	}
}

// report writes the one line ft shows in its status bar.
//
// It names the pane and where that pane sits, because the whole question the
// key raises is "which shell did it pick, and is it the one I meant". The copy
// note appears only when the shell is somewhere other than the selection, which
// is exactly when the clipboard is the thing you need next.
func report(verb, pane string, scope herdr.Scope, dir string, note bool) {
	line := fmt.Sprintf("%s %s — %s", verb, pane, abbrev(scope.Root, dir))
	if note {
		line += " (path copied)"
	}
	fmt.Println(line)
}

// herdrShellArgs reports whether the command line is the "ft herdr-shell" form.
//
// One argument after the name, so a directory called "herdr-shell" still opens
// as a tree — the same guard "ft jump" and "ft bookmark" use.
func herdrShellArgs(args []string) bool {
	return len(args) == 2 && args[0] == "herdr-shell"
}
