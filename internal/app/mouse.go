package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const doubleClickWindow = 400 * time.Millisecond

func (m *Model) handleWheel(mo tea.Mouse) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m, nil
	}
	switch mo.Button {
	case tea.MouseWheelUp:
		m.scroll -= 3
	case tea.MouseWheelDown:
		m.scroll += 3
	}
	m.clampScroll()
	return m, nil
}

func (m *Model) handleClick(mo tea.Mouse) (tea.Model, tea.Cmd) {
	if mo.Button != tea.MouseLeft {
		return m, nil
	}
	switch m.mode {
	case modeHelp:
		m.mode = modeNormal
		return m, nil
	case modeNormal:
	default:
		return m, nil
	}

	if mo.Y == 0 {
		switch {
		case mo.X >= m.zoneHidden[0] && mo.X < m.zoneHidden[1]:
			return m.toggleHidden()
		case mo.X >= m.zoneIgnored[0] && mo.X < m.zoneIgnored[1]:
			return m.toggleIgnored()
		}
		return m, nil
	}

	idx := m.scroll + mo.Y - 1
	if idx < 0 || idx >= len(m.rows) || mo.Y > m.treeHeight() {
		return m, nil
	}
	now := time.Now()
	double := idx == m.lastClickRow && now.Sub(m.lastClickTime) < doubleClickWindow
	m.lastClickTime, m.lastClickRow = now, idx
	m.cursor = idx
	m.ensureVisible()

	n := m.rows[idx].Node
	if double {
		if n.IsDir {
			return m.toggleDir(n)
		}
		return m.runCommand(m.cfg.DefaultCommand)
	}
	// Single click on a directory's chevron toggles it immediately.
	indent := m.rows[idx].Depth * 2
	if n.IsDir && mo.X >= indent && mo.X < indent+2 {
		return m.toggleDir(n)
	}
	return m, nil
}
