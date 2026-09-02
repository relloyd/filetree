//go:build unix

package tmux

import (
	"os"
	"os/exec"
	"syscall"
)

// Wrap replaces this process with `tmux new-session -s name -c root <ft> <root>`.
//
// syscall.Exec rather than a child process: replacement keeps terminal
// ownership, signal delivery and the exit code correct with no supervision
// code. It only returns on failure, and the caller should then carry on
// without tmux.
//
// An empty name creates the session unnamed, as this did before there was a
// name to give it. A name that is already taken is fatal — tmux refuses with
// "duplicate session" and there is no process left here to recover in — so the
// caller picks a free one (see UniqueName). Deliberately not `new-session -A`:
// attaching would drop this terminal into the *other* tree, which may be
// rooted somewhere else entirely.
//
// The command is passed to tmux as separate arguments, which tmux execs
// directly instead of routing through `sh -c` (see SHELL COMMANDS in
// tmux(1)), so paths containing spaces need no quoting. No `--` separator is
// used — tmux's argument parser is not getopt — which is safe because both
// paths are absolute and so cannot be mistaken for options.
func Wrap(root, name string) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	// os.Args[0] may be a bare name that execvp would re-resolve against a
	// different PATH or working directory.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	argv := []string{"tmux", "new-session"}
	if name != "" {
		argv = append(argv, "-s", name)
	}
	argv = append(argv, "-c", root, self, root)
	return syscall.Exec(tmuxPath, argv, os.Environ())
}
