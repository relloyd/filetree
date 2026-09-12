package main

import (
	"fmt"
)

// lazygitBin is the program this key looks for and launches.
const lazygitBin = "lazygit"

// lazygit is the git UI as one of these keys sees it.
//
// No Reuse: lazygit shows a repository rather than a file, so one already open
// for this checkout is already showing what you asked for. Finding it is the
// whole answer, and going there is the action.
//
// NeedsRepo, unlike the shell and editor keys: outside a checkout lazygit has
// nothing to show, and opening a tab for it would only put an error on screen.
// The tmux "L" refuses the same way, though it gets there differently — its
// template mentions {repokey}, and config.NeedsRepo refuses any template that
// names a repo token. This one is asked directly, so that the same
// home-spanning-dotfiles repo the scoping ignores is ignored here too.
var lazygit = program{
	Argv0:     lazygitBin,
	Command:   func([]string) string { return lazygitBin },
	NeedsRepo: true,
}

// runHerdrLazygit puts lazygit for the checkout under ft's selection in front of
// you, reusing the one already open for it where there is one.
//
// The herdr counterpart of "L". The tmux version keeps one session per checkout
// and reattaches it in a popup; this keeps one tab per checkout and goes to it,
// which is the same idea in herdr's shape — there being no popup to reattach
// into, and no need for one when a tab is a keystroke away.
//
// It takes only a directory. lazygit walks up to find the repository itself, so
// the selection's own directory is enough, and the checkout is what decides
// which workspace answers.
func runHerdrLazygit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ft herdr-lazygit <dir>")
	}
	return runHerdrProgram(lazygit, args[0], nil)
}

// herdrLazygitArgs reports whether the command line is the "ft herdr-lazygit"
// form.
//
// One argument after the name, so a directory called "herdr-lazygit" still opens
// as a tree — the same guard the other subcommands use.
func herdrLazygitArgs(args []string) bool {
	return len(args) == 2 && args[0] == "herdr-lazygit"
}
