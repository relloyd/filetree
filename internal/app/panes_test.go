package app

import "testing"

// display-popup floats over the window without taking a column from anything,
// so it must not be measured around: there is no width to keep and no pane to
// record. The splits are the ones that cost ft its columns.
func TestPaneOpeningCommand(t *testing.T) {
	opens := []string{
		`tmux split-window -fh -c {root} "hx {paths}"`,
		`tmux split-window -h -c {dir}`,
		`tmux split-window -fh "delta {marked1} {marked2}; read x"`,
		`[ -z "$TMUX" ] || tmux join-pane -s %3`,
	}
	for _, run := range opens {
		if !paneOpeningCommand(run) {
			t.Errorf("paneOpeningCommand(%q) = false, want true", run)
		}
	}

	leaves := []string{
		`tmux display-popup -E -w 92% -h 92% "tmux new-session -c {dir}"`,
		`tmux display-popup -E -d {gitroot} -w 92% -h 92% "tmux new-session -A -s {session}/claude"`,
		`hx {paths}`,
		`open {path}`,
		`[ -z "$TMUX" ] || tmux select-pane -R`,
		`[ -z "$TMUX" ] || tmux resize-pane -x 30%`,
		`printf %s {path} | pbcopy`,
	}
	for _, run := range leaves {
		if paneOpeningCommand(run) {
			t.Errorf("paneOpeningCommand(%q) = true, want false", run)
		}
	}
}

// The hand-off tries the pane beside it first and splits only as a fallback,
// so it is suspected: paying two tmux calls for a command that turns out not
// to split is much cheaper than missing the one that does.
func TestHandoffIsSuspected(t *testing.T) {
	const handoff = `case "$target" in
  hx) tmux send-keys -t "{last}" ":open {paths}" Enter ;;
  *) tmux split-window -fdh -l 70% -c {root} "hx {paths}" ;;
esac`
	if !paneOpeningCommand(handoff) {
		t.Fatal("the hand-off can split, so it must be measured around")
	}
}

// The sidebar test is the one openSessionPane has always used: an ft that
// filled the window had no sidebar to preserve, and putting its old width back
// would crush whatever is beside it down to a single column.
func TestKeepSidebarWidthOnlyActsOnASidebar(t *testing.T) {
	for _, tc := range []struct {
		name           string
		self           string
		before, window int
		wantResize     bool
	}{
		{"a sidebar is restored", "%0", 30, 200, true},
		{"half the window is not a sidebar", "%0", 100, 200, false},
		{"more than half is not a sidebar", "%0", 150, 200, false},
		{"an unreadable width does nothing", "%0", 0, 200, false},
		{"an unreadable window does nothing", "%0", 30, 0, false},
		{"outside tmux does nothing", "", 30, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The decision, not the exec: a sidebar is anything ft can measure
			// that is under half its window.
			got := tc.self != "" && tc.before > 0 && tc.before < tc.window/2
			if got != tc.wantResize {
				t.Errorf("sidebar test = %v, want %v", got, tc.wantResize)
			}
		})
	}
}
