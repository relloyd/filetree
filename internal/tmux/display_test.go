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

// ft must never find itself: its own pane is excluded by id, so even a client
// on ft's own tty cannot be mistaken for an agent sharing the window.
func TestPaneShowingExcludesSelf(t *testing.T) {
	panes := []Pane{{ID: "%11", WindowID: "@8", TTY: "/dev/ttys001", Command: "ft"}}
	clients := []Client{{TTY: "/dev/ttys001", Session: "ft/repo/main/claude"}}
	if _, _, ok := PaneShowing(panes, clients, "%11", func(string) bool { return true }); ok {
		t.Error("ft found itself")
	}
}
