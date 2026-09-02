package app

import (
	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/tmux"
)

// finderPane records the resize ft made to its own pane when the finder
// opened, so leaving can undo it.
//
// applied is what ft asked for, and is the consent check on the way out: if
// the pane is no longer that wide, someone else has had a say since — the user
// dragging a border, ctrl+j/ctrl+k, another pane opening or closing — and
// putting it back would be ft overruling them. A zero applied means ft never
// resized anything and there is nothing to undo.
type finderPane struct {
	restore int
	applied int
}

// syncFinderPaneWidth widens ft's pane on the way into the finder and puts it
// back on the way out. Called from Update, off the mode transition.
//
// Every path is silent. These are the same waters as the ctrl+l/ctrl+j/ctrl+k
// commands, which are silent by design because a pane operation that has
// nothing to act on is not a mistake worth a status line — and outside tmux
// (selfPane empty, or tmux not installed) there is nothing to say at all.
func (m *Model) syncFinderPaneWidth(entering bool) {
	if m.selfPane == "" {
		return
	}
	if entering {
		m.widenForFinder()
		return
	}
	m.restoreFinderWidth()
}

func (m *Model) widenForFinder() {
	m.finderPane = finderPane{}
	if m.cfg == nil {
		return
	}
	pane, window, err := tmux.PaneWidths(m.selfPane)
	if err != nil {
		return
	}
	target, ok := planFinderWiden(m.cfg.General.FinderWidth, pane, window)
	if !ok {
		return
	}
	if err := tmux.ResizePaneWidth(m.selfPane, target); err != nil {
		return
	}
	m.finderPane = finderPane{restore: pane, applied: target}
}

func (m *Model) restoreFinderWidth() {
	fp := m.finderPane
	m.finderPane = finderPane{}
	if fp.applied == 0 {
		return
	}
	pane, _, err := tmux.PaneWidths(m.selfPane)
	if err != nil {
		return
	}
	target, ok := planFinderRestore(fp, pane)
	if !ok {
		return
	}
	_ = tmux.ResizePaneWidth(m.selfPane, target)
}

// planFinderWiden decides the width to ask for when the finder opens, given
// the configured setting and the pane's current size. ok is false when ft
// should leave the pane exactly as it is.
//
// ft only ever widens. An ft that already has the window, or that the user has
// deliberately pulled out to 80 columns, is telling you it does not need help;
// narrowing it to the configured target would be ft taking space away from the
// finder in the name of giving it some.
func planFinderWiden(spec string, pane, window int) (int, bool) {
	target := config.FinderWidthCells(spec, window)
	if target == 0 || pane >= target {
		return 0, false
	}
	return target, true
}

// planFinderRestore decides the width to go back to when the finder closes.
// ok is false when ft made no resize, or when the pane has moved since and the
// restore is no longer ft's to make.
func planFinderRestore(fp finderPane, pane int) (int, bool) {
	if fp.applied == 0 || pane != fp.applied {
		return 0, false
	}
	return fp.restore, true
}
