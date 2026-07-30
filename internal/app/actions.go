package app

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/fsops"
	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/tree"
)

// --- navigation ---

func (m *Model) cursorUp() (tea.Model, tea.Cmd) {
	m.cursor = max(0, m.cursor-1)
	m.ensureVisible()
	return m, nil
}

func (m *Model) cursorDown() (tea.Model, tea.Cmd) {
	m.cursor = min(len(m.rows)-1, m.cursor+1)
	m.ensureVisible()
	return m, nil
}

func (m *Model) gotoTop() (tea.Model, tea.Cmd) {
	m.cursor = 0
	m.ensureVisible()
	return m, nil
}

func (m *Model) gotoBottom() (tea.Model, tea.Cmd) {
	m.cursor = max(0, len(m.rows)-1)
	m.ensureVisible()
	return m, nil
}

func (m *Model) halfPageDown() (tea.Model, tea.Cmd) {
	m.cursor = min(len(m.rows)-1, m.cursor+m.treeHeight()/2)
	m.ensureVisible()
	return m, nil
}

func (m *Model) halfPageUp() (tea.Model, tea.Cmd) {
	m.cursor = max(0, m.cursor-m.treeHeight()/2)
	m.ensureVisible()
	return m, nil
}

// leftKey collapses an expanded dir, otherwise jumps to the parent.
func (m *Model) leftKey() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if n.IsDir && n.Expanded {
		m.tr.Collapse(n)
		m.reflatten()
		m.syncWatches()
		m.saveState()
		return m, nil
	}
	if n.Parent != nil {
		for i, r := range m.rows {
			if r.Node == n.Parent {
				m.cursor = i
				m.ensureVisible()
				break
			}
		}
	}
	return m, nil
}

// rightKey expands a collapsed dir, or steps into the first child.
func (m *Model) rightKey() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil || !n.IsDir {
		return m, nil
	}
	if !n.Expanded {
		return m.expandDir(n)
	}
	if m.cursor+1 < len(m.rows) && m.rows[m.cursor+1].Node.Parent == n {
		m.cursor++
		m.ensureVisible()
	}
	return m, nil
}

// enterKey toggles directories; on files it runs the default command.
func (m *Model) enterKey() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if n.IsDir {
		return m.toggleDir(n)
	}
	return m.runCommand(m.cfg.DefaultCommand)
}

func (m *Model) toggleDir(n *tree.Node) (tea.Model, tea.Cmd) {
	if n.Expanded {
		m.tr.Collapse(n)
		m.reflatten()
		m.syncWatches()
		m.saveState()
		return m, nil
	}
	return m.expandDir(n)
}

func (m *Model) expandDir(n *tree.Node) (tea.Model, tea.Cmd) {
	if err := m.tr.Expand(n); err != nil {
		return m, m.note(err.Error(), true)
	}
	m.reflatten()
	m.syncWatches()
	m.saveState()
	return m, m.ensureStatusCmd(n.Path)
}

func (m *Model) collapseAll() (tea.Model, tea.Cmd) {
	m.tr.CollapseAll()
	m.clearAllMarks()
	m.cursor, m.scroll = 0, 0
	m.reflatten()
	m.syncWatches()
	m.saveState()
	return m, nil
}

// --- marks ---

// toggleMark marks/unmarks the selection and advances the cursor, so
// tapping space marks a run of files.
func (m *Model) toggleMark() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil || n == m.tr.Root {
		return m, nil
	}
	if m.marked[n.Path] {
		m.unmark(n.Path)
	} else {
		m.marked[n.Path] = true
		m.markOrder = append(m.markOrder, n.Path)
	}
	return m.cursorDown()
}

func (m *Model) unmark(path string) {
	delete(m.marked, path)
	for i, p := range m.markOrder {
		if p == path {
			m.markOrder = append(m.markOrder[:i], m.markOrder[i+1:]...)
			break
		}
	}
}

// remapMark keeps a mark pointing at a renamed path, preserving its
// position in the mark order.
func (m *Model) remapMark(old, new string) {
	if !m.marked[old] {
		return
	}
	delete(m.marked, old)
	m.marked[new] = true
	for i, p := range m.markOrder {
		if p == old {
			m.markOrder[i] = new
			break
		}
	}
}

func (m *Model) clearAllMarks() {
	m.marked = map[string]bool{}
	m.markOrder = nil
}

func (m *Model) clearMarks() (tea.Model, tea.Cmd) {
	if len(m.marked) == 0 {
		return m, nil
	}
	n := len(m.marked)
	m.clearAllMarks()
	return m, m.note(fmt.Sprintf("Cleared %d mark(s)", n), false)
}

// --- copy / move marked items ---

// stageTransfer validates the marked set against the current target
// directory and enters the confirmation prompt.
func (m *Model) stageTransfer(kind opKind) (tea.Model, tea.Cmd) {
	if len(m.markOrder) == 0 {
		return m, m.note("Nothing marked — space marks the selection", true)
	}
	target := m.createTargetDir()
	var items []string
	for _, p := range append([]string(nil), m.markOrder...) {
		if _, err := os.Lstat(p); err != nil {
			m.unmark(p) // vanished since it was marked
			continue
		}
		items = append(items, p)
	}
	if len(items) == 0 {
		return m, m.note("Marked items no longer exist", true)
	}
	for _, p := range items {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
			if target == p || strings.HasPrefix(target+"/", p+"/") {
				return m, m.note("Cannot copy/move "+filepath.Base(p)+" into itself", true)
			}
		}
	}
	conflicts := 0
	for _, p := range items {
		if _, err := os.Lstat(filepath.Join(target, filepath.Base(p))); err == nil {
			conflicts++
		}
	}
	m.pending = &pendingOp{kind: kind, items: items, targetDir: target, conflicts: conflicts}
	m.mode = modeConfirm
	return m, nil
}

