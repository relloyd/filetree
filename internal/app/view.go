package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/icons"
	"github.com/relloyd/filetree/internal/search"
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
	colType    = lipgloss.Color("#D7BA7D") // finder: what the Type filter matched

	styleBase    = lipgloss.NewStyle()
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleError   = lipgloss.NewStyle().Foreground(colError)
	styleOK      = lipgloss.NewStyle().Foreground(colOK)
	styleTitle   = lipgloss.NewStyle().Foreground(colTitle).Bold(true)
	styleChevron = lipgloss.NewStyle().Foreground(colDim)
	styleChanged = lipgloss.NewStyle().Foreground(colChanged)
	styleMark    = lipgloss.NewStyle().Foreground(colMark)
	styleType    = lipgloss.NewStyle().Foreground(colType)

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

// renderHeader draws the title, root path, and the clickable toggle buttons,
// recording their x-ranges for mouse hit-testing.
func (m *Model) renderHeader() string {
	btns := []struct {
		label string
		on    bool
		zone  *[2]int
	}{
		{"hidden", m.showHidden, &m.zoneHidden},
		{"ignored", m.showIgnored, &m.zoneIgnored},
		{"scoped", m.scoped, &m.zoneScoped},
	}
	const btnGap = 2

	rightPlain := " " // the trailing space after the last button
	for i, b := range btns {
		if i > 0 {
			rightPlain += strings.Repeat(" ", btnGap)
		}
		rightPlain += checkbox(b.label, b.on)
	}
	rightStart := max(0, m.width-len(rightPlain))

	title := " ft "
	chip, chipStyle := "", styleMark
	if m.tr.Root.Path == m.scratchDirPath() {
		chip = "scratch "
	}
	if m.inWorktreesDir(m.tr.Root.Path) {
		chip, chipStyle = "worktrees ", styleTitle
	}
	root := " " + abbrevHome(m.tr.Root.Path)
	leftW := len(title) + len(chip) + lipgloss.Width(root)
	if leftW > rightStart-1 {
		keep := rightStart - 1 - len(title) - len(chip)
		root = truncateLeft(root, keep)
	}
	gap := max(1, rightStart-len(title)-len(chip)-lipgloss.Width(root))

	// Place the zones from where the buttons actually start, not from
	// rightStart: on a terminal too narrow to squeeze the root path into, the
	// gap bottoms out at one column and pushes them right of it. Offsets are
	// accumulated per button, so adding a fourth is a line in the slice above.
	right := ""
	x := len(title) + len(chip) + lipgloss.Width(root) + gap
	for i, b := range btns {
		if i > 0 {
			right += strings.Repeat(" ", btnGap)
			x += btnGap
		}
		txt := checkbox(b.label, b.on)
		*b.zone = [2]int{x, x + len(txt)}
		right += checkboxStyle(b.on).Render(txt)
		x += len(txt)
	}
	right += " "
	return styleTitle.Render(title) + chipStyle.Render(chip) + styleDim.Render(root) +
		strings.Repeat(" ", gap) + right
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
			promptNewFile:  " New file: ",
			promptNewDir:   " New directory: ",
			promptRename:   " Rename: ",
			promptWorktree: " Worktree (branch or PR#): ",
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
	case m.mode == modeFuzzy && m.finderCommand() != "":
		// The tree cursor is not what you are looking at in the fuzzy
		// finder, so the status bar shows the ripgrep command instead — the
		// one place a long line fits without adding a row. Truncated from
		// the left so the pattern, the interesting end, always survives.
		left = styleDim.Render(" " + truncateLeft(m.finderCommand(), max(1, m.width-2)))
	default:
		if sel := m.selected(); sel != nil {
			left = styleDim.Render(" " + m.gitRelPath(sel))
		}
	}
	right := styleDim.Render(fmt.Sprintf("%d/%d ", m.cursor+1, len(m.rows)))
	if n := len(m.marked); n > 0 {
		right = styleMark.Render(fmt.Sprintf("● %d marked  ", n)) + right
	}
	if b := m.selectedBranch(); b != "" {
		right = styleTitle.Render("⎇ "+truncate(b, 24)+"  ") + right
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
	if p.kind == opWorktree {
		name := filepath.Base(p.items[0])
		if p.force {
			return styleError.Render(" Worktree "+name+" has changes:") +
				styleTitle.Render(" [f]orce remove · [n]o")
		}
		return styleError.Render(" Remove worktree " + name + "? (y/n)")
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

// renderFinderHeader draws the finder's input lines: the fuzzy query always,
// and the type filter once it is focused or non-empty. It returns exactly
// finderHeaderLines() lines so the match list below it lines up.
func (m *Model) renderFinderHeader() []string {
	label := func(text string, f finderField) string {
		if m.finderField == f {
			return styleTitle.Render(text)
		}
		return styleDim.Render(text)
	}
	lines := []string{m.renderFinderScope()}
	lines = append(lines, label(" Find ", fieldQuery)+m.input.View()+m.renderFinderCounter())
	if m.finderField == fieldType || m.typeInput.Value() != "" {
		line := label(" Type ", fieldType) + m.typeInput.View()
		if m.fuzzyFilterErr != "" {
			line += styleError.Render("  " + m.fuzzyFilterErr)
		}
		lines = append(lines, line)
	}
	if m.finderField == fieldGrep || m.grepInput.Value() != "" {
		line := label(" Grep ", fieldGrep) + m.grepInput.View()
		switch {
		case m.grepErr != "":
			line += styleError.Render("  " + m.grepErr)
		case m.grepRunning:
			line += styleDim.Render("  searching…")
		}
		// A skipped match means the search was not exhaustive, so the note
		// stays up after it finishes rather than clearing with the spinner.
		if n := m.grepSkipped; n > 0 {
			line += styleChanged.Render(fmt.Sprintf("  %d skipped (line too long)", n))
		}
		lines = append(lines, line)
	}
	return lines
}

// renderFinderScope is the "Dir" line: where the finder is searching. It is
// always drawn, so the toggle's state is never in doubt. Scoped, it shows the
// directory in force; unscoped it is dimmed and shows the directory that
// turning the scope on would pick — a preview of the key rather than a stale
// record of the last one.
func (m *Model) renderFinderScope() string {
	dir := m.scopeDir
	if !m.scoped {
		dir = m.selectionDir()
	}
	if dir == "" {
		dir = abbrevHome(m.tr.Root.Path)
	}
	style := styleDim
	if m.scoped {
		style = styleOK
	}
	return styleDim.Render(" Dir  ") + style.Render(truncateLeft(dir, max(1, m.width-6)))
}

// renderFinderCounter is the position indicator: "12/1000", with "…" while the
// walk is still running, "+" when it stopped at the candidate cap, "max" when
// results were dropped at the match cap, and "×N" once the session limit has
// been raised. It brightens at ×2 and above so a raised limit is obvious.
func (m *Model) renderFinderCounter() string {
	busy := m.fuzzyWalking
	if m.grepping() {
		busy = m.grepRunning
	}
	n := m.finderLen()
	if n == 0 {
		if busy {
			return styleDim.Render("  …")
		}
		return ""
	}
	s := fmt.Sprintf("  %d/%d", m.fuzzySel+1, n)
	switch {
	case busy:
		s += "…"
	case m.fuzzyTrunc && !m.grepping():
		s += "+"
	}
	if factor := m.fuzzyFactor(); factor > 1 {
		s += fmt.Sprintf(" ×%d", factor)
		if m.finderCapped() {
			// Still capped even after raising: say so in the same bright
			// style rather than mixing two colours on one counter.
			s += " max"
		}
		return styleOK.Render(s)
	}
	if m.finderCapped() {
		return styleDim.Render(s) + styleChanged.Render(" max")
	}
	return styleDim.Render(s)
}

func (m *Model) renderFuzzy() string {
	h := m.treeHeight()
	lines := make([]string, 0, h)
	lines = append(lines, m.renderFinderHeader()...)
	if m.grepping() {
		for i := m.fuzzyScroll; i < len(m.grepRows) && len(lines) < h; i++ {
			lines = append(lines, m.renderGrepRow(m.grepHits[m.grepRows[i]], i == m.fuzzySel))
		}
		for len(lines) < h {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}
	for i := m.fuzzyScroll; i < len(m.fuzzyMatches) && len(lines) < h; i++ {
		mt := m.fuzzyMatches[i]
		prefix := "   "
		if i == m.fuzzySel {
			prefix = styleTitle.Render(" > ")
		}
		line := m.highlightPath(mt.Str, mt.MatchedIndexes)
		lines = append(lines, styleBase.MaxWidth(m.width).Render(prefix+line))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// highlightPath colours a result path: the runes the Find query matched in
// blue, and the parts the Type filter accounts for in gold. Find wins any
// overlap — it is what the user is actively typing.
//
// Both index sets are byte offsets: sahilm/fuzzy walks its candidate by byte
// (advancing by rune size), search.Span is documented as a byte range, and
// "for bi := range s" yields byte offsets too, so multi-byte names line up.
func (m *Model) highlightPath(s string, matched []int) string {
	find := make(map[int]bool, len(matched))
	for _, idx := range matched {
		find[idx] = true
	}
	typed := make(map[int]bool)
	if !m.fuzzyFilter.Empty() {
		for _, sp := range m.fuzzyFilter.Highlight(s) {
			for bi := sp.Start; bi < sp.End; bi++ {
				typed[bi] = true
			}
		}
	}

	var b strings.Builder
	for bi, r := range s {
		switch {
		case find[bi]:
			b.WriteString(styleTitle.Render(string(r)))
		case typed[bi]:
			b.WriteString(styleType.Render(string(r)))
		default:
			b.WriteString(string(r))
		}
	}
	return b.String()
}

// renderGrepRow draws one content match as "path:line  matched text". The
// location is what enter acts on, so it is never truncated: the matched line
// takes whatever width is left over.
func (m *Model) renderGrepRow(h search.Hit, selected bool) string {
	prefix := "   "
	if selected {
		prefix = styleTitle.Render(" > ")
	}
	at := ":" + strconv.Itoa(h.Line)
	line := prefix + m.highlightPath(h.Path, nil) + styleDim.Render(at)
	if room := m.width - 3 - lipgloss.Width(h.Path) - len(at) - 2; room > 0 {
		if text := strings.TrimSpace(h.Text); text != "" {
			line += "  " + styleDim.Render(truncate(text, room))
		}
	}
	return styleBase.MaxWidth(m.width).Render(line)
}

func (m *Model) renderHelp() string {
	type row struct{ key, desc string }
	reloadKeys := "F5" // reload has no letter key unless [keys] gives it one
	if k := m.actionKeys["reload"]; k != "" {
		reloadKeys = k + " / F5"
	}
	rows := []row{
		{"↑/k ↓/j", "move selection"},
		{"←/h", "collapse / go to parent"},
		{"→/l", "expand / step in"},
		{"enter", "open file (default command) / toggle dir"},
		{"g G", "top / bottom"},
		{"ctrl+u ctrl+d", "half page up / down"},
		{m.actionKeys["mark"], "mark/unmark selection (and move down)"},
		{m.actionKeys["clear-marks"], "clear marks, else leave scratch/worktrees view"},
		{m.actionKeys["scratch"], "toggle scratch view"},
		{m.actionKeys["scratch-new"], "new scratch file, opened in editor"},
		{m.actionKeys["worktrees"], "toggle worktrees view"},
		{m.actionKeys["worktree-new"], "new git worktree (branch name or PR#)"},
		{m.actionKeys["copy-here"] + " / " + m.actionKeys["move-here"], "copy / move marked items here"},
		{m.actionKeys["copy-abs"] + " / " + m.actionKeys["copy-rel"], "copy absolute / git-relative path"},
		{m.actionKeys["copy-url"] + " / " + m.actionKeys["open-url"], "copy web URL / open in browser (+copy)"},
		{m.actionKeys["toggle-hidden"], "toggle hidden files"},
		{m.actionKeys["toggle-ignored"], "toggle gitignored files"},
		{m.actionKeys["toggle-scope"], "confine the fuzzy finder to the selected dir (works inside it too)"},
		{reloadKeys, "reload from disk"},
		{m.actionKeys["reveal"], "reveal in Finder"},
		{m.actionKeys["fuzzy"], "fuzzy finder"},
		{m.actionKeys["finder-next-field"], "in the fuzzy finder: cycle Find / Type / Grep"},
		{m.actionKeys["finder-more"], "in the fuzzy finder: raise the match limit (2×, 3×, …)"},
		{m.actionKeys["finder-copy-command"], "in the fuzzy finder: copy the rg command"},
		{m.actionKeys["finder-clear"], "in the fuzzy finder: empty all three fields"},
		{m.actionKeys["finder-resume"], "reopen the fuzzy finder where you left it"},
		{m.actionKeys["new-file"] + " / " + m.actionKeys["new-dir"], "new file / directory"},
		{m.actionKeys["rename"], "rename"},
		{m.actionKeys["delete"], "delete marked (or selection) to Trash; worktree: git remove"},
		{m.actionKeys["collapse-all"], "collapse all"},
		{m.actionKeys["edit-config"], "edit config (reloads on exit)"},
		{m.actionKeys["help"], "toggle this help"},
		{m.actionKeys["quit"], "quit"},
	}
	var names []string
	for name, c := range m.cfg.Commands {
		if c.Key != "" || c.FinderKey != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		c := m.cfg.Commands[name]
		if c.Key != "" {
			rows = append(rows, row{c.Key, "run command: " + name})
		}
		if c.FinderKey != "" {
			rows = append(rows, row{c.FinderKey, "in the fuzzy finder: run command: " + name})
		}
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

// truncate keeps the head of s within w runes, suffixing an ellipsis.
func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
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
