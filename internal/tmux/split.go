package tmux

// Side is where the "n" chord puts a new shell, as seen from the tree.
type Side int

const (
	// Beside is immediately to the right of ft, between it and whatever is
	// already there.
	Beside Side = iota
	// Below and Above divide the pane to the right of ft, the new shell taking
	// the bottom or the top half.
	Below
	Above
	// LeftEdge is full height at the window's left edge, with ft pushed right.
	LeftEdge
)

// Split is one split-window call, worked out before it is made: the pane to
// divide, the flags saying how, and whether ft's own width needs putting back
// afterwards.
type Split struct {
	Target string
	Flags  []string

	// KeepWidth is set for a full-size (-f) split, the only kind that takes
	// columns from every pane in the window, ft's own included.
	KeepWidth bool
}

// PlanShell decides how to open a shell on side of self. Pure, so the choice
// of pane is table-tested; SplitShell makes the call.
//
// Beside splits the pane *touching* ft's right edge, with -b so the new one
// lands on its left — between it and ft. Dividing that pane rather than ft's
// own is what keeps the tree its width: a plain "-h" on ft halves a sidebar.
// It has to be the touching pane and not PaneRightOf's pick, which may be two
// panes over: "-hb" on that one would open the shell between the two others.
// When the column beside ft is a stack, only the one cell is divided, so the
// shell is as tall as that cell.
//
// Below and Above divide PaneRightOf's pick, the same pane "ctrl+s" in T
// splits, so the two keys agree about which pane is "the one on the right".
//
// With nothing to the right, every side but LeftEdge opens the right-hand pane
// instead: a shell beside the tree is what all three were reaching for. That
// split is full height at the right edge, so a narrow ft beside nothing still
// gets a column of its own, and is the one case that needs its width put back.
func PlanShell(panes []Pane, self string, side Side) Split {
	var (
		p  Pane
		ok bool
	)
	switch side {
	case LeftEdge:
		return Split{Target: self, Flags: []string{"-h", "-f", "-b"}, KeepWidth: true}
	case Beside:
		if p, ok = paneTouchingRight(panes, self); ok {
			return Split{Target: p.ID, Flags: []string{"-h", "-b"}}
		}
	case Below, Above:
		if p, ok = PaneRightOf(panes, self); ok {
			flags := []string{"-v"}
			if side == Above {
				flags = append(flags, "-b")
			}
			return Split{Target: p.ID, Flags: flags}
		}
	}
	// -l 70% is the share "t" gives the editor it opens the same way. The
	// percentage form needs tmux 3.1, which "t" has asked for all along.
	return Split{Target: self, Flags: []string{"-h", "-f", "-l", "70%"}, KeepWidth: true}
}

// paneTouchingRight is the pane across ft's right border: in its window,
// starting one column past ft's last (the border itself takes a column), and
// sharing at least one row with it. Several qualify when that column is a
// stack, and they are ranked as PaneRightOf ranks its candidates.
func paneTouchingRight(panes []Pane, self string) (Pane, bool) {
	me, found := findPane(panes, self)
	if !found {
		return Pane{}, false
	}
	var best Pane
	ok := false
	for _, p := range panes {
		if p.WindowID != me.WindowID || p.ID == self || p.Left != me.Left+me.Width+1 {
			continue
		}
		if p.Top >= me.Top+me.Height || me.Top >= p.Top+p.Height {
			continue
		}
		if !ok || rightPaneBefore(p, best) {
			best, ok = p, true
		}
	}
	return best, ok
}
