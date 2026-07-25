package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/icons"
	"github.com/relloyd/filetree/internal/tree"
)

var (
	colSelBg   = lipgloss.Color("#264F78")
	colDim     = lipgloss.Color("#6D7A85")
	colError   = lipgloss.Color("#F14C4C")
	colOK      = lipgloss.Color("#73C991")
	colChanged = lipgloss.Color("#E2C08D")
	colTitle   = lipgloss.Color("#569CD6")
	colMark    = lipgloss.Color("#C586C0") // marks: magenta, distinct from git colours

	styleBase    = lipgloss.NewStyle()
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleError   = lipgloss.NewStyle().Foreground(colError)
	styleOK      = lipgloss.NewStyle().Foreground(colOK)
	styleTitle   = lipgloss.NewStyle().Foreground(colTitle).Bold(true)
	styleChevron = lipgloss.NewStyle().Foreground(colDim)
	styleChanged = lipgloss.NewStyle().Foreground(colChanged)
	styleMark    = lipgloss.NewStyle().Foreground(colMark)

	codeColors = map[gitx.Code]color.Color{
		gitx.Ignored:   lipgloss.Color("#6D6D6D"),
		gitx.Untracked: lipgloss.Color("#73C991"),
		gitx.Staged:    lipgloss.Color("#4EC9B0"),
		gitx.Modified:  lipgloss.Color("#E2C08D"),
		gitx.Conflict:  lipgloss.Color("#F14C4C"),
	}
)

func (m *Model) View() tea.View {
	var body string
	switch m.mode {
	case modeFuzzy:
		body = m.renderFuzzy()
	case modeHelp:
		body = m.renderHelp()
	default:
		body = m.renderTree()
	}
	content := m.renderHeader() + "\n" + body + "\n" + m.renderStatus()
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "ft — " + abbrevHome(m.tr.Root.Path)
	return v
}

// renderHeader draws the title, root path, and the two clickable toggle
// buttons, recording their x-ranges for mouse hit-testing.
func (m *Model) renderHeader() string {
	hBtn := checkbox("hidden", m.showHidden)
	iBtn := checkbox("ignored", m.showIgnored)
	rightPlain := hBtn + "  " + iBtn + " "
	rightStart := max(0, m.width-len(rightPlain))
	m.zoneHidden = [2]int{rightStart, rightStart + len(hBtn)}
	m.zoneIgnored = [2]int{rightStart + len(hBtn) + 2, rightStart + len(hBtn) + 2 + len(iBtn)}

	title := " ft "
	chip := ""
	if m.tr.Root.Path == m.scratchDirPath() {
		chip = "scratch "
	}
	root := " " + abbrevHome(m.tr.Root.Path)
	leftW := len(title) + len(chip) + lipgloss.Width(root)
	if leftW > rightStart-1 {
		keep := rightStart - 1 - len(title) - len(chip)
		root = truncateLeft(root, keep)
	}
	gap := max(1, rightStart-len(title)-len(chip)-lipgloss.Width(root))
	return styleTitle.Render(title) + styleMark.Render(chip) + styleDim.Render(root) +
		strings.Repeat(" ", gap) +
		checkboxStyle(m.showHidden).Render(hBtn) + "  " +
		checkboxStyle(m.showIgnored).Render(iBtn) + " "
}

func checkbox(label string, on bool) string {
	if on {
		return "[x] " + label
	}
	return "[ ] " + label
}

func checkboxStyle(on bool) lipgloss.Style {
	if on {
		return styleOK
	}
	return styleDim
}