// handleConfirmKey resolves the staged operation: y/n for trash, worktree
// removal, and conflict-free transfers; o(verwrite)/k(eep both)/n when
// conflicts exist; f/n when git has refused to drop a dirty worktree.
func (m *Model) handleConfirmKey(s string) (tea.Model, tea.Cmd) {
	p := m.pending
	if p == nil {
		m.mode = modeNormal
		return m, nil
	}
	cancel := func() (tea.Model, tea.Cmd) {
		m.mode, m.pending = modeNormal, nil
		return m, nil
	}
	proceed := func(policy fsops.Policy) (tea.Model, tea.Cmd) {
		m.mode, m.pending = modeNormal, nil
		return m, m.transferCmd(p, policy)
	}
	switch p.kind {
	case opTrash:
		switch s {
		case "y", "enter":
			m.mode, m.pending = modeNormal, nil
			return m, m.trashCmd(p.items)
		case "n", "esc", "q":
			return cancel()
		}
	case opWorktree:
		// The force variant only accepts "f", so a stray y/enter can't
		// re-run the removal git has already refused.
		if p.force {
			switch s {
			case "f":
				m.mode, m.pending = modeNormal, nil
				return m, m.worktreeRemoveCmd(p.repoRoot, p.items[0], true)
			case "n", "esc", "q":
				return cancel()
			}
		} else {
			switch s {
			case "y", "enter":
				m.mode, m.pending = modeNormal, nil
				return m, m.worktreeRemoveCmd(p.repoRoot, p.items[0], false)
			case "n", "esc", "q":
				return cancel()
			}
		}
	case opCopy, opMove:
		if p.conflicts == 0 {
			switch s {
			case "y", "enter":
				return proceed(fsops.PolicyNone)
			case "n", "esc", "q":
				return cancel()
			}
		} else {
			switch s {
			case "o":
				return proceed(fsops.PolicyOverwrite)
			case "k":
				return proceed(fsops.PolicyKeepBoth)
			case "n", "esc", "q":
				return cancel()
			}
		}
	}
	return m, nil
}

func (m *Model) transferCmd(p *pendingOp, policy fsops.Policy) tea.Cmd {
	op := fsops.OpCopy
	if p.kind == opMove {
		op = fsops.OpMove
	}
	trash := m.plat.Trash
	return func() tea.Msg {
		res := fsops.Transfer(op, p.items, p.targetDir, policy, trash)
		return transferDoneMsg{kind: p.kind, items: p.items, target: p.targetDir, res: res}
	}
}

func (m *Model) handleTransferDone(msg transferDoneMsg) (tea.Model, tea.Cmd) {
	dirty := map[string]bool{msg.target: true}
	if msg.kind == opMove {
		for _, it := range msg.items {
			dirty[filepath.Dir(it)] = true
		}
	}
	var dirs []string
	for d := range dirty {
		dirs = append(dirs, d)
		if n := m.tr.FindByPath(d); n != nil && n.Loaded {
			_ = m.tr.Refresh(n)
		}
	}
	m.clearAllMarks()
	m.reflatten()
	m.syncWatches()
	m.saveState()
	op := fsops.OpCopy
	if msg.kind == opMove {
		op = fsops.OpMove
	}
	cmds := m.invalidateStatusCmds(dirs)
	cmds = append(cmds, m.note(msg.res.Summary(op), len(msg.res.Errors) > 0))
	return m, tea.Batch(cmds...)
}

// --- toggles ---

func (m *Model) toggleHidden() (tea.Model, tea.Cmd) {
	m.showHidden = !m.showHidden
	m.reflatten()
	m.syncWatches()
	m.saveState()
	return m, nil
}

func (m *Model) toggleIgnored() (tea.Model, tea.Cmd) {
	m.showIgnored = !m.showIgnored
	m.reflatten()
	m.syncWatches()
	m.saveState()
	return m, nil
}

// selectionDir is the root-relative directory the finder would scope to: the
// selection itself when it is a directory, otherwise its parent. "" is the
// root, for which scoping is a no-op.
func (m *Model) selectionDir() string {
	n := m.selected()
	if n == nil {
		return ""
	}
	p := n.Path
	if !n.IsDir {
		p = filepath.Dir(p)
	}
	if rel := m.tr.Rel(p); rel != "." {
		return rel
	}
	return ""
}

// scopeAbs is scopeDir as an absolute path — the tree root when unscoped.
func (m *Model) scopeAbs() string {
	if m.scopeDir != "" {
		return filepath.Join(m.tr.Root.Path, filepath.FromSlash(m.scopeDir))
	}
	return m.tr.Root.Path
}

// --- clipboard / reveal ---

func (m *Model) copyAbs() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if err := m.plat.CopyToClipboard(n.Path); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, m.note("Copied: "+n.Path, false)
}

func (m *Model) copyRel() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	p := m.gitRelPath(n)
	if err := m.plat.CopyToClipboard(p); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, m.note("Copied: "+p, false)
}

// gitRelPath is the node's path relative to its closest parent git repo,
// falling back to the absolute path outside any repo.
func (m *Model) gitRelPath(n *tree.Node) string { return m.gitRelPathFor(n.Path) }

