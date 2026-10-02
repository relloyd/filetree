package tmux

import (
	"strconv"
	"strings"
)

// Client is one attached tmux client, as listed by ListClients.
//
// A session shown in a pane is a *nested client* on that pane's tty, so a
// client is the link between "this session is attached" and "it is attached
// over there". That is what lets ft find the pane an agent is showing in
// without remembering that it opened it — which matters because ft may have
// been restarted, or the pane opened by hand, since.
type Client struct {
	TTY     string // #{client_tty}
	Session string // #{client_session}, the session name
}

// clientFields are the properties ListClients asks tmux for, in order.
// Tab-joined like paneFields, and for the same reason.
var clientFields = []string{
	"#{client_tty}",
	"#{client_session}",
}

// ClientFormat is the -F argument for list-clients.
var ClientFormat = strings.Join(clientFields, "\t")

// ParseClients turns list-clients output into clients. A malformed line is
// skipped rather than failing the list, as in ParsePanes.
func ParseClients(out string) []Client {
	var cs []Client
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != len(clientFields) {
			continue
		}
		cs = append(cs, Client{TTY: f[0], Session: f[1]})
	}
	return cs
}

// SocketPath extracts the server socket from a $TMUX value, which tmux sets in
// every pane as "<socket>,<server pid>,<session index>".
//
// It is needed because a session cannot be attached inside a pane without
// unsetting $TMUX — tmux refuses to nest otherwise — and unsetting it alone
// sends the nested tmux to the *default* socket, where the session does not
// exist ("set $TMUX to force"). Passing the socket back with -S is what keeps
// "tmux -L foo" working, which is exactly the case AttachPopup avoids by never
// unsetting $TMUX at all.
func SocketPath(tmuxEnv string) string {
	if tmuxEnv == "" {
		return ""
	}
	socket, _, _ := strings.Cut(tmuxEnv, ",")
	return socket
}

// PaneShowing finds the pane alongside self that is displaying a session match
// accepts, and returns it with that session's name.
//
// "Alongside" is the same window, not the same session: ft and the agent it
// opened are two panes of one window, and a session attached in some other
// window is not ft's to reach for. self is excluded so ft can never find
// itself — load-bearing now that ft's own session is named under the same
// prefix as everything else it opens.
//
// When several match, the newest pane wins: tmux numbers panes in creation
// order and never reuses an id. That is what "X" needs once "ctrl+s" has
// stacked sessions in a column — the one to send away is the one you opened
// last, not whichever list-panes happens to print first.
func PaneShowing(panes []Pane, clients []Client, self string, match func(string) bool) (Pane, string, bool) {
	if match == nil {
		return Pane{}, "", false
	}
	me, ok := findPane(panes, self)
	if !ok {
		return Pane{}, "", false
	}
	sessionOn := make(map[string]string, len(clients))
	for _, c := range clients {
		if c.TTY != "" {
			sessionOn[c.TTY] = c.Session
		}
	}
	var best Pane
	name, found := "", false
	for _, p := range panes {
		if p.WindowID != me.WindowID || p.ID == self || p.TTY == "" {
			continue
		}
		if s, ok := sessionOn[p.TTY]; ok && match(s) && (!found || paneNumber(p.ID) > paneNumber(best.ID)) {
			best, name, found = p, s, true
		}
	}
	return best, name, found
}

// findPane looks a pane up by id. An empty id — outside tmux there is no self
// — finds nothing rather than a pane whose id failed to parse.
func findPane(panes []Pane, id string) (Pane, bool) {
	if id == "" {
		return Pane{}, false
	}
	for _, p := range panes {
		if p.ID == id {
			return p, true
		}
	}
	return Pane{}, false
}

// paneNumber is the number in a pane id ("%12" is 12), compared numerically
// because as text "%10" sorts before "%9". An unreadable id is -1, so it
// loses to any real one.
func paneNumber(id string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "%"))
	if err != nil {
		return -1
	}
	return n
}

// PaneRightOf picks the pane the split-below key ("ctrl+s" in T) divides: one
// in self's window lying wholly to the right of it. Usually that is the editor
// beside the tree, but any program counts — an agent opened with "ctrl+w" is
// a pane to split like any other, which is how sessions stack in a column.
//
// Several can qualify. The one you were last in wins (#{pane_last}): ft is the
// active pane while you press the key, so "last" is the pane you came from,
// which is the same tie-break the "t" hand-off uses. tmux records no recency
// beyond that one flag, so the rest are ranked by size — the editor is
// normally the biggest thing on screen and has the most rows to give — and
// then by position, so the answer never depends on list order.
//
// false means nothing is to the right, and the caller falls back to the
// full-height split "ctrl+w" makes.
func PaneRightOf(panes []Pane, self string) (Pane, bool) {
	me, found := findPane(panes, self)
	if !found {
		return Pane{}, false
	}
	var best Pane
	ok := false
	for _, p := range panes {
		if p.WindowID != me.WindowID || p.ID == self || p.Left < me.Left+me.Width {
			continue
		}
		if !ok || rightPaneBefore(p, best) {
			best, ok = p, true
		}
	}
	return best, ok
}

// rightPaneBefore orders PaneRightOf's candidates: last-active, then larger,
// then further left, then higher up. Two panes of one window never share a
// top-left corner, so that is a total order and needs no tie-break on id.
func rightPaneBefore(a, b Pane) bool {
	if a.Last != b.Last {
		return a.Last
	}
	if aa, ba := a.Width*a.Height, b.Width*b.Height; aa != ba {
		return aa > ba
	}
	if a.Left != b.Left {
		return a.Left < b.Left
	}
	return a.Top < b.Top
}
