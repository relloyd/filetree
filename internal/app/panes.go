package app

import (
	"strings"

	"github.com/relloyd/filetree/internal/tmux"
)

// paneOpeningCommand reports whether a command's template looks like it opens
// a pane in ft's own window, and so is worth measuring around.
//
// A guess, deliberately: the templates are the user's own shell, and a command
// that splits from inside a script of its own cannot be spotted from here. It
// costs little to be wrong in either direction — a command wrongly suspected
// pays two tmux calls and restores the width it already had, and one wrongly
// missed leaves the widths exactly where they were before any of this.
//
// display-popup is not in the list and must not be: a popup floats over the
// window without taking a column from anything, so there is no width to keep.
func paneOpeningCommand(run string) bool {
	return strings.Contains(run, "split-window") ||
		strings.Contains(run, "join-pane") ||
		strings.Contains(run, "move-pane")
}

// paneWidths reads ft's own width and its window's, reporting zeroes for
// anything it cannot measure. Zero is the "do nothing" value in
// keepSidebarWidth, so an unreadable width degrades to leaving the pane alone
// rather than to a resize based on a guess.
func (m *Model) paneWidths() (pane, window int) {
	if m.selfPane == "" {
		return 0, 0
	}
	p, w, err := tmux.PaneWidths(m.selfPane)
	if err != nil {
		return 0, 0
	}
	return p, w
}

// keepSidebarWidth re-applies the width ft had before a split it ran took
// columns from every pane in the window, its own included.
//
// Only for a split ft opened itself, where ft knows it caused the disturbance.
// A pane closing is the mirror image and is deliberately not handled here: ft
// only hears about a close when its own pane resizes, and a close that leaves
// ft's width alone goes unnoticed — so the news arrives, if at all, attached
// to some later resize that ft would then wrongly undo. Widening and closing
// look identical from the width alone, and overruling a deliberate resize is
// worse than the drift. That one wants tmux's pane-exited hook, not a guess.
//
// It is skipped for an ft that filled the window, where there was no sidebar
// to preserve and restoring the old width would crush whatever was just opened
// down to a single column.
func (m *Model) keepSidebarWidth(before, window int) {
	keepSidebarWidth(m.selfPane, before, window)
}

// keepSidebarWidth is the model-free half, so the goroutine running a
// background command can put the width back the moment the split returns
// rather than a message round-trip later, where the jump would be visible.
func keepSidebarWidth(self string, before, window int) {
	if self == "" || before <= 0 || before >= window/2 {
		return
	}
	_ = tmux.ResizePaneWidth(self, before)
}