func (m *Model) renderTree() string {
	h := m.treeHeight()
	lines := make([]string, 0, h)
	for i := m.scroll; i < min(len(m.rows), m.scroll+h); i++ {
		lines = append(lines, m.renderRow(m.rows[i], i == m.cursor))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderRow(r tree.Row, selected bool) string {
	n := r.Node
	bg := func(st lipgloss.Style) lipgloss.Style {
		if selected {
			return st.Background(colSelBg)
		}
		return st
	}
	marked := m.marked[n.Path]
	var b strings.Builder
	if r.Depth > 0 {
		// Marked rows draw a bar over the first indent cell — markable rows
		// always have depth ≥ 1, so nothing shifts.
		if marked {
			b.WriteString(bg(styleMark).Render("▍"))
			b.WriteString(bg(styleBase).Render(strings.Repeat(" ", 2*r.Depth-1)))
		} else {
			b.WriteString(bg(styleBase).Render(strings.Repeat("  ", r.Depth)))
		}
	}
	chev := "  "
	if n.IsDir {
		if n.Expanded {
			chev = "▾ "
		} else {
			chev = "▸ "
		}
	}
	b.WriteString(bg(styleChevron).Render(chev))

	code := m.nodeCode(n)
	if m.cfg.General.Icons == "nerd" {
		ic := icons.For(n.Name, n.IsDir, n.Expanded)
		b.WriteString(bg(styleBase.Foreground(lipgloss.Color(ic.Color))).Render(ic.Glyph + " "))
	}

	nameStyle := styleBase
	if n.IsDir {
		nameStyle = nameStyle.Bold(true)
	}
	if c, ok := codeColors[code]; ok {
		nameStyle = nameStyle.Foreground(c)
	}
	if n.Broken {
		nameStyle = nameStyle.Foreground(colError)
	}
	if marked {
		nameStyle = nameStyle.Foreground(colMark) // mark tint wins over git colour
	}
	b.WriteString(bg(nameStyle).Render(n.Name))

	if n.IsSymlink {
		b.WriteString(bg(styleDim).Render(" → " + n.SymlinkTarget))
	}
	if n.IsDir && code == gitx.None && m.dirHasChanges(n) {
		b.WriteString(bg(styleChanged).Render(" •"))
	}

	line := b.String()
	if selected {
		if pad := m.width - lipgloss.Width(line); pad > 0 {
			line += bg(styleBase).Render(strings.Repeat(" ", pad))
		}
	}
	return styleBase.MaxWidth(m.width).Render(line)
}

func (m *Model) renderStatus() string {
	switch m.mode {
	case modePrompt:
		labels := map[promptKind]string{
			promptNewFile: " New file: ",
			promptNewDir:  " New directory: ",
			promptRename:  " Rename: ",
		}
		return styleTitle.Render(labels[m.prompt]) + m.input.View()
	case modeConfirm:
		return m.renderConfirm()
	}

	var left string
	switch {
	case m.statusMsg != "" && m.statusErr:
		left = styleError.Render(" " + m.statusMsg)
	case m.statusMsg != "":
		left = styleOK.Render(" " + m.statusMsg)
	default:
		if sel := m.selected(); sel != nil {
			left = styleDim.Render(" " + m.gitRelPath(sel))
		}
	}
	right := styleDim.Render(fmt.Sprintf("%d/%d ", m.cursor+1, len(m.rows)))
	if n := len(m.marked); n > 0 {
		right = styleMark.Render(fmt.Sprintf("● %d marked  ", n)) + right
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return styleBase.MaxWidth(m.width).Render(left)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) renderConfirm() string {
	p := m.pending
	if p == nil {
		return ""
	}
	if p.kind == opTrash {
		if len(p.items) == 1 {
			return styleError.Render(" Move " + filepath.Base(p.items[0]) + " to Trash? (y/n)")
		}
		// Marked items may be off-screen: name what will be deleted.
		names := make([]string, 0, 3)
		for i, it := range p.items {
			if i == 3 {
				break
			}
			names = append(names, filepath.Base(it))
		}
		extra := ""
		if len(p.items) > 3 {
			extra = fmt.Sprintf(", +%d more", len(p.items)-3)
		}
		return styleError.Render(fmt.Sprintf(" Move %d marked to Trash (%s%s)? (y/n)",
			len(p.items), strings.Join(names, ", "), extra))
	}
	verb := "Copy"
	if p.kind == opMove {
		verb = "Move"
	}
	tgt := m.tr.Rel(p.targetDir)
	if tgt != "." {
		tgt += "/"
	}
	if p.conflicts == 0 {
		return styleTitle.Render(fmt.Sprintf(" %s %d item(s) into %s? (y/n)", verb, len(p.items), tgt))
	}
	return styleError.Render(fmt.Sprintf(" %s %d into %s — %d exist:", verb, len(p.items), tgt, p.conflicts)) +
		styleTitle.Render(" [o]verwrite (to Trash) · [k]eep both · [n]o")
}

func (m *Model) renderFuzzy() string {
	h := m.treeHeight()
	lines := make([]string, 0, h)
	counter := ""
	if len(m.fuzzyMatches) > 0 {
		counter = styleDim.Render(fmt.Sprintf("  %d/%d", m.fuzzySel+1, len(m.fuzzyMatches)))
	}
	lines = append(lines, styleTitle.Render(" Find: ")+m.input.View()+counter)
	for i := m.fuzzyScroll; i < len(m.fuzzyMatches) && len(lines) < h; i++ {
		mt := m.fuzzyMatches[i]
		matched := make(map[int]bool, len(mt.MatchedIndexes))
		for _, idx := range mt.MatchedIndexes {
			matched[idx] = true
		}
		var b strings.Builder
		for bi, r := range mt.Str {
			if matched[bi] {
				b.WriteString(styleTitle.Render(string(r)))
			} else {
				b.WriteString(string(r))
			}
		}
		prefix := "   "
		line := b.String()
		if i == m.fuzzySel {
			prefix = styleTitle.Render(" > ")
		}
		lines = append(lines, styleBase.MaxWidth(m.width).Render(prefix+line))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderHelp() string {
	type row struct{ key, desc string }
	rows := []row{
		{"↑/k ↓/j", "move selection"},
		{"←/h", "collapse / go to parent"},
		{"→/l", "expand / step in"},
		{"enter", "open file (default command) / toggle dir"},
		{"g G", "top / bottom"},
		{"ctrl+u ctrl+d", "half page up / down"},
		{m.actionKeys["mark"], "mark/unmark selection (and move down)"},
		{m.actionKeys["clear-marks"], "clear marks, else leave scratch view"},
		{m.actionKeys["scratch"], "toggle scratch view"},
		{m.actionKeys["scratch-new"], "new scratch file, opened in editor"},
		{m.actionKeys["copy-here"] + " / " + m.actionKeys["move-here"], "copy / move marked items here"},
		{m.actionKeys["copy-abs"] + " / " + m.actionKeys["copy-rel"], "copy absolute / git-relative path"},
		{m.actionKeys["copy-url"] + " / " + m.actionKeys["open-url"], "copy web URL / open in browser (+copy)"},
		{m.actionKeys["toggle-hidden"], "toggle hidden files"},
		{m.actionKeys["toggle-ignored"], "toggle gitignored files"},
		{m.actionKeys["reload"] + " / F5", "reload from disk"},
		{m.actionKeys["reveal"], "reveal in Finder"},
		{m.actionKeys["fuzzy"], "fuzzy find"},
		{m.actionKeys["new-file"] + " / " + m.actionKeys["new-dir"], "new file / directory"},
		{m.actionKeys["rename"], "rename"},
		{m.actionKeys["delete"], "delete marked (or selection) to Trash"},
		{m.actionKeys["collapse-all"], "collapse all"},
		{m.actionKeys["edit-config"], "edit config (reloads on exit)"},
		{m.actionKeys["help"], "toggle this help"},
		{m.actionKeys["quit"], "quit"},
	}
	var names []string
	for name, c := range m.cfg.Commands {
		if c.Key != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		rows = append(rows, row{m.cfg.Commands[name].Key, "run command: " + name})
	}

	h := m.treeHeight()
	lines := make([]string, 0, h)
	lines = append(lines, styleTitle.Render(" Keys"), "")
	for _, r := range rows {
		if len(lines) >= h {
			break
		}
		lines = append(lines, fmt.Sprintf("  %s%s", styleOK.Render(fmt.Sprintf("%-14s", r.key)), styleDim.Render(r.desc)))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func abbrevHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// truncateLeft keeps the tail of s within w cells, prefixing an ellipsis.
func truncateLeft(s string, w int) string {
	if w <= 1 {
		return "…"
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return "…" + string(r[len(r)-w+1:])
}
