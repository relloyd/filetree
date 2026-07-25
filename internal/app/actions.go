package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// handleConfirmKey resolves the staged operation: y/n for trash and
// conflict-free transfers; o(verwrite)/k(eep both)/n when conflicts exist.
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
			return m, m.trashCmd(p.items[0])
		case "n", "esc", "q":
			return cancel()
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
func (m *Model) gitRelPath(n *tree.Node) string {
	if root := m.repoRootFor(filepath.Dir(n.Path)); root != "" {
		return gitx.RelPath(root, n.Path)
	}
	return n.Path
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
	return m.execCommand(name, c, config.Vars{
		Path:    n.Path,
		RelPath: m.gitRelPath(n),
		Dir:     dir,
		Root:    m.tr.Root.Path,
		Name:    n.Name,
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
		if n := m.tr.FindByPath(target); n != nil {
			for i, r := range m.rows {
				if r.Node == n {
					m.cursor = i
					m.ensureVisible()
					break
				}
			}
		}
	}
	m.syncWatches()
	m.saveState()
	return tea.Batch(m.invalidateStatusCmds([]string{parent})...)
}

func (m *Model) confirmDelete() (tea.Model, tea.Cmd) {
	n := m.selected()
	if n == nil {
		return m, nil
	}
	if n == m.tr.Root {
		return m, m.note("Cannot delete the root", true)
	}
	m.pending = &pendingOp{kind: opTrash, items: []string{n.Path}}
	m.mode = modeConfirm
	return m, nil
}

func (m *Model) trashCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return trashDoneMsg{path: path, err: m.plat.Trash(path)}
	}
}

func (m *Model) handleTrashDone(msg trashDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.note(msg.err.Error(), true)
	}
	m.unmark(msg.path)
	cmd := m.afterFsMutation(filepath.Dir(msg.path) + "/")
	return m, tea.Batch(cmd, m.note("Moved to Trash: "+filepath.Base(msg.path), false))
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
