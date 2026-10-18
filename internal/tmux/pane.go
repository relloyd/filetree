package tmux

import "strings"

// Pane is one pane on the server, as listed by ListPanes.
//
// Where a pane *lives* is what matters to the caller: SessionID and WindowID
// are what say "this pane and that pane are side by side". The ids are used
// rather than the names because a session can be renamed while ft is running,
// and "$8"/"@8" cannot.
type Pane struct {
	ID        string // #{pane_id}, "%11" — stable across move-pane/break-pane
	SessionID string // #{session_id}, "$8"
	WindowID  string // #{window_id}, "@8"
	Command   string // #{pane_current_command}

	// TTY is #{pane_tty}. It is what ties a pane to a *client*: a session
	// attached inside a pane is a client on that pane's tty, and matching the
	// two is the only way to ask "which of my panes is showing that session?"
	// without keeping a note of what we opened.
	TTY string

	// Where the pane sits in its window, in cells, and whether it was the
	// active pane before the current one (#{pane_last}). The split-below key
	// needs both: geometry to tell which panes are to the right of ft, and
	// "last" to prefer the one you were just in — tmux keeps no per-pane focus
	// time, so that one flag is all the recency there is.
	Left, Top, Width, Height int
	Last                     bool

	// Dead is #{pane_dead}: the program has exited and remain-on-exit is
	// keeping the pane on screen. SessionAttached is #{session_attached}, a
	// property of the pane's *session*: how many clients are showing it. Both
	// are what a quit needs to know before it ends the session.
	Dead            bool
	SessionAttached int
}

// paneFields are the properties ListPanes asks tmux for, in order. Tab-joined
// like listFields: none of these can contain a tab, so no field can be split
// in two by its own contents.
var paneFields = []string{
	"#{pane_id}",
	"#{session_id}",
	"#{window_id}",
	"#{pane_current_command}",
	"#{pane_tty}",
	"#{pane_left}",
	"#{pane_top}",
	"#{pane_width}",
	"#{pane_height}",
	"#{pane_last}",
	"#{pane_dead}",
	"#{session_attached}",
}

// PaneFormat is the -F argument for list-panes.
var PaneFormat = strings.Join(paneFields, "\t")

// ParsePanes turns list-panes output into panes.
//
// A malformed line is skipped rather than failing the list, for the same
// reason ParseList skips one: a single unreadable pane should not hide every
// other pane on the server.
func ParsePanes(out string) []Pane {
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != len(paneFields) {
			continue
		}
		panes = append(panes, Pane{
			ID:        f[0],
			SessionID: f[1],
			WindowID:  f[2],
			Command:   f[3],
			TTY:       f[4],
			Left:      atoi(f[5]),
			Top:       atoi(f[6]),
			Width:     atoi(f[7]),
			Height:    atoi(f[8]),
			Last:      f[9] == "1",

			Dead:            f[10] == "1",
			SessionAttached: atoi(f[11]),
		})

	}
	return panes
}

// QuitCheck is what ending a session would interrupt.
type QuitCheck struct {
	// Busy are the panes running something other than a shell at its prompt.
	Busy []Pane
	// Others is how many clients besides the one quitting are showing the
	// session: another terminal attached to it, or another ft's picker.
	Others int
}

// CheckQuit reads, from one pane list, what killing session would interrupt.
// ok is false unless self is a pane of session: a pane moved into another
// session (join-pane, move-pane) takes ft with it, and the session it is in
// now is not the one it created.
//
// Every window counts, not just ft's, because kill-session takes them all. A
// pane running "tmux" is a nested client — ctrl+w and ctrl+s show an agent
// that way — and killing it only detaches the client, so the agent behind it
// keeps running and the pane is not busy. Nor is a dead pane, or one whose
// command tmux cannot name: there is nothing to say about either.
//
// Others assumes the quit was typed into a client showing this session, so
// one attached client is the one quitting. With none attached at all (keys
// sent by "tmux send-keys") there is nobody else either.
func CheckQuit(panes []Pane, self, session string) (QuitCheck, bool) {
	me, ok := findPane(panes, self)
	if !ok || session == "" || me.SessionID != session {
		return QuitCheck{}, false
	}
	check := QuitCheck{Others: max(0, me.SessionAttached-1)}
	for _, p := range panes {
		if p.ID == me.ID || p.SessionID != session || p.Dead || p.Command == "" {
			continue
		}
		if isShell(p.Command) || p.Command == "tmux" {
			continue
		}
		check.Busy = append(check.Busy, p)
	}
	return check, true
}
