//go:build unix

package tmux

import (
	"os"
	"os/exec"
	"syscall"
)

// Wrap replaces this process with `tmux new-session -c root <ft> <root>`.
//
// syscall.Exec rather than a child process: replacement keeps terminal
// ownership, signal delivery and the exit code correct with no supervision
// code. It only returns on failure, and the caller should then carry on
// without tmux.
//
// The command is passed to tmux as separate arguments, which tmux execs
// directly instead of routing through `sh -c` (see SHELL COMMANDS in
// tmux(1)), so paths containing spaces need no quoting. No `--` separator is
// used — tmux's argument parser is not getopt — which is safe because both
// paths are absolute and so cannot be mistaken for options.
func Wrap(root string) error {
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
	argv := []string{"tmux", "new-session", "-c", root, self, root}
	return syscall.Exec(tmuxPath, argv, os.Environ())
}
