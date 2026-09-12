package main

import (
	"fmt"
	"path/filepath"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/herdr"
)

// lazygitBin is the program these two keys look for and launch.
const lazygitBin = "lazygit"

// fileFlag is what turns lazygit from "show me this repository" into "show me
// this file's history". It is also what tells the two keys' panes apart.
const fileFlag = "-f"

// lazygit is the git UI as the repository key sees it.
//
// No Reuse: lazygit shows a repository rather than a file, so one already open
// for this checkout is already showing what you asked for. Finding it is the
// whole answer, and going there is the action.
//
// Match excludes an instance started with "-f". That one is somebody's blame
// view, filtered to a single file, and handing it back to a key that asked for
// the whole repository would be answering a different question.
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
	Match:     func(i herdr.ProcessInfo) bool { return i.RunsWithout(lazygitBin, fileFlag) },
	NeedsRepo: true,
}

// blame is lazygit pointed at one file's history.
//
// Built per press rather than kept as a value, because both the arguments it
// runs with and the panes it will claim depend on which file is under the
// cursor. Matching on the path as well as the binary is what gives one tab per
// file: press it twice on the same file and you go back to the view you had,
// press it on another and that file gets its own — which is exactly what the
// tmux "M" does with its per-file session name.
func blame(path string) program {
	return program{
		Argv0:     lazygitBin,
		Label:     "blame " + filepath.Base(path),
		Command:   func([]string) string { return lazygitBin + " " + fileFlag + " " + config.ShellQuote(path) },
		Match:     func(i herdr.ProcessInfo) bool { return i.RunsWith(lazygitBin, fileFlag, path) },
		NeedsRepo: true,
	}
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

// runHerdrBlame opens lazygit on one file's history, in the workspace that holds
// the code.
//
// The herdr counterpart of "M". It takes the directory as well as the file
// because the two answer different questions: the directory says which workspace
// this belongs to, and the file says what to show. On a finder row they can be
// in different checkouts entirely.
func runHerdrBlame(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: ft herdr-blame <dir> <path>")
	}
	return runHerdrProgram(blame(filepath.Clean(args[1])), args[0], nil)
}

// herdrLazygitArgs reports whether the command line is the "ft herdr-lazygit"
// form.
//
// One argument after the name, so a directory called "herdr-lazygit" still opens
// as a tree — the same guard the other subcommands use.
func herdrLazygitArgs(args []string) bool {
	return len(args) == 2 && args[0] == "herdr-lazygit"
}

// herdrBlameArgs reports whether the command line is the "ft herdr-blame" form.
func herdrBlameArgs(args []string) bool {
	return len(args) == 3 && args[0] == "herdr-blame"
}