// gitRelPathFor is gitRelPath by path, for callers holding one without a tree
// node — a finder hit need not be in a loaded directory.
func (m *Model) gitRelPathFor(abs string) string {
	if root := m.repoRootFor(filepath.Dir(abs)); root != "" {
		return gitx.RelPath(root, abs)
	}
	return abs
}

func (m *Model) reveal() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if err := m.plat.Reveal(n.Path); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, nil
}

// --- reload ---

// reloadAll re-reads every loaded directory from disk and refreshes git
// status for every known repo.
func (m *Model) reloadAll() (tea.Model, tea.Cmd) {
	m.refreshLoaded(m.tr.Root)
	m.reflatten()
	m.syncWatches()
	m.saveState()
	cmds := m.refreshAllStatusCmds()
	cmds = append(cmds, m.note("Reloaded from disk", false))
	return m, tea.Batch(cmds...)
}

// refreshLoaded re-lists n and recurses into children that remain loaded.
func (m *Model) refreshLoaded(n *tree.Node) {
	if !n.IsDir || !n.Loaded {
		return
	}
	if err := m.tr.Refresh(n); err != nil {
		return // dir vanished; the parent's refresh has pruned it
	}
	for _, c := range n.Children {
		m.refreshLoaded(c)
	}
}

func (m *Model) handleFsBatch(dirs fsBatchMsg) (tea.Model, tea.Cmd) {
	rootGone := false
	for _, d := range dirs {
		n := m.tr.FindByPath(d)
		if n == nil || !n.IsDir || !n.Loaded {
			continue
		}
		if err := m.tr.Refresh(n); err != nil && n == m.tr.Root {
			rootGone = true
		}
	}
	m.reflatten()
	m.syncWatches()
	m.saveState()
	cmds := m.invalidateStatusCmds(dirs)
	cmds = append(cmds, waitFs(m.watcher))
	if rootGone {
		cmds = append(cmds, m.note("Root directory is gone", true))
	}
	return m, tea.Batch(cmds...)
}

// --- user commands ---

func (m *Model) runCommand(name string) (tea.Model, tea.Cmd) {
	c, ok := m.cfg.Commands[name]
	if !ok {
		return m, m.note(fmt.Sprintf("unknown command %q", name), true)
	}
	n := m.selected()
	if n == nil {
		return m, nil
	}
	dir := n.Path
	if !n.IsDir {
		dir = filepath.Dir(n.Path)
	}
	// {path} and friends stay on the cursor whatever is marked; {paths} is
	// where the marks come in, so a command opts into acting on a set.
	paths := m.commandTargets(n)
	if len(paths) == 0 {
		return m, m.note("Marked items no longer exist", true)
	}
	return m.execCommand(name, c, config.Vars{
		Path:    n.Path,
		RelPath: m.gitRelPath(n),
		Dir:     dir,
		Root:    m.tr.Root.Path,
		Name:    n.Name,
		Paths:   paths,
		Marked:  append([]string(nil), m.markOrder...),
	}, false)
}

// commandTargets is what a command should act on: the marked paths in mark
// order, or the selection when nothing is marked.
//
// Marks whose file has vanished are dropped and unmarked, the same courtesy
// confirmDelete and stageTransfer extend, so a stale mark cannot be handed to
// an editor as an empty buffer. Nothing needs redrawing after one goes: the
// mark tint is read from m.marked at render time.
func (m *Model) commandTargets(sel *tree.Node) []string {
	if len(m.markOrder) == 0 {
		return []string{sel.Path}
	}
	var paths []string
	for _, p := range append([]string(nil), m.markOrder...) {
		if _, err := os.Lstat(p); err != nil {
			m.unmark(p)
			continue
		}
		paths = append(paths, p)
	}
	return paths
}

// runFinderCommand runs a configured command against the highlighted finder
// row, without leaving the finder. An interactive command owns the terminal
// via tea.ExecProcess, which blocks the event loop rather than running beside
// it, so the finder is simply frozen and repainted intact when the child
// exits — there is nothing to tear down and nothing to restore.
func (m *Model) runFinderCommand(name string) (tea.Model, tea.Cmd) {
	c, ok := m.cfg.Commands[name]
	if !ok {
		return m, m.note(fmt.Sprintf("unknown command %q", name), true)
	}
	rel := m.finderPath(m.fuzzySel)
	if rel == "" {
		return m, nil // an empty result list is not an error
	}
	abs := filepath.Join(m.tr.Root.Path, filepath.FromSlash(rel))
	pick := finderPick{rel: rel}
	if m.grepping() && m.fuzzySel < len(m.grepRows) {
		pick.line = m.grepHits[m.grepRows[m.fuzzySel]].Line
	}
	// Not needed to come back here — we never left — but it keeps the
	// finder-resume key honest if the finder is escaped later on.
	m.lastPick = pick
	// Paths is the row alone. Marks belong to the tree, and the finder acts on
	// what is under its own cursor: letting them in would make ctrl+e open
	// something other than the row you are looking at.
	return m.execCommand(name, c, config.Vars{
		Path:    abs,
		RelPath: m.gitRelPathFor(abs),
		Dir:     filepath.Dir(abs),
		Root:    m.tr.Root.Path,
		Name:    path.Base(rel),
		Line:    pick.line,
		Paths:   []string{abs},
		Marked:  append([]string(nil), m.markOrder...),
	}, false)
}

