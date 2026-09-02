package config

import (
	"strconv"
	"strings"
)

// DefaultFinderWidth is how wide ft asks its own pane to be while the "/"
// finder is open. Sixty percent is enough for a repo-relative path and a
// readable slice of the line it matched, and still leaves a usable third of
// the window to whatever ft is sitting beside.
const DefaultFinderWidth = "60%"

// FinderWidthOff is the setting that turns the resize off entirely.
const FinderWidthOff = "off"

// minFinderWidthCells is the narrowest target worth asking for. Below this the
// finder is no better off than it was, and a stray "finder_width = 3" would
// squeeze the pane to nothing on the way in.
const minFinderWidthCells = 20

// ValidFinderWidth reports whether a general.finder_width setting is one ft
// can act on: "off", a percentage like "60%", or a plain column count.
//
// The bounds are deliberately loose — a percentage over 100 or a column count
// wider than the window are not errors, because the window they are measured
// against is not known until ft is running and may change afterwards.
// FinderWidthCells clamps instead.
func ValidFinderWidth(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, FinderWidthOff) {
		return true
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
	return err == nil && n > 0
}

// FinderWidthCells resolves a general.finder_width setting against the current
// window width, in columns. It returns 0 when ft should leave the pane alone —
// "off", an unreadable setting, or a window too narrow to divide up.
//
// The result is clamped to the window: a pane cannot be wider than the window
// holding it, and tmux would silently cap it anyway. It is also floored at
// minFinderWidthCells, so a target smaller than that is treated as "off"
// rather than as an instruction to crush the pane.
func FinderWidthCells(s string, window int) int {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, FinderWidthOff) || window <= 0 {
		return 0
	}
	pct := strings.HasSuffix(s, "%")
	n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
	if err != nil || n <= 0 {
		return 0
	}
	cells := n
	if pct {
		cells = window * n / 100
	}
	if cells > window {
		cells = window
	}
	if cells < minFinderWidthCells {
		return 0
	}
	return cells
}
