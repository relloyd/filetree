package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/relloyd/filetree/internal/ipc"
	"github.com/relloyd/filetree/internal/tmux"
)

// runJump moves a running ft's cursor onto a file. It is the whole of the
// sending side: an editor binds a key to
//
//	:sh ft jump %{buffer_name}
//
// and the tree pane beside it follows along as you move around the project.
//
// Like `ft bookmark`, helix expands %{buffer_name} relative to its own working
// directory and runs the command *with* that working directory, so resolving
// against our own cwd is right either way and there is nothing for the editor
// to tell us about where it is.
//
// Silent on success and non-zero on failure, for the same reason: an exit code
// is the only feedback channel an editor gives a shell command, and helix
// renders it as "Shell command failed" with our stderr behind it.
func runJump(cfgDir string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ft jump <file>")
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)

	dir := ipc.Dir(cfgDir)
	cands := ipc.Collect(dir)
	if len(cands) == 0 {
		return fmt.Errorf("no filetree is running")
	}
	panes, caller := paneLayout(os.Getenv("TMUX_PANE"))

	win, path, ok := choose(cands, panes, caller, abs)
	if !ok {
		return fmt.Errorf("no filetree pane covers %s", abs)
	}
	reply, err := ipc.Reveal(ipc.SocketFor(dir, win.PID), path)
	if err != nil {
		return err
	}
	if !reply.OK {
		if reply.Reason != "" {
			return fmt.Errorf("%s", reply.Reason)
		}
		return fmt.Errorf("%s was not revealed", abs)
	}
	return nil
}

// choose picks the instance to send to, and the spelling of the path to send
// it.
//
// The straight comparison first, which is what nearly always matches. The
// fallback exists because only one side of the comparison has to have resolved
// a symlink for it to fail: on macOS /tmp is a link to /private/tmp, and Go's
// filepath.Abs resolves it (os.Getwd goes to the kernel) while an absolute
// path typed on the command line does not. So an ft rooted at /tmp/proj and an
// editor reporting /private/tmp/proj/x.go would never find each other.
//
// Matching resolved means the winner's root may be spelled differently from
// the path we matched, so the path is rebuilt in that instance's own terms
// before it is sent — its tree only knows the root it was given.
func choose(cands []ipc.Candidate, panes map[string]ipc.Location, caller ipc.Location, abs string) (ipc.Candidate, string, bool) {
	if win, ok := ipc.Pick(cands, panes, caller, abs); ok {
		return win, abs, true
	}
	target := resolve(abs)
	roots := make(map[int]string, len(cands))
	resolved := make([]ipc.Candidate, len(cands))
	for i, c := range cands {
		roots[c.PID] = c.Root
		c.Root = resolve(c.Root)
		resolved[i] = c
	}
	win, ok := ipc.Pick(resolved, panes, caller, target)
	if !ok {
		return ipc.Candidate{}, "", false
	}
	rel, err := filepath.Rel(win.Root, target)
	if err != nil {
		return ipc.Candidate{}, "", false
	}
	return win, filepath.Join(roots[win.PID], rel), true
}

// resolve follows symlinks, leaving the path alone when it cannot.
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// paneLayout asks tmux where every pane on the server lives, and picks out the
// caller's own.
//
// No tmux, no server, or a caller outside a pane all come back empty, which
// leaves Pick with nothing but root containment to go on — the right answer
// when there is no layout to reason about.
func paneLayout(callerPane string) (map[string]ipc.Location, ipc.Location) {
	panes, err := tmux.ListPanes()
	if err != nil || len(panes) == 0 {
		return nil, ipc.Location{}
	}
	out := make(map[string]ipc.Location, len(panes))
	for _, p := range panes {
		out[p.ID] = ipc.Location{Session: p.SessionID, Window: p.WindowID}
	}
	return out, out[callerPane]
}
