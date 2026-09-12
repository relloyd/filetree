package app

import (
	"sort"

	tea "charm.land/bubbletea/v2"
)

// The help page is a filtered, scrollable list rather than a static card.
//
// It became one because it stopped fitting. Every command contributes a row,
// and columns only buy the room back on a wide pane: in a sidebar the page
// ended in "… 6 more" and those six were unreachable. Now typing narrows the
// list and the wheel moves through it, so nothing is merely off the end.
//
// Typing filters, which means the page gives up the letter keys that used to
// close it — "q" and "?" are now characters. "esc" is the way out, and it is
// the one people reach for anyway.

// helpInputWidth is how much of the title line the filter box takes: enough for
// a word or two, and never so much that the legend beside it has nowhere to go.
func helpInputWidth(paneWidth int) int {
	return clamp(paneWidth-24, 12, 30)
}

// handleHelpKey drives the help page: esc leaves, a few keys scroll, and
// everything else is filter text.
//
// The scroll keys are taken before the input sees them because a textinput
// would otherwise swallow up and down for its own history, leaving the page
// scrollable by mouse alone.
func (m *Model) handleHelpKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc":
		m.mode = modeNormal
		m.helpInput.Reset()
		m.helpScroll = 0
		return m, nil
	case "up":
		m.scrollHelp(-1)
		return m, nil
	case "down":
		m.scrollHelp(1)
		return m, nil
	case "pgup":
		m.scrollHelp(-m.helpPage())
		return m, nil
	case "pgdown":
		m.scrollHelp(m.helpPage())
		return m, nil
	}

	before := m.helpInput.Value()
	var cmd tea.Cmd
	m.helpInput, cmd = m.helpInput.Update(msg)
	// Only when the query actually changed: the page re-flows underneath a new
	// filter, and a scroll position measured against the old set would leave
	// the top of the new one off the screen.
	if m.helpInput.Value() != before {
		m.helpScroll = 0
	}
	return m, cmd
}

// scrollHelp moves the page by n rows, keeping it inside what there is.
func (m *Model) scrollHelp(n int) {
	m.helpScroll += n
	m.clampHelpScroll()
}

// clampHelpScroll keeps the offset between the top and the last screenful.
//
// Scrolling stops where the final row comes into view rather than where the
// rows run out, so the page cannot be scrolled into blankness — the same rule
// clampScroll applies to the tree.
func (m *Model) clampHelpScroll() {
	rows := m.helpVisibleRows()
	capacity := m.helpCapacity(rows)
	m.helpScroll = clamp(m.helpScroll, 0, max(0, len(rows)-capacity))
}

// helpPage is how far pgup and pgdn move: a screenful, less a row of overlap so
// something carries over and the eye can pick the list back up.
func (m *Model) helpPage() int {
	return max(1, m.helpCapacity(m.helpVisibleRows())-1)
}

// helpVisibleRows is the rows the filter lets through, in page order.
//
// The fuzzy match is the one the finder views use, so "!" excludes here as it
// does there. Its ranking is then thrown away and the page's own order
// restored: these rows are grouped by what they do — navigation, then the
// commands in catalogue order — and on a reference page that grouping is worth
// more than relevance. You are scanning for something, not picking a result.
func (m *Model) helpVisibleRows() []helpRow {
	rows := m.helpRows()
	q := parseFindQuery(m.helpInput.Value())
	if q.include == "" && len(q.exclude) == 0 {
		return rows
	}
	hay := make([]string, len(rows))
	for i, r := range rows {
		hay[i] = helpSearchText(r)
	}
	idxs, _ := applyFindQuery(q, hay)
	sort.Ints(idxs)
	out := make([]helpRow, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, rows[i])
	}
	return out
}

// helpSearchText is what a query matches against: both key columns and the
// description, so "ctrl" finds the chords and "worktree" finds the actions.
func helpSearchText(r helpRow) string {
	return r.key + " " + r.finderKey + " " + r.desc
}
