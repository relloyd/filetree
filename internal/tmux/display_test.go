package tmux

import (
	"strings"
	"testing"
)

func TestParseClients(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want []Client
	}{
		{"empty", "", nil},
		{
			"two clients",
			"/dev/ttys006\tft/repo/main/claude\n/dev/ttys009\twork\n",
			[]Client{
				{TTY: "/dev/ttys006", Session: "ft/repo/main/claude"},
				{TTY: "/dev/ttys009", Session: "work"},
			},
		},
		// One unreadable line must not hide the rest, as in ParsePanes.
		{
			"short line skipped",
			"broken\n/dev/ttys006\tft/repo/main/claude\n",
			[]Client{{TTY: "/dev/ttys006", Session: "ft/repo/main/claude"}},
		},
		{"crlf", "/dev/ttys006\twork\r\n", []Client{{TTY: "/dev/ttys006", Session: "work"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseClients(tc.out)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseClients() = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("client %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The format string is what ParseClients' field count is checked against; if
// one grows without the other the parse silently drops every line.
func TestClientFormatMatchesFieldCount(t *testing.T) {
	if got, want := len(clientFields), 2; got != want {
		t.Fatalf("clientFields = %d, want %d", got, want)
	}
	if ClientFormat != "#{client_tty}\t#{client_session}" {
		t.Errorf("ClientFormat = %q", ClientFormat)
	}
}

// $TMUX is "<socket>,<pid>,<session index>". The socket has to survive,
// because attaching inside a pane means unsetting $TMUX, and a nested tmux
// with no -S goes to the *default* socket — where the session does not exist.
// Anyone running "tmux -L foo" would find the pane opening on nothing.
func TestSocketPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/private/tmp/tmux-501/default,15391,1", "/private/tmp/tmux-501/default"},
		{"/private/tmp/tmux-501/exp3,15391,1", "/private/tmp/tmux-501/exp3"},
		{"", ""},
		{"/no/commas", "/no/commas"},
	} {
		if got := SocketPath(tc.in); got != tc.want {
			t.Errorf("SocketPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// PaneShowing is how "X" and "ctrl+w" find an agent pane without ft having
// remembered opening one — which matters because ft may have been restarted,
// or the pane opened by hand, in between.
func TestPaneShowing(t *testing.T) {
	// ft (%11) and an agent (%12) share window @8; %20 is another window
	// entirely, and %13 is a pane with no client attached in it.
	panes := []Pane{
		{ID: "%11", WindowID: "@8", TTY: "/dev/ttys001", Command: "ft"},
		{ID: "%12", WindowID: "@8", TTY: "/dev/ttys002", Command: "tmux"},
		{ID: "%13", WindowID: "@8", TTY: "/dev/ttys003", Command: "hx"},
		{ID: "%20", WindowID: "@9", TTY: "/dev/ttys004", Command: "tmux"},
	}
	clients := []Client{
		{TTY: "/dev/ttys002", Session: "ft/repo/main/claude"},
		{TTY: "/dev/ttys004", Session: "ft/other/main/claude"}, // another window
	}
	anyAgent := func(s string) bool { return strings.HasPrefix(s, "ft/") }

	p, name, ok := PaneShowing(panes, clients, "%11", anyAgent)
	if !ok || p.ID != "%12" || name != "ft/repo/main/claude" {
		t.Errorf("got %q/%q ok=%v, want %%12/ft/repo/main/claude", p.ID, name, ok)
	}

	// A session attached in another window is not ours to reach for: "beside
	// me" means the same window, not the same server.
	if _, _, ok := PaneShowing(panes, clients, "%20", anyAgent); ok {
		t.Error("found an agent from a window with none in it")
	}

	// Matching by exact name is what makes ctrl+w focus rather than re-open.
	if _, _, ok := PaneShowing(panes, clients, "%11", func(s string) bool {
		return s == "ft/repo/main/claude"
	}); !ok {
		t.Error("an exact name match should find the pane showing it")
	}
	if _, _, ok := PaneShowing(panes, clients, "%11", func(s string) bool {
		return s == "ft/repo/main/copilot"
	}); ok {
		t.Error("a session that is not on screen must not be found")
	}

	// Guards. Outside tmux there is no self, and a self tmux does not know
	// about has no window to search.
	if _, _, ok := PaneShowing(panes, clients, "", anyAgent); ok {
		t.Error("no self pane should find nothing")
	}
	if _, _, ok := PaneShowing(panes, clients, "%99", anyAgent); ok {
		t.Error("an unknown self pane should find nothing")
	}
	if _, _, ok := PaneShowing(panes, nil, "%11", anyAgent); ok {
		t.Error("no clients means nothing is displayed")
	}
}

// With sessions stacked beside ft, the newest pane is the one found — "X"
// sends away what you opened last. Pane ids compare as numbers: as text, %10
// would lose to %9.
func TestPaneShowingPrefersNewest(t *testing.T) {
	panes := []Pane{
		{ID: "%1", WindowID: "@8", TTY: "/dev/ttys001", Command: "ft"},
		{ID: "%10", WindowID: "@8", TTY: "/dev/ttys010", Command: "tmux"},
		{ID: "%9", WindowID: "@8", TTY: "/dev/ttys009", Command: "tmux"},
		{ID: "%2", WindowID: "@8", TTY: "/dev/ttys002", Command: "tmux"},
	}
	clients := []Client{
		{TTY: "/dev/ttys002", Session: "ft/agent/a"},
		{TTY: "/dev/ttys009", Session: "ft/agent/b"},
		{TTY: "/dev/ttys010", Session: "ft/agent/c"},
	}
	p, name, ok := PaneShowing(panes, clients, "%1", func(s string) bool { return strings.HasPrefix(s, "ft/") })
	if !ok || p.ID != "%10" || name != "ft/agent/c" {
		t.Errorf("got %q/%q ok=%v, want %%10/ft/agent/c", p.ID, name, ok)
	}
}

// ft must never find itself: its own pane is excluded by id, so even a client
// on ft's own tty cannot be mistaken for an agent sharing the window.
func TestPaneShowingExcludesSelf(t *testing.T) {
	panes := []Pane{{ID: "%11", WindowID: "@8", TTY: "/dev/ttys001", Command: "ft"}}
	clients := []Client{{TTY: "/dev/ttys001", Session: "ft/repo/main/claude"}}
	if _, _, ok := PaneShowing(panes, clients, "%11", func(string) bool { return true }); ok {
		t.Error("ft found itself")
	}
}

func TestPaneRightOf(t *testing.T) {
	// The usual shape: ft (%1) a 40-column sidebar, helix (%2) beside it, and
	// a window @9 elsewhere whose panes must never be chosen.
	ft := Pane{ID: "%1", WindowID: "@8", Left: 0, Width: 40, Height: 50}
	hx := Pane{ID: "%2", WindowID: "@8", Left: 41, Width: 159, Height: 50, Command: "hx"}
	other := Pane{ID: "%9", WindowID: "@9", Left: 41, Width: 159, Height: 50, Last: true}

	cases := []struct {
		name  string
		panes []Pane
		self  string
		want  string // "" for no pane
	}{
		{"editor beside the tree", []Pane{ft, hx, other}, "%1", "%2"},
		{"alone in the window", []Pane{ft, other}, "%1", ""},
		{"no self", []Pane{ft, hx}, "", ""},
		{"unknown self", []Pane{ft, hx}, "%7", ""},
		// A pane below ft, sharing its column, is not to the right of it.
		{
			"below is not right",
			[]Pane{
				{ID: "%1", WindowID: "@8", Left: 0, Width: 40, Height: 25},
				{ID: "%3", WindowID: "@8", Left: 0, Top: 26, Width: 40, Height: 24, Last: true},
			},
			"%1", "",
		},
		// Nor is a pane to the left: ft is not always the first column.
		{
			"left is not right",
			[]Pane{
				{ID: "%4", WindowID: "@8", Left: 0, Width: 100, Height: 50, Last: true},
				{ID: "%1", WindowID: "@8", Left: 101, Width: 40, Height: 50},
			},
			"%1", "",
		},
		// The pane you came from beats a bigger one: an agent stacked under
		// the editor, last visited, is what gets split next.
		{
			"last active wins",
			[]Pane{
				ft,
				{ID: "%2", WindowID: "@8", Left: 41, Width: 159, Height: 25},
				{ID: "%5", WindowID: "@8", Left: 41, Top: 26, Width: 159, Height: 24, Last: true},
			},
			"%1", "%5",
		},
		// With no last-active candidate, the biggest one — the editor.
		{
			"then the largest",
			[]Pane{
				ft,
				{ID: "%5", WindowID: "@8", Left: 41, Width: 159, Height: 12},
				{ID: "%2", WindowID: "@8", Left: 41, Top: 13, Width: 159, Height: 37},
			},
			"%1", "%2",
		},
		// Equal sizes fall back to position, never to list order.
		{
			"then leftmost, then top",
			[]Pane{
				{ID: "%7", WindowID: "@8", Left: 120, Width: 78, Height: 24},
				ft,
				{ID: "%6", WindowID: "@8", Left: 41, Top: 26, Width: 78, Height: 24},
				{ID: "%5", WindowID: "@8", Left: 41, Width: 78, Height: 24},
			},
			"%1", "%5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := PaneRightOf(tc.panes, tc.self)
			if got := map[bool]string{true: p.ID, false: ""}[ok]; got != tc.want {
				t.Errorf("PaneRightOf = %q, want %q", got, tc.want)
			}
		})
	}
}
