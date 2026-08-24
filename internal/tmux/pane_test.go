package tmux

import "testing"

func TestParsePanes(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []Pane
	}{
		{"empty", "", nil},
		{
			"one window, two panes",
			"%19\t$13\t@13\tzsh\n%25\t$13\t@13\thx\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh"},
				{ID: "%25", SessionID: "$13", WindowID: "@13", Command: "hx"},
			},
		},
		{
			"across sessions",
			"%19\t$13\t@13\tzsh\n%11\t$8\t@8\tft\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh"},
				{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft"},
			},
		},
		// One unreadable pane must not hide the rest, the way ParseList
		// skips a malformed session.
		{
			"short line skipped",
			"%19\t$13\t@13\tzsh\nbroken\n%11\t$8\t@8\tft\n",
			[]Pane{
				{ID: "%19", SessionID: "$13", WindowID: "@13", Command: "zsh"},
				{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft"},
			},
		},
		{"blank lines", "\n\n", nil},
		{"crlf", "%11\t$8\t@8\tft\r\n", []Pane{{ID: "%11", SessionID: "$8", WindowID: "@8", Command: "ft"}}},
		// A pane running nothing tmux can name still has a location, which is
		// the only field routing needs.
		{"empty command", "%11\t$8\t@8\t\n", []Pane{{ID: "%11", SessionID: "$8", WindowID: "@8"}}},
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
	if got, want := len(paneFields), 4; got != want {
		t.Fatalf("paneFields = %d, want %d", got, want)
	}
	if PaneFormat != "#{pane_id}\t#{session_id}\t#{window_id}\t#{pane_current_command}" {
		t.Errorf("PaneFormat = %q", PaneFormat)
	}
}
