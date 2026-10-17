package tmux

import (
	"slices"
	"testing"
)

func TestParsePanes(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []Pane
	}{
		{"empty", "", nil},
		{
			"one window, two panes",
			"%19\t$13\t@13\tzsh\t/dev/ttys003\t0\t0\t80\t24\t0\n%25\t$13\t@13\thx\t/dev/ttys006\t0\t0\t80\t24\t0\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh", TTY: "/dev/ttys003", Width: 80, Height: 24},
				{ID: "%25", SessionID: "$13", WindowID: "@13", Command: "hx", TTY: "/dev/ttys006", Width: 80, Height: 24},
			},
		},
		{
			"across sessions",
			"%19\t$13\t@13\tzsh\t/dev/ttys003\t0\t0\t80\t24\t0\n%11\t$8\t@8\tft\t/dev/ttys009\t0\t0\t80\t24\t0\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh", TTY: "/dev/ttys003", Width: 80, Height: 24},
				{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft", TTY: "/dev/ttys009", Width: 80, Height: 24},
			},
		},
		// One unreadable pane must not hide the rest, the way ParseList
		// skips a malformed session.
		{
			"short line skipped",
			"%19\t$13\t@13\tzsh\t/dev/ttys003\t0\t0\t80\t24\t0\nbroken\n%11\t$8\t@8\tft\t/dev/ttys009\t0\t0\t80\t24\t0\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh", TTY: "/dev/ttys003", Width: 80, Height: 24},
				{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft", TTY: "/dev/ttys009", Width: 80, Height: 24},
			},
		},
		{"blank lines", "\n\n", nil},
		{"crlf", "%11\t$8\t@8\tft\t/dev/ttys009\t0\t0\t80\t24\t0\r\n", []Pane{{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft", TTY: "/dev/ttys009", Width: 80, Height: 24}}},
		// A pane running nothing tmux can name still has a location, which is
		// the only field routing needs.
		// Geometry and the last-pane flag, which the split-below key routes on.
		{
			"geometry",
			"%25\t$13\t@13\thx\t/dev/ttys006\t41\t0\t120\t50\t1\n",
			[]Pane{{ID: "%25", SessionID: "$13", WindowID: "@13", Command: "hx", TTY: "/dev/ttys006",
				Left: 41, Width: 120, Height: 50, Last: true}},
		},
		// A pre-geometry line (an older field count) is malformed, not zeroed.
		{"short of geometry", "%11\t$8\t@8\tft\t/dev/ttys009\n", nil},
		{"empty command", "%11\t$8\t@8\t\t/dev/ttys009\t0\t0\t80\t24\t0\n", []Pane{{ID: "%11", SessionID: "$8", WindowID: "@8", TTY: "/dev/ttys009", Width: 80, Height: 24}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParsePanes(tc.out)
			if len(got) != len(tc.want) {
				t.Fatalf("ParsePanes() = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("pane %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The format string is what ParsePanes' field count is checked against; if one
// grows without the other the parse silently drops every line.
func TestPaneFormatMatchesFieldCount(t *testing.T) {
	if got, want := len(paneFields), 10; got != want {
		t.Fatalf("paneFields = %d, want %d", got, want)
	}
	if PaneFormat != "#{pane_id}\t#{session_id}\t#{window_id}\t#{pane_current_command}\t#{pane_tty}"+
		"\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_last}" {
		t.Errorf("PaneFormat = %q", PaneFormat)
	}
}

func TestBusyPanes(t *testing.T) {
	panes := []Pane{
		{ID: "%1", SessionID: "$1", Command: "ft"},
		{ID: "%2", SessionID: "$1", Command: "zsh"},
		{ID: "%3", SessionID: "$1", Command: "hx"},
		{ID: "%4", SessionID: "$1", Command: "-zsh"},
		{ID: "%5", SessionID: "$1", Command: "tmux"}, // a nested client: ctrl+w
		{ID: "%6", SessionID: "$1", Command: "delta"},
		{ID: "%7", SessionID: "$2", Command: "claude"}, // another session
	}
	cases := []struct {
		name      string
		self      string
		want      []string
		wantFound bool
	}{
		{"busy panes in ft's session only", "%1", []string{"%3", "%6"}, true},
		{"self is never busy", "%3", []string{"%1", "%6"}, true},
		{"alone in its session", "%7", nil, true},
		{"self missing", "%9", nil, false},
		{"outside tmux", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			busy, found := BusyPanes(panes, tc.self)
			var got []string
			for _, p := range busy {
				got = append(got, p.ID)
			}
			if found != tc.wantFound || !slices.Equal(got, tc.want) {
				t.Errorf("BusyPanes(%q) = %v, %v; want %v, %v", tc.self, got, found, tc.want, tc.wantFound)
			}
		})
	}
}
