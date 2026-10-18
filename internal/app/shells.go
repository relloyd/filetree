package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/tmux"
)

// openShell is one of the "n" chord's arrows: a shell in the selection's
// directory, in a new pane on that side of the tree.
//
// These used to be templates ("n" was split-window -fh, "N" split-window -h).
// They are actions now because "below the pane to the right" depends on the
// window: which pane is there, and whether anything is, is read from tmux at
// the moment of the key press and decided by tmux.PlanShell, which is pure and
// table-tested. The calls are made inline, like ctrl+w's — a key press is
// allowed a few milliseconds of tmux.
//
// A pane list that cannot be read is not fatal: PlanShell given nothing opens
// the right-hand pane, which is still a shell beside the tree. LeftEdge needs
// no list at all, so it does not pay for a server-wide one.
func (m *Model) openShell(side tmux.Side) func() (tea.Model, tea.Cmd) {
	return func() (tea.Model, tea.Cmd) {
		if m.selfPane == "" {
			return m, m.note("not running inside tmux", true)
		}
		dir := m.createTargetDir()
		var panes []tmux.Pane
		if side != tmux.LeftEdge {
			panes, _ = tmux.ListPanes() // nil on error, which PlanShell handles
		}
		plan := tmux.PlanShell(panes, m.selfPane, side)
		// Read before the split, while the window still has the shape the
		// user gave it — see openSessionPane.
		var before, window int
		if plan.KeepWidth {
			before, window = m.paneWidths()
		}
		if err := tmux.SplitShell(plan, dir); err != nil {
			return m, m.note(err.Error(), true)
		}
		if plan.KeepWidth {
			m.keepSidebarWidth(before, window)
		}
		return m, nil
	}
}
