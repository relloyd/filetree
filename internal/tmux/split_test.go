package tmux

import (
	"slices"
	"testing"
)

// The layouts are 200x50 windows with ft as a 40-column sidebar at the left
// (%1), as the probe that settled the flags measured them. Borders take a
// column, so the pane beside ft starts at 41.
func TestPlanShell(t *testing.T) {
	ft := Pane{ID: "%1", WindowID: "@1", Left: 0, Top: 0, Width: 40, Height: 50}
	editor := Pane{ID: "%2", WindowID: "@1", Left: 41, Top: 0, Width: 159, Height: 50}
	// ft | A | B, with B the pane you were last in: PaneRightOf picks B, and
	// only A touches ft.
	a := Pane{ID: "%3", WindowID: "@1", Left: 41, Top: 0, Width: 79, Height: 50}
	b := Pane{ID: "%4", WindowID: "@1", Left: 121, Top: 0, Width: 78, Height: 50, Last: true}
	// ft | a stack of two, the lower one last-active.
	top := Pane{ID: "%5", WindowID: "@1", Left: 41, Top: 0, Width: 159, Height: 25}
	bottom := Pane{ID: "%6", WindowID: "@1", Left: 41, Top: 26, Width: 159, Height: 24, Last: true}
	// Another window's pane, positioned exactly where an editor would be.
	elsewhere := Pane{ID: "%9", WindowID: "@2", Left: 41, Top: 0, Width: 159, Height: 50}

	edge := Split{Target: "%1", Flags: []string{"-h", "-f", "-l", "70%"}, KeepWidth: true}
	left := Split{Target: "%1", Flags: []string{"-h", "-f", "-b"}, KeepWidth: true}

	for _, tc := range []struct {
		name  string
		panes []Pane
		side  Side
		want  Split
	}{
		{"beside splits the editor's left side", []Pane{ft, editor}, Beside, Split{Target: "%2", Flags: []string{"-h", "-b"}}},
		{"below splits the editor", []Pane{ft, editor}, Below, Split{Target: "%2", Flags: []string{"-v"}}},
		{"above splits the editor, new pane first", []Pane{ft, editor}, Above, Split{Target: "%2", Flags: []string{"-v", "-b"}}},
		{"left edge splits the window", []Pane{ft, editor}, LeftEdge, left},

		// The case paneTouchingRight exists for: -hb on B would put the shell
		// between A and B, nowhere near the tree.
		{"beside takes the touching pane, not the last one", []Pane{ft, a, b}, Beside, Split{Target: "%3", Flags: []string{"-h", "-b"}}},
		{"below keeps PaneRightOf's pick", []Pane{ft, a, b}, Below, Split{Target: "%4", Flags: []string{"-v"}}},

		{"beside a stack takes the last-active cell", []Pane{ft, top, bottom}, Beside, Split{Target: "%6", Flags: []string{"-h", "-b"}}},

		{"nothing right: beside opens at the edge", []Pane{ft}, Beside, edge},
		{"nothing right: below falls back", []Pane{ft}, Below, edge},
		{"nothing right: above falls back", []Pane{ft}, Above, edge},
		{"another window does not count", []Pane{ft, elsewhere}, Beside, edge},
		{"left edge alone", []Pane{ft}, LeftEdge, left},

		// An unreadable pane list still opens something rather than nothing.
		{"no pane list", nil, Below, edge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanShell(tc.panes, "%1", tc.side)
			if got.Target != tc.want.Target || !slices.Equal(got.Flags, tc.want.Flags) || got.KeepWidth != tc.want.KeepWidth {
				t.Errorf("PlanShell() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// ft in the middle column: the pane to its left is not beside it, and one that
// starts at the right column but entirely below ft's rows does not touch it.
func TestPaneTouchingRight(t *testing.T) {
	left := Pane{ID: "%2", WindowID: "@1", Left: 0, Top: 0, Width: 39, Height: 50}
	ft := Pane{ID: "%1", WindowID: "@1", Left: 40, Top: 0, Width: 40, Height: 25}
	under := Pane{ID: "%3", WindowID: "@1", Left: 40, Top: 26, Width: 40, Height: 24}
	low := Pane{ID: "%4", WindowID: "@1", Left: 81, Top: 26, Width: 119, Height: 24}
	if p, ok := paneTouchingRight([]Pane{left, ft, under, low}, "%1"); ok {
		t.Errorf("paneTouchingRight() = %s, want none", p.ID)
	}
	high := Pane{ID: "%5", WindowID: "@1", Left: 81, Top: 0, Width: 119, Height: 25}
	if p, ok := paneTouchingRight([]Pane{left, ft, under, low, high}, "%1"); !ok || p.ID != "%5" {
		t.Errorf("paneTouchingRight() = %s %v, want %%5", p.ID, ok)
	}
}
