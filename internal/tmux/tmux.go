// Package tmux decides whether ft should relaunch itself inside a new tmux
// session, and performs that relaunch. The tmux hand-off commands in the
// starter config (split-window, send-keys) only work from inside a pane, so
// ft arranges that itself rather than relying on a shell alias — an alias is
// not loaded by non-interactive shells, and it breaks when the user is
// already inside tmux.
package tmux

import "os/exec"

// Config values for general.tmux.
const (
	ModeAuto  = "auto"  // relaunch inside tmux when started outside one
	ModeNever = "never" // never relaunch
)

// Env is the subset of the world the wrap decision depends on, gathered by
// the caller so the decision itself stays pure and table-testable.
type Env struct {
	TMUX    string // $TMUX — non-empty inside a tmux pane
	STY     string // $STY — inside GNU screen
	ZELLIJ  string // $ZELLIJ — inside zellij
	Mode    string // general.tmux: ModeAuto or ModeNever
	HasTmux bool   // tmux found on PATH
	TTY     bool   // stdout is a terminal
}

// ShouldWrap reports whether ft should replace itself with a new tmux session.
//
// $TMUX is the loop guard: tmux sets it in every pane it spawns, so the
// relaunched process always sees it and never wraps a second time. $STY and
// $ZELLIJ suppress wrapping too — nesting tmux inside another multiplexer is
// almost never what someone wants.
func ShouldWrap(e Env) bool {
	if e.Mode != ModeAuto {
		return false
	}
	if e.TMUX != "" || e.STY != "" || e.ZELLIJ != "" {
		return false
	}
	// Without a terminal tmux fails with "open terminal failed"; running bare
	// is the better failure.
	return e.HasTmux && e.TTY
}

// Available reports whether tmux can be found on PATH.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}
