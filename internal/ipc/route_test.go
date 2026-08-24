package ipc

import "testing"

func TestContains(t *testing.T) {
	cases := []struct {
		name, root, target string
		want               bool
	}{
		{"child", "/a/proj", "/a/proj/x.go", true},
		{"deep child", "/a/proj", "/a/proj/i/app/view.go", true},
		{"root itself", "/a/proj", "/a/proj", true},
		{"trailing separator", "/a/proj/", "/a/proj/x.go", true},
		{"unclean target", "/a/proj", "/a/proj/./sub/../x.go", true},
		// The case a string prefix gets wrong.
		{"name-prefix sibling", "/a/proj", "/a/proj2/x.go", false},
		{"parent", "/a/proj", "/a/x.go", false},
		{"unrelated", "/a/proj", "/etc/hosts", false},
		{"filesystem root contains all", "/", "/etc/hosts", true},
		{"empty root", "", "/a/x.go", false},
		{"empty target", "/a/proj", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Contains(tc.root, tc.target); got != tc.want {
				t.Errorf("Contains(%q, %q) = %v, want %v", tc.root, tc.target, got, tc.want)
			}
		})
	}
}

// The pane layout every routing case below is scored against:
//
//	session $1  window @1  ->  %1 (the editor, i.e. the caller), %2 (an ft)
//	session $1  window @2  ->  %3 (an ft, same session, other window)
//	session $2  window @3  ->  %4 (an ft, another session entirely)
var testPanes = map[string]Location{
	"%1": {Session: "$1", Window: "@1"},
	"%2": {Session: "$1", Window: "@1"},
	"%3": {Session: "$1", Window: "@2"},
	"%4": {Session: "$2", Window: "@3"},
}

var caller = Location{Session: "$1", Window: "@1"}

func TestPick(t *testing.T) {
	cases := []struct {
		name    string
		cands   []Candidate
		target  string
		wantPID int
		wantOK  bool
	}{
		{
			name:   "nothing running",
			cands:  nil,
			target: "/a/proj/x.go",
			wantOK: false,
		},
		{
			name:    "only one, and it covers the path",
			cands:   []Candidate{{PID: 10, Root: "/a/proj", Pane: "%4"}},
			target:  "/a/proj/x.go",
			wantPID: 10, wantOK: true,
		},
		{
			name:   "no instance covers the path",
			cands:  []Candidate{{PID: 10, Root: "/a/proj", Pane: "%2"}},
			target: "/etc/hosts",
			wantOK: false,
		},
		{
			name: "same window wins over same session",
			cands: []Candidate{
				{PID: 10, Root: "/a/proj", Pane: "%3"}, // same session
				{PID: 11, Root: "/a/proj", Pane: "%2"}, // same window
			},
			target:  "/a/proj/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			name: "same session wins over a stranger",
			cands: []Candidate{
				{PID: 10, Root: "/a/proj", Pane: "%4"}, // other session
				{PID: 11, Root: "/a/proj", Pane: "%3"}, // same session
			},
			target:  "/a/proj/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			// The case additive scoring gets wrong: the local pane has
			// re-rooted somewhere else and simply cannot show this file.
			name: "containment beats locality",
			cands: []Candidate{
				{PID: 10, Root: "/a/other", Pane: "%2"}, // same window, wrong root
				{PID: 11, Root: "/a/proj", Pane: "%4"},  // another session, right root
			},
			target:  "/a/proj/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			name: "deepest root wins among equals",
			cands: []Candidate{
				{PID: 10, Root: "/a", Pane: "%4"},
				{PID: 11, Root: "/a/proj/sub", Pane: "%4"},
				{PID: 12, Root: "/a/proj", Pane: "%4"},
			},
			target:  "/a/proj/sub/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			// Depth must never outrank being in the right window, however
			// deep the other root is.
			name: "locality beats depth",
			cands: []Candidate{
				{PID: 10, Root: "/a/proj/i/app/deep/deeper", Pane: "%4"},
				{PID: 11, Root: "/a", Pane: "%2"},
			},
			target:  "/a/proj/i/app/deep/deeper/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			name: "pid breaks a tie",
			cands: []Candidate{
				{PID: 21, Root: "/a/proj", Pane: "%2"},
				{PID: 12, Root: "/a/proj", Pane: "%2"},
			},
			target:  "/a/proj/x.go",
			wantPID: 12, wantOK: true,
		},
		{
			name: "an unknown pane is not local",
			cands: []Candidate{
				{PID: 10, Root: "/a/proj", Pane: "%99"}, // pane tmux never listed
				{PID: 11, Root: "/a/proj", Pane: "%3"},  // same session
			},
			target:  "/a/proj/x.go",
			wantPID: 11, wantOK: true,
		},
		{
			name:    "the root itself is a valid target",
			cands:   []Candidate{{PID: 10, Root: "/a/proj", Pane: "%2"}},
			target:  "/a/proj",
			wantPID: 10, wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Pick(tc.cands, testPanes, caller, tc.target)
			if ok != tc.wantOK {
				t.Fatalf("Pick() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.PID != tc.wantPID {
				t.Errorf("Pick() pid = %d, want %d", got.PID, tc.wantPID)
			}
		})
	}
}

// Outside tmux there is no caller pane, so every locality term is off and
// containment plus depth has to decide by itself. The trap this guards is an
// empty caller field matching an instance that also has none, which would make
// every candidate read as "in my window".
func TestPickOutsideTmux(t *testing.T) {
	cands := []Candidate{
		{PID: 10, Root: "/a", Pane: ""},
		{PID: 11, Root: "/a/proj", Pane: ""},
	}
	got, ok := Pick(cands, nil, Location{}, "/a/proj/x.go")
	if !ok {
		t.Fatal("Pick() not ok")
	}
	if got.PID != 11 {
		t.Errorf("Pick() pid = %d, want 11 (the deeper root)", got.PID)
	}
}

// An ft started from a shell inside tmux has a pane, but the caller may not
// be in tmux at all. Locality is simply unavailable then; it must not crash
// or match.
func TestPickCallerWithoutTmuxAgainstTmuxInstances(t *testing.T) {
	cands := []Candidate{
		{PID: 10, Root: "/a/proj", Pane: "%2"},
		{PID: 11, Root: "/a/proj/sub", Pane: "%4"},
	}
	got, ok := Pick(cands, testPanes, Location{}, "/a/proj/sub/x.go")
	if !ok {
		t.Fatal("Pick() not ok")
	}
	if got.PID != 11 {
		t.Errorf("Pick() pid = %d, want 11 (the deeper root)", got.PID)
	}
}