// editConfig runs the default editor command against ~/.filetree/config.toml
// and reloads the config (commands, keys, general settings) when it returns.
func (m *Model) editConfig() (tea.Model, tea.Cmd) {
	c, ok := m.cfg.Commands[m.cfg.DefaultCommand]
	if !ok {
		return m, m.note("no default command configured", true)
	}
	return m.execCommand("edit-config", c, config.Vars{
		Path:    m.cfgPath,
		RelPath: m.cfgPath, // the config lives outside any tree root
		Dir:     filepath.Dir(m.cfgPath),
		Root:    m.tr.Root.Path,
		Name:    filepath.Base(m.cfgPath),
	}, true)
}

func (m *Model) execCommand(name string, c config.Command, v config.Vars, reloadCfg bool) (tea.Model, tea.Cmd) {
	m.recordRecent(c.Run, v)
	if m.clearMarksAfter(c.Run, v) {
		m.clearAllMarks()
	}
	line := config.ExpandCommand(c.Run, v)
	// The template is the user's own config (shell syntax is the point, e.g.
	// tmux invocations); substituted values are shell-quoted by ExpandCommand.
	ec := exec.Command("/bin/sh", "-c", line)
	ec.Dir = m.tr.Root.Path
	if c.Mode == config.ModeInteractive {
		m.saveState()
		return m, tea.ExecProcess(ec, func(err error) tea.Msg {
			return cmdDoneMsg{name: name, interactive: true, reloadConfig: reloadCfg, err: err}
		})
	}
	return m, func() tea.Msg {
		out, err := ec.CombinedOutput()
		return cmdDoneMsg{name: name, out: string(out), reloadConfig: reloadCfg, err: err}
	}
}

// recentJumpAndOpen is enter in the history view: reveal the file in the tree,
// then open it in the default command.
//
// The open names the path outright rather than going through runCommand, which
// acts on m.selected(). fuzzyJump moves the cursor only when the file has a row
// to move to, and a hidden or gitignored file is filtered out of m.rows — so
// runCommand would quietly open whatever the cursor was already on. Revealing
// is best effort; opening the row you chose is not.
func (m *Model) recentJumpAndOpen() (tea.Model, tea.Cmd) {
	rel := m.finderPath(m.fuzzySel)
	if rel == "" {
		return m, nil // an empty history is not an error
	}
	abs := filepath.Join(m.tr.Root.Path, filepath.FromSlash(rel))
	model, jump := m.fuzzyJump()
	c, ok := m.cfg.Commands[m.cfg.DefaultCommand]
	if !ok {
		return model, tea.Batch(jump, m.note("no default command configured", true))
	}
	_, open := m.execCommand(m.cfg.DefaultCommand, c, config.Vars{
		Path:    abs,
		RelPath: m.gitRelPathFor(abs),
		Dir:     filepath.Dir(abs),
		Root:    m.tr.Root.Path,
		Name:    path.Base(rel),
		Marked:  append([]string(nil), m.markOrder...),
	}, false)
	return model, tea.Batch(jump, open)
}

// clearMarksAfter reports whether running this command should consume the
// marked set, under clear_marks_after_command.
//
// Two tests, because naming the marks is not the same as acting on them. The
// template has to reference them — otherwise "n" (a shell in {dir}) or "tab"
// (no placeholders at all) would drop a set they never looked at — and the
// command's first target has to *be* a mark. That second test is what keeps
// "S", which opens a brand new scratch file through the default command's
// {paths}, from clearing marks it had nothing to do with.
func (m *Model) clearMarksAfter(tmpl string, v config.Vars) bool {
	if m.cfg == nil || !m.cfg.General.ClearMarksAfterCommand {
		return false
	}
	return config.UsesMarks(tmpl) && len(v.Paths) > 0 && m.marked[v.Paths[0]]
}

// recordRecent adds the files a command opened to this root's history. It sits
// in execCommand rather than at each call site so every path into a command is
// covered, including scratchNew's direct one, and so a command added to the
// config later needs no wiring.
//
// Three things have to hold, and each excludes a real case:
//
//   - the template names the file. A command that never substitutes it did not
//     open it: this is what keeps "n" (a shell in {dir}), "r" (an rg primed at
//     {dir}), "tab" (focus the pane to the right, no placeholders at all) and
//     "D" (a diff of {marked1}/{marked2}) out of the history;
//   - the path is inside the tree. History is per root and stored
//     root-relative, so there is nowhere to put anything else — this is what
//     excludes "C", whose file lives under ~/.filetree;
//   - it is a regular file. "e" on a directory opens helix's file picker, which
//     is not a file you can come back to.
//
// Opening a set through {paths} records the lot, oldest mark first so the
// newest ends up newest here too. Best effort, like saveState: a history that
// failed to write is not worth interrupting anyone over.
func (m *Model) recordRecent(tmpl string, v config.Vars) {
	if m.recent == nil {
		return
	}
	named := strings.Contains(tmpl, "{path}") || strings.Contains(tmpl, "{relpath}")
	opened := v.Paths
	if strings.Contains(tmpl, "{paths}") {
		if len(opened) == 0 && v.Path != "" {
			opened = []string{v.Path} // the same fallback ExpandCommand applies
		}
	} else if named {
		opened = []string{v.Path}
	} else {
		return
	}

	var added bool
	for _, abs := range opened {
		if abs == "" {
			continue
		}
		rel := m.tr.Rel(abs)
		if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			continue
		}
		if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		m.recent.Add(rel, time.Now(), m.recentMax())
		added = true
	}
	if added {
		_ = m.recent.Save(m.stateDir, m.recentMax())
	}
}

