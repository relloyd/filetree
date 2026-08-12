package tmux

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// ErrNotInstalled is returned when tmux is not on PATH. Every session feature
// is optional in the way the rest of ft's external tools are: missing tmux
// surfaces in the status bar, it does not stop ft starting.
var ErrNotInstalled = errors.New("tmux is not installed")

// List returns the sessions carrying the prefix, newest activity first left to
// the caller to order.
//
// No server running is an empty list, not an error: it is the ordinary state
// before the first agent session exists, and tmux reports it by exiting 1 with
// "no server running on <socket>" — indistinguishable, by exit code alone,
// from a real failure.
func List(prefix string) ([]Session, error) {
	if !Available() {
		return nil, ErrNotInstalled
	}
	var stdout, stderr bytes.Buffer
	c := exec.Command("tmux", "list-sessions", "-F", ListFormat)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		if noServer(stderr.String()) {
			return nil, nil
		}
		return nil, tmuxError(err, stderr.String())
	}
	return ParseList(prefix, stdout.String()), nil
}

// Kill ends one session by name. A session that has already gone is not an
// error — the list the caller acted on is a snapshot, and the outcome it
// wanted has happened either way.
func Kill(name string) error {
	if !Available() {
		return ErrNotInstalled
	}
	if name == "" {
		return errors.New("no session to kill")
	}
	var stderr bytes.Buffer
	c := exec.Command("tmux", "kill-session", "-t", target(name))
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := stderr.String()
		if noServer(msg) || strings.Contains(msg, "can't find session") {
			return nil
		}
		return tmuxError(err, msg)
	}
	return nil
}

// The "=" prefix on a -t target is tmux's exact-match marker; without it a
// name is treated as a prefix and then as a pattern, so killing
// "ft/repo/main/claude" could take "ft/repo/main/claude-2" with it.
func target(name string) string { return "=" + name }

// popupSize matches the popups in the starter config, so a session reattached
// from the picker sits exactly where one launched by "c" does.
const popupSize = `-w 92% -h 92%`

// AttachPopup is the shell command that opens an existing session in a popup
// over the current window. Detaching from it (the tmux prefix then "d", or
// exiting the last shell) closes the popup and returns to the tree.
//
// Attaching from inside tmux normally refuses — "sessions should be nested
// with care" — but a popup is not a pane, and tmux's nested check works by
// matching the client's tty against the panes it knows about. That is the same
// reason the popup commands in the starter config work, and why unsetting
// $TMUX here would be wrong: it would send tmux to the default socket and
// break anyone running "tmux -L".
//
// quote is injected rather than imported so this package keeps no dependency
// on the config package, which is the one that owns shell quoting.
func AttachPopup(name string, quote func(string) string) string {
	return `tmux display-popup -E ` + popupSize + ` "tmux attach-session -t ` + quote(target(name)) + `"`
}

// SwitchClient is the shell command that moves this tmux client to the
// session, giving it the whole window instead of a popup.
//
// It takes the same "$TMUX" guard as the pane commands in the starter: run
// outside a pane, tmux resolves the client against the most recently used
// session and would move a window the user is not even looking at.
func SwitchClient(name string, quote func(string) string) string {
	return `[ -z "$TMUX" ] || tmux switch-client -t ` + quote(target(name))
}

// noServer recognises the one failure that means "nothing is running yet".
func noServer(stderr string) bool {
	return strings.Contains(stderr, "no server running") ||
		strings.Contains(stderr, "error connecting to")
}

// tmuxError prefers tmux's own message: "duplicate session: x" says far more
// than "exit status 1".
func tmuxError(err error, stderr string) error {
	if msg := strings.TrimSpace(stderr); msg != "" {
		return errors.New(msg)
	}
	return err
}
