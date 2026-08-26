package tmux

import (
	"bytes"
	"errors"
	"os/exec"
	"strconv"
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

// ListPanes returns every pane on the server, across all sessions.
//
// "-a" is what makes this server-wide: without it tmux lists the panes of the
// current window only, and outside a pane there is no current window at all.
// No server running is an empty list rather than an error, exactly as in List.
func ListPanes() ([]Pane, error) {
	if !Available() {
		return nil, ErrNotInstalled
	}
	var stdout, stderr bytes.Buffer
	c := exec.Command("tmux", "list-panes", "-a", "-F", PaneFormat)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		if noServer(stderr.String()) {
			return nil, nil
		}
		return nil, tmuxError(err, stderr.String())
	}
	return ParsePanes(stdout.String()), nil
}

// ListClients returns every attached client on the server, across all
// sessions. No server running is an empty list rather than an error, as in
// List and ListPanes.
func ListClients() ([]Client, error) {
	if !Available() {
		return nil, ErrNotInstalled
	}
	var stdout, stderr bytes.Buffer
	c := exec.Command("tmux", "list-clients", "-F", ClientFormat)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		if noServer(stderr.String()) {
			return nil, nil
		}
		return nil, tmuxError(err, stderr.String())
	}
	return ParseClients(stdout.String()), nil
}

// SplitAttach opens name in a new pane beside self, as a nested client.
//
// Everything but the attach runs as direct argv, so no tmux target is ever
// exposed to a shell. The attach cannot: tmux hands a split's shell-command to
// default-shell, which is zsh on a stock macOS, so the "=" exact-match target
// has to be quoted or zsh's equals expansion eats it — the bug that already
// shipped once. quote is injected for that, as it is for AttachPopup.
//
// -f makes the pane span the window rather than carving up ft's own column,
// which on a sidebar would leave both halves too narrow to read. -d leaves the
// focus in ft: opening an agent is not the same as wanting to type at it, and
// the tree is where you were. A second ctrl+w on a session already on screen
// is what moves you into it — see paneSession.
//
// "TMUX=" is what gets past tmux's refusal to nest, and -S is what stops that
// from meaning "the default socket": see SocketPath.
func SplitAttach(self, socket, name string, quote func(string) string) error {
	if !Available() {
		return ErrNotInstalled
	}
	return run("split-window", "-h", "-f", "-d", "-t", self, AttachCommand(socket, name, quote))
}

// AttachCommand is the shell command a split pane runs to become a nested
// client on name. Pure, so the quoting it depends on can be table-tested: it
// is handed to tmux as a shell-command and therefore reaches default-shell,
// which is zsh on a stock macOS.
func AttachCommand(socket, name string, quote func(string) string) string {
	cmd := "TMUX= tmux "
	if socket != "" {
		cmd += "-S " + quote(socket) + " "
	}
	return cmd + "attach-session -t " + quote(target(name))
}

// DetachClient detaches whatever is attached to name, which for a session
// shown in a pane closes that pane and hands its space back. The agent keeps
// running: that is the whole point of a session it was started in.
//
// It exists so detaching needs no nested prefix. A client inside a pane owns
// the prefix key, so detaching from the inside is "prefix prefix d"; ft is
// sitting next to it and can simply say so instead.
func DetachClient(name string) error {
	if !Available() {
		return ErrNotInstalled
	}
	return run("detach-client", "-s", target(name))
}

// SelectPane moves the focus to a pane, by id.
func SelectPane(id string) error {
	if !Available() {
		return ErrNotInstalled
	}
	return run("select-pane", "-t", id)
}

// ResizePaneWidth sets a pane's width in columns, taking the difference from
// its neighbours.
func ResizePaneWidth(id string, cols int) error {
	if !Available() {
		return ErrNotInstalled
	}
	return run("resize-pane", "-t", id, "-x", strconv.Itoa(cols))
}

// PaneWidths reports a pane's own width and that of the window holding it.
//
// The two go together because the caller wants the ratio: ft restores its
// width after a split only when it was a *sidebar* to begin with. Restoring it
// unconditionally would be wrong for an ft that had the window to itself,
// where it would squeeze the pane it had just opened down to one column.
//
// Note display-message resolves -t as a *pane*, and unlike attach-session or
// kill-session it does not accept the "=" exact-match prefix, so this is one
// of the few places target() must not be used. A pane id is unambiguous
// anyway.
func PaneWidths(id string) (pane, window int, err error) {
	if !Available() {
		return 0, 0, ErrNotInstalled
	}
	var stdout, stderr bytes.Buffer
	c := exec.Command("tmux", "display-message", "-p", "-t", id, "#{pane_width}\t#{window_width}")
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return 0, 0, tmuxError(err, stderr.String())
	}
	f := strings.Split(strings.TrimSpace(stdout.String()), "\t")
	if len(f) != 2 {
		return 0, 0, errors.New("tmux: unreadable pane size")
	}
	pane, err = strconv.Atoi(f[0])
	if err != nil {
		return 0, 0, err
	}
	window, err = strconv.Atoi(f[1])
	if err != nil {
		return 0, 0, err
	}
	return pane, window, nil
}

// run is the plain-argv tmux call the commands above share.
func run(args ...string) error {
	var stderr bytes.Buffer
	c := exec.Command("tmux", args...)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return tmuxError(err, stderr.String())
	}
	return nil
}