// recentMax is the configured history cap, falling back to the default for
// models built without a config (tests).
func (m *Model) recentMax() int {
	if m.cfg == nil || m.cfg.General.RecentMax < 1 {
		return config.DefaultRecentMax
	}
	return m.cfg.General.RecentMax
}

func (m *Model) handleCmdDone(msg cmdDoneMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg.interactive {
		// An editor session may have changed anything: re-read the world.
		m.refreshLoaded(m.tr.Root)
		m.reflatten()
		m.syncWatches()
		cmds = append(cmds, m.refreshAllStatusCmds()...)
	}
	if msg.reloadConfig && msg.err == nil {
		if cfg, err := config.Load(m.cfgPath); err != nil {
			cmds = append(cmds, m.note("config: "+err.Error(), true))
		} else {
			m.cfg = cfg
			m.buildBindings()
			cmds = append(cmds, m.note("Config reloaded", false))
		}
	}
	if msg.err != nil {
		detail := strings.TrimSpace(msg.out)
		if len(detail) > 120 {
			detail = detail[:120] + "…"
		}
		text := fmt.Sprintf("%s: %v", msg.name, msg.err)
		if detail != "" {
			text += " — " + detail
		}
		cmds = append(cmds, m.note(text, true))
	}
	return m, tea.Batch(cmds...)
}

// --- file operations ---

func (m *Model) startPrompt(kind promptKind) (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if kind == promptRename && n == m.tr.Root {
		return m, m.note("Cannot rename the root", true)
	}
	m.prompt = kind
	m.mode = modePrompt
	m.input.Reset()
	if kind == promptRename {
		m.input.SetValue(n.Name)
		m.input.CursorEnd()
	}
	return m, m.input.Focus()
}

// createTargetDir is where new files/dirs land: the selected dir, or the
// selected file's parent.
func (m *Model) createTargetDir() string {
	n := m.selected()
	if n == nil {
		return m.tr.Root.Path
	}
	if n.IsDir {
		return n.Path
	}
	return filepath.Dir(n.Path)
}

func (m *Model) commitPrompt() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.input.Value())
	m.mode = modeNormal
	// Branch names legitimately contain slashes, so worktrees are resolved
	// before the file-name validation below.
	if m.prompt == promptWorktree {
		return m.createWorktree(name)
	}
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return m, m.note("Invalid name", true)
	}
	var target string
	switch m.prompt {
	case promptNewFile, promptNewDir:
		dir := m.createTargetDir()
		target = filepath.Join(dir, name)
		var err error
		if m.prompt == promptNewDir {
			err = os.Mkdir(target, 0o755)
		} else {
			var f *os.File
			f, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				err = f.Close()
			}
		}
		if err != nil {
			return m, m.note(err.Error(), true)
		}
	case promptRename:
		n := m.selected()
		if n == nil {
			return m, nil
		}
		target = filepath.Join(filepath.Dir(n.Path), name)
		if err := os.Rename(n.Path, target); err != nil {
			return m, m.note(err.Error(), true)
		}
		m.remapMark(n.Path, target)
	}
	return m, m.afterFsMutation(target)
}

