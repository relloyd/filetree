package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ft only ever widens for the finder. An ft that already has the room — one
// that owns the window, or that the user has deliberately pulled out — is
// telling you it does not need help, and narrowing it to the configured target
// would take space away in the name of giving some.
func TestPlanFinderWidenOnlyWidens(t *testing.T) {
	for _, tc := range []struct {
		name         string
		spec         string
		pane, window int
		want         int
		wantOK       bool
	}{
		{"narrow sidebar widens", "60%", 30, 200, 120, true},
		{"already wider is left alone", "60%", 160, 200, 0, false},
		{"exactly at target is left alone", "60%", 120, 200, 0, false},
		{"off does nothing", "off", 30, 200, 0, false},
		{"empty does nothing", "", 30, 200, 0, false},
		{"a column count is taken as-is", "90", 30, 200, 90, true},
		{"a target wider than the window is clamped", "150", 30, 100, 100, true},
		{"a target below the floor is ignored", "10", 5, 200, 0, false},
		{"nonsense is ignored rather than fatal", "wide", 30, 200, 0, false},
		{"an unknown window is ignored", "60%", 30, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := planFinderWiden(tc.spec, tc.pane, tc.window)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("planFinderWiden(%q, %d, %d) = %d, %v; want %d, %v",
					tc.spec, tc.pane, tc.window, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// The restore is consent-checked. A pane that is no longer the width ft asked
// for has been moved by someone else since — the user dragging a border,
// ctrl+j, another pane opening — and putting it back would be ft overruling
// them.
func TestPlanFinderRestoreDefersToAnythingThatMovedThePane(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fp     finderPane
		pane   int
		want   int
		wantOK bool
	}{
		{"untouched pane is restored", finderPane{restore: 30, applied: 120}, 120, 30, true},
		{"resized since, so left alone", finderPane{restore: 30, applied: 120}, 90, 0, false},
		{"ft never resized, nothing to undo", finderPane{}, 120, 0, false},
		{"a zero record ignores a zero pane", finderPane{}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := planFinderRestore(tc.fp, tc.pane)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("planFinderRestore(%+v, %d) = %d, %v; want %d, %v",
					tc.fp, tc.pane, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// A widen followed by a restore has to land back where it started, or a
// finder opened and closed a few times walks the pane across the window.
func TestFinderWidenRestoreRoundTrips(t *testing.T) {
	const window, start = 200, 30
	target, ok := planFinderWiden("60%", start, window)
	if !ok {
		t.Fatal("a 30-column pane in a 200-column window should widen")
	}
	back, ok := planFinderRestore(finderPane{restore: start, applied: target}, target)
	if !ok || back != start {
		t.Errorf("round trip landed at %d (ok=%v), want %d", back, ok, start)
	}
}

// syncFinderPaneWidth must be inert outside tmux, where selfPane is empty:
// every tmux call it could make would resolve against the most recently used
// session and resize a pane in a window nobody is looking at.
func TestSyncFinderPaneWidthIsInertOutsideTmux(t *testing.T) {
	m := finderModel()
	m.selfPane = ""
	m.finderPane = finderPane{restore: 30, applied: 120}
	m.syncFinderPaneWidth(true)
	m.syncFinderPaneWidth(false)
	if m.finderPane != (finderPane{restore: 30, applied: 120}) {
		t.Errorf("outside tmux the record was touched: %+v", m.finderPane)
	}
}

// Only a real crossing counts. Reopening the finder from inside itself — "/"
// to "F", or the bookmark list to the session list — stays on the same side of
// the boundary, and a resize on each would walk the pane across the window.
func TestFinderBoundaryOnlyFiresOnACrossing(t *testing.T) {
	for _, tc := range []struct {
		before, after      mode
		wantEnter, wantAny bool
	}{
		{modeNormal, modeFuzzy, true, true},
		{modeFuzzy, modeNormal, false, true},
		{modeFuzzy, modeHelp, false, true},
		{modeHelp, modeFuzzy, true, true},
		{modeFuzzy, modeFuzzy, false, false},
		{modeNormal, modeNormal, false, false},
		{modeNormal, modeHelp, false, false},
		{modePrompt, modeConfirm, false, false},
	} {
		enter, any := finderBoundary(tc.before, tc.after)
		if enter != tc.wantEnter || any != tc.wantAny {
			t.Errorf("finderBoundary(%d, %d) = %v, %v; want %v, %v",
				tc.before, tc.after, enter, any, tc.wantEnter, tc.wantAny)
		}
	}
}

// Every way into the finder has to leave the mode where the boundary check can
// see it, and esc has to bring it back — otherwise a pane widened on the way
// in is never given back on the way out.
func TestEveryFinderEntryPointCrossesTheBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*Model) (tea.Model, tea.Cmd)
	}{
		{"/", func(m *Model) (tea.Model, tea.Cmd) { return m.startFuzzy() }},
		{"f", func(m *Model) (tea.Model, tea.Cmd) { return m.resumeFuzzy() }},
		{"recent", func(m *Model) (tea.Model, tea.Cmd) { return m.startRecent() }},
		{"B", func(m *Model) (tea.Model, tea.Cmd) { return m.startBookmarks() }},
		{"T", func(m *Model) (tea.Model, tea.Cmd) { return m.startTmuxSessions() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := rootedModel(t, t.TempDir())
			m.mode = modeNormal
			tc.open(m)
			if m.mode != modeFuzzy {
				t.Fatalf("mode after opening = %d, want modeFuzzy", m.mode)
			}
			if _, crossed := finderBoundary(modeNormal, m.mode); !crossed {
				t.Error("opening the finder did not read as a crossing")
			}
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.mode == modeFuzzy {
				t.Fatal("esc left the finder open")
			}
			if _, crossed := finderBoundary(modeFuzzy, m.mode); !crossed {
				t.Error("leaving the finder did not read as a crossing")
			}
		})
	}
}
