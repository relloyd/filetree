package main

import (
	"fmt"
	"strings"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/herdr"
)

// editor is the program this key looks for and launches.
//
// Hard-coded, as "hx" is in the tmux hand-off template it mirrors. A [commands]
// override already lets anyone point the whole command somewhere else, which is
// why this is not also a setting.
const editor = "hx"

// helix is the editor as one of these keys sees it.
var helix = program{
	Argv0:   editor,
	Command: func(paths []string) string { return editor + " " + quoted(paths) },
	Reuse:   openIn,
}

// runHerdrEdit opens files in the helix belonging to the place under ft's
// selection, and takes you to it.
//
// The herdr counterpart of the "t" hand-off, and the same idea one level up. "t"
// looks for an editor in the pane beside ft and types into it; this looks for
// the workspace that holds the code, and for an editor inside that, so it
// reaches a helix a whole workspace away.
//
// Deliberately no "type hx at an idle shell" branch, which "t" has. A shell in
// the checkout is often one you are using for something, and turning it into an
// editor takes it away from you. A tab costs nothing by comparison.
func runHerdrEdit(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: ft herdr-edit <dir> <path>...")
	}
	return runHerdrProgram(helix, args[0], args[1:])
}

// openIn hands paths to a helix that is already running.
//
// Escape first, and on its own, because ":open" only means anything in normal
// mode. Escape is not free — in helix it also closes a picker or cancels a
// prompt — but that is the bargain anyone reaching into a running editor makes,
// and the tmux hand-off makes it too.
//
// Then SendText rather than SendInput, and this one is load-bearing. SendInput
// arrives as a bracketed paste, and helix inserts a paste into the buffer
// literally however it is spelled: the command text ends up in the file, the
// file is left modified, and nothing opens. SendText arrives as typing, so the
// leading colon opens helix's command line.
//
// The tmux hand-off has a cousin of this bug — it needs a sleep between the
// escape and the colon, because tmux coalesces them into alt+colon — but the
// cause is different and so is the cure. There is no sleep here; there is a
// different method.
func openIn(c *herdr.Client, pane string, paths []string) error {
	if err := c.SendKeys(pane, []string{"esc"}); err != nil {
		return err
	}
	if err := c.SendText(pane, ":open "+quoted(paths)); err != nil {
		return err
	}
	return c.SendKeys(pane, []string{"enter"})
}

// quoted renders paths for a command line, leaving ordinary ones untouched.
//
// config.ShellQuote is the rule ft already applies to every command template, so
// a path that survives "hx {paths}" survives this. Ordinary paths come back
// bare, which matters for the ":open" branch: that goes to helix's own command
// line rather than to a shell, and the fewer quotes it has to agree with us
// about, the better.
func quoted(paths []string) string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = config.ShellQuote(p)
	}
	return strings.Join(out, " ")
}

// herdrEditArgs reports whether the command line is the "ft herdr-edit" form.
//
// A directory and at least one path, so a directory called "herdr-edit" still
// opens as a tree — the same guard the other subcommands use.
func herdrEditArgs(args []string) bool {
	return len(args) >= 3 && args[0] == "herdr-edit"
}