// selectPath puts the cursor on the row for an absolute path, when that path
// is currently visible.
func (m *Model) selectPath(path string) {
	n := m.tr.FindByPath(path)
	if n == nil {
		return
	}
	for i, r := range m.rows {
		if r.Node == n {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

// afterFsMutation refreshes the parent of a changed path, reselects it, and
// kicks a git status refresh.
func (m *Model) afterFsMutation(target string) tea.Cmd {
	parent := filepath.Dir(target)
	if n := m.tr.FindByPath(parent); n != nil && n.Loaded {
		_ = m.tr.Refresh(n)
		_ = m.tr.Expand(n)
	}
	m.reflatten()
	if target != "" {
		m.selectPath(target)
	}
	m.syncWatches()
	m.saveState()
	return tea.Batch(m.invalidateStatusCmds([]string{parent})...)
}

// confirmDelete stages the marked items for trashing, or the selection
// when nothing is marked.
func (m *Model) confirmDelete() (tea.Model, tea.Cmd) {
	if len(m.markOrder) > 0 {
		var items []string
		for _, p := range append([]string(nil), m.markOrder...) {
			if _, err := os.Lstat(p); err != nil {
				m.unmark(p) // vanished since it was marked
				continue
			}
			items = append(items, p)
		}
		items = dedupeAncestors(items)
		if len(items) == 0 {
			return m, m.note("Marked items no longer exist", true)
		}
		for _, p := range items {
			// A worktree must be handed to git, one at a time.
			if gitx.IsLinkedWorktree(p) {
				return m, m.note("Unmark worktrees — remove them individually with "+m.actionKeys["delete"], true)
			}
		}
		m.pending = &pendingOp{kind: opTrash, items: items}
		m.mode = modeConfirm
		return m, nil
	}
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if n == m.tr.Root {
		return m, m.note("Cannot delete the root", true)
	}
	if n.IsDir && gitx.IsLinkedWorktree(n.Path) {
		return m.confirmWorktreeRemove(n.Path)
	}
	m.pending = &pendingOp{kind: opTrash, items: []string{n.Path}}
	m.mode = modeConfirm
	return m, nil
}

// dedupeAncestors drops entries covered by an ancestor also in the list, so
// trashing a marked directory doesn't then fail on its separately marked
// children. Order is preserved.
func dedupeAncestors(paths []string) []string {
	var out []string
	for _, p := range paths {
		covered := false
		for _, a := range paths {
			if a != p && strings.HasPrefix(p+"/", a+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) trashCmd(paths []string) tea.Cmd {
	trash := m.plat.Trash
	return func() tea.Msg {
		var msg trashDoneMsg
		for _, p := range paths {
			if _, err := os.Lstat(p); err != nil {
				msg.skipped++ // vanished between confirm and execution
				continue
			}
			if err := trash(p); err != nil {
				msg.errs = append(msg.errs, err.Error())
				continue
			}
			msg.done = append(msg.done, p)
		}
		return msg
	}
}

func (m *Model) handleTrashDone(msg trashDoneMsg) (tea.Model, tea.Cmd) {
	dirty := map[string]bool{}
	for _, p := range msg.done {
		m.unmark(p)
		dirty[filepath.Dir(p)] = true
	}
	var dirs []string
	for d := range dirty {
		dirs = append(dirs, d)
		if n := m.tr.FindByPath(d); n != nil && n.Loaded {
			_ = m.tr.Refresh(n)
		}
	}
	m.reflatten()
	m.syncWatches()
	m.saveState()

	text := fmt.Sprintf("Moved %d to Trash", len(msg.done))
	if len(msg.done) == 1 {
		text = "Moved to Trash: " + filepath.Base(msg.done[0])
	}
	if msg.skipped > 0 {
		text += fmt.Sprintf(", skipped %d", msg.skipped)
	}
	isErr := len(msg.errs) > 0
	if isErr {
		text += " — " + strings.Join(msg.errs, "; ")
	}
	cmds := m.invalidateStatusCmds(dirs)
	cmds = append(cmds, m.note(text, isErr))
	return m, tea.Batch(cmds...)
}

// --- web links ---

// linkAction copies the selection's web URL (GitHub-style, built from the
// origin remote) to the clipboard; openInBrowser additionally opens it.
func (m *Model) linkAction(openInBrowser bool) (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	dir := n.Path
	if !n.IsDir {
		dir = filepath.Dir(n.Path)
	}
	root := m.repoRootFor(dir)
	if root == "" {
		return m, m.note("Not inside a git repository", true)
	}
	rel := gitx.RelPath(root, n.Path)
	untracked := m.nodeCode(n) == gitx.Untracked
	useBranch := m.cfg.General.LinkRef == "branch"
	isDir := n.IsDir
	plat := m.plat
	return m, func() tea.Msg {
		remote, err := gitx.RemoteURL(root)
		if err != nil {
			return linkDoneMsg{err: err}
		}
		base, ok := gitx.WebURL(remote)
		if !ok {
			return linkDoneMsg{err: fmt.Errorf("unsupported remote URL %q", remote)}
		}
		ref, err := gitx.HeadRef(root, useBranch)
		if err != nil {
			return linkDoneMsg{err: err}
		}
		u := gitx.FileURL(base, ref, rel, isDir)
		if err := plat.CopyToClipboard(u); err != nil {
			return linkDoneMsg{err: err}
		}
		if openInBrowser {
			if err := plat.OpenURL(u); err != nil {
				return linkDoneMsg{err: err}
			}
		}
		return linkDoneMsg{url: u, opened: openInBrowser, untracked: untracked}
	}
}

// --- scratch view ---

// scratchDirPath resolves the configured scratch directory without touching
// the filesystem (used by rendering).
func (m *Model) scratchDirPath() string {
	return filepath.Clean(config.ExpandHome(m.cfg.Scratch.Dir))
}

func (m *Model) scratchDir() (string, error) {
	dir := m.scratchDirPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// switchRoot re-roots the explorer, persisting the current view first. On
// load failure the current view stays intact.
func (m *Model) switchRoot(root string) (tea.Model, tea.Cmd) {
	m.saveState()
	if err := m.loadRoot(root); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, tea.Batch(m.ensureStatusesForExpanded()...)
}

// enterView switches to one of the view directories, remembering the project
// root to come back to.
//
// It is remembered *once*, and a second view does not overwrite it. Scratch and
// worktrees are siblings — detours from your tree rather than a hierarchy — so
// there is no stack to unwind, and Esc from either goes back to the project.
// Overwriting instead used to lose the project root entirely on "s" then "w",
// leaving the two views bouncing off each other with no way home.
func (m *Model) enterView(dir string) (tea.Model, tea.Cmd) {
	prev := m.homeRoot
	if prev == "" {
		m.homeRoot = m.tr.Root.Path
	}
	_, cmd := m.switchRoot(dir)
	if m.tr.Root.Path != dir {
		// The load failed and we are still where we were; keep switchRoot's
		// error note. Restoring the old value matters because otherwise
		// homeRoot names the root we are standing in, and the next Esc becomes
		// a switch to nowhere.
		m.homeRoot = prev
	}
	return m, cmd
}

// leaveView returns to the project root a view was entered from.
func (m *Model) leaveView() (tea.Model, tea.Cmd) {
	home := m.homeRoot
	m.homeRoot = ""
	return m.switchRoot(home)
}

// toggleScratch switches to the scratch tree, or back to where you were.
func (m *Model) toggleScratch() (tea.Model, tea.Cmd) {
	sdir, err := m.scratchDir()
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	if m.tr.Root.Path == sdir {
		if m.homeRoot == "" {
			return m, m.note("Already in the scratch directory", false)
		}
		return m.leaveView()
	}
	return m.enterView(sdir)
}

// escKey layers Esc: clear marks first; otherwise leave the scratch or
// worktrees view.
func (m *Model) escKey() (tea.Model, tea.Cmd) {
	if len(m.marked) > 0 {
		return m.clearMarks()
	}
	if m.homeRoot == "" {
		// Not an error — Esc with nothing to leave is not a mistake. But saying
		// so distinguishes "nothing to do" from "the key is broken", which is
		// exactly the confusion a silent no-op here used to cause.
		return m, m.note("Already at the project root", false)
	}
	return m.leaveView()
}

// scratchNew touches an empty timestamped file in the scratch dir, reveals
// it in the scratch view, and opens it with the default command — so a
// quick note is just: n, type, :wq.
func (m *Model) scratchNew() (tea.Model, tea.Cmd) {
	sdir, err := m.scratchDir()
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	path := filepath.Join(sdir, scratchName(time.Now(), m.cfg.Scratch.Extension))
	if _, err := os.Lstat(path); err == nil {
		if path, err = fsops.KeepBothName(path, false); err != nil {
			return m, m.note(err.Error(), true)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	f.Close()

	var cmds []tea.Cmd
	if m.tr.Root.Path != sdir {
		_, cmd := m.enterView(sdir)
		cmds = append(cmds, cmd)
	} else {
		_ = m.tr.Refresh(m.tr.Root)
		m.reflatten()
	}
	m.selectPath(path)
	m.saveState()

	c, ok := m.cfg.Commands[m.cfg.DefaultCommand]
	if !ok {
		return m, tea.Batch(append(cmds, m.note("no default command configured", true))...)
	}
	_, ecmd := m.execCommand(m.cfg.DefaultCommand, c, config.Vars{
		Path:    path,
		RelPath: path,
		Dir:     sdir,
		Root:    m.tr.Root.Path,
		Name:    filepath.Base(path),
		Marked:  append([]string(nil), m.markOrder...),
	}, false)
	return m, tea.Batch(append(cmds, ecmd)...)
}

// scratchName builds the timestamped scratch file name: a YYYYMMDDHH prefix
// so files sort chronologically, plus the configured extension.
func scratchName(now time.Time, ext string) string {
	name := now.Format("2006010215")
	if ext != "" {
		name += "." + ext
	}
	return name
}

// --- worktrees ---

// worktreesDirPath resolves the configured worktrees directory without
// touching the filesystem (used by rendering).
func (m *Model) worktreesDirPath() string {
	return filepath.Clean(config.ExpandHome(m.cfg.Worktrees.Dir))
}

func (m *Model) worktreesDir() (string, error) {
	dir := m.worktreesDirPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// inWorktreesDir reports whether p is the worktrees directory or lives
// inside it — true for the view itself and for any worktree created in it.
func (m *Model) inWorktreesDir(p string) bool {
	dir := m.worktreesDirPath()
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// toggleWorktrees switches to the worktrees tree, or back to where you were.
func (m *Model) toggleWorktrees() (tea.Model, tea.Cmd) {
	wdir, err := m.worktreesDir()
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	if m.tr.Root.Path == wdir {
		if m.homeRoot == "" {
			return m, m.note("Already in the worktrees directory", false)
		}
		return m.leaveView()
	}
	return m.enterView(wdir)
}

// worktreeNew asks for a branch name or PR number; commitPrompt creates the
// worktree for the repo containing the selection.
func (m *Model) worktreeNew() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	dir := n.Path
	if !n.IsDir {
		dir = filepath.Dir(n.Path)
	}
	root := m.repoRootFor(dir)
	if root == "" {
		return m, m.note("Not inside a git repository", true)
	}
	// Started from inside a worktree: group the new one with its siblings
	// under the main repo, not under the worktree we happen to be in.
	if gitx.IsLinkedWorktree(root) {
		if main, err := gitx.MainRepoOf(root); err == nil {
			root = main
		}
	}
	m.worktreeRepo = root
	return m.startPrompt(promptWorktree)
}

// createWorktree resolves the prompt input to a destination under the
// worktrees directory and starts the (possibly slow) git work.
func (m *Model) createWorktree(input string) (tea.Model, tea.Cmd) {
	repo := m.worktreeRepo
	m.worktreeRepo = ""
	if repo == "" {
		return m, m.note("Not inside a git repository", true)
	}
	name := gitx.WorktreeDirName(input)
	if name == "" {
		return m, m.note("Invalid branch name", true)
	}
	wdir, err := m.worktreesDir()
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	dest := filepath.Join(wdir, filepath.Base(repo), name)
	if gitx.IsLinkedWorktree(dest) {
		return m.switchToWorktree(dest, "Already exists — switched to "+abbrevHome(dest))
	}
	if _, err := os.Lstat(dest); err == nil {
		return m, m.note("Already exists and is not a worktree: "+dest, true)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return m, m.note(err.Error(), true)
	}
	pr, branch := gitx.ParseWorktreeInput(input)
	return m, tea.Batch(m.note("Creating worktree…", false), worktreeAddCmd(repo, dest, pr, branch))
}

// worktreeAddCmd does the git work off the update loop: fetching a PR ref or
// an unknown branch can take seconds.
func worktreeAddCmd(repo, dest string, pr int, branch string) tea.Cmd {
	return func() tea.Msg {
		if pr > 0 {
			ref, err := gitx.FetchPR(repo, pr)
			if err != nil {
				return worktreeDoneMsg{err: err}
			}
			if err := gitx.WorktreeAdd(repo, dest, ref, false); err != nil {
				return worktreeDoneMsg{err: err}
			}
			return worktreeDoneMsg{dest: dest, note: worktreeNote(dest, ref, false)}
		}
		if gitx.HasRef(repo, branch) {
			// Known locally: a failure here is real (already checked out).
			if err := gitx.WorktreeAdd(repo, dest, branch, false); err != nil {
				return worktreeDoneMsg{err: err}
			}
			return worktreeDoneMsg{dest: dest, note: worktreeNote(dest, branch, false)}
		}
		// Unknown locally: git DWIMs a tracking branch when exactly one
		// remote has it, so try a plain add before fetching.
		addErr := gitx.WorktreeAdd(repo, dest, branch, false)
		if addErr == nil {
			return worktreeDoneMsg{dest: dest, note: worktreeNote(dest, branch, false)}
		}
		if err := gitx.FetchBranch(repo, branch); err == nil {
			if err := gitx.WorktreeAdd(repo, dest, branch, false); err == nil {
				return worktreeDoneMsg{dest: dest, note: worktreeNote(dest, branch, false)}
			}
		}
		if err := gitx.WorktreeAdd(repo, dest, branch, true); err != nil {
			return worktreeDoneMsg{err: addErr} // the first failure is the informative one
		}
		return worktreeDoneMsg{dest: dest, note: worktreeNote(dest, branch, true)}
	}
}

func worktreeNote(dest, ref string, newBranch bool) string {
	kind := "branch"
	if newBranch {
		kind = "new branch"
	}
	return fmt.Sprintf("Worktree ready: %s (%s %s)", abbrevHome(dest), kind, ref)
}

// switchToWorktree lands in the worktrees view with dest revealed and
// selected — the same place "w" takes you — rather than re-rooting into the
// worktree itself. The original root is remembered once, so Esc still
// returns to the project even after hopping between worktrees.
func (m *Model) switchToWorktree(dest, text string) (tea.Model, tea.Cmd) {
	wdir, err := m.worktreesDir()
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	var cmds []tea.Cmd
	if m.tr.Root.Path != wdir {
		_, cmd := m.enterView(wdir)
		if m.tr.Root.Path != wdir {
			return m, cmd // the load failed; enterView kept the error note
		}
		cmds = append(cmds, cmd)
	} else {
		// Already here: the new directory may predate the watcher's batch.
		_ = m.tr.Refresh(m.tr.Root)
	}
	cmds = append(cmds, m.revealPath(dest)...)
	cmds = append(cmds, m.note(text, false))
	return m, tea.Batch(cmds...)
}

// revealPath expands the tree down to an absolute path and selects it,
// leaving the path itself collapsed. Directories on the way are refreshed so
// a just-created entry is picked up before the watcher's batch arrives.
func (m *Model) revealPath(path string) []tea.Cmd {
	parent := filepath.Dir(path)
	if n := m.tr.FindByPath(parent); n != nil && n.Loaded {
		_ = m.tr.Refresh(n)
	}
	m.tr.ExpandRel(m.tr.Rel(parent))
	m.reflatten()
	m.selectPath(path)
	m.syncWatches()
	m.saveState()
	cmds := m.ensureStatusesForExpanded()
	// The revealed directory stays collapsed, so nothing else would read its
	// status — and the status bar's branch segment comes from that read.
	if c := m.ensureStatusCmd(path); c != nil {
		cmds = append(cmds, c)
	}
	return cmds
}

// confirmWorktreeRemove stages a `git worktree remove` for a worktree root,
// which must not be trashed like an ordinary directory.
func (m *Model) confirmWorktreeRemove(dest string) (tea.Model, tea.Cmd) {
	repo, err := gitx.MainRepoOf(dest)
	if err != nil {
		return m, m.note(err.Error(), true)
	}
	m.pending = &pendingOp{kind: opWorktree, items: []string{dest}, repoRoot: repo}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) worktreeRemoveCmd(repo, dest string, force bool) tea.Cmd {
	return func() tea.Msg {
		err := gitx.WorktreeRemove(repo, dest, force)
		return worktreeRemovedMsg{repo: repo, dest: dest, forced: force, err: err}
	}
}

func (m *Model) handleWorktreeRemoved(msg worktreeRemovedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Local changes: offer the forced retry rather than just failing.
		if !msg.forced && gitx.IsDirtyWorktreeError(msg.err) {
			m.pending = &pendingOp{kind: opWorktree, items: []string{msg.dest}, repoRoot: msg.repo, force: true}
			m.mode = modeConfirm
			return m, nil
		}
		return m, m.note(msg.err.Error(), true)
	}
	m.unmark(msg.dest)
	parent := filepath.Dir(msg.dest)
	if n := m.tr.FindByPath(parent); n != nil && n.Loaded {
		_ = m.tr.Refresh(n)
	}
	m.reflatten()
	m.syncWatches()
	m.saveState()
	cmds := m.invalidateStatusCmds([]string{msg.repo})
	cmds = append(cmds, m.note("Removed worktree: "+filepath.Base(msg.dest), false))
	return m, tea.Batch(cmds...)
}

// --- misc ---

func (m *Model) toggleHelp() (tea.Model, tea.Cmd) {
	m.mode = modeHelp
	return m, nil
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.saveState()
	m.watcher.Close()
	return m, tea.Quit
}
