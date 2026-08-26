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
		})

	}
	return panes
}
