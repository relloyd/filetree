// Package app is the Bubble Tea program: model, update loop, and rendering
// for the file tree.
package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/fsops"
	"github.com/relloyd/filetree/internal/gitx"
	"github.com/relloyd/filetree/internal/platform"
	"github.com/relloyd/filetree/internal/search"
	"github.com/relloyd/filetree/internal/state"
	"github.com/relloyd/filetree/internal/tree"
)

type mode int

const (
	modeNormal mode = iota
	modeFuzzy
	modePrompt
	modeConfirm
	modeHelp
)

type promptKind int

const (
	promptNewFile promptKind = iota
	promptNewDir
	promptRename
	promptWorktree
)

type (
	statusLoadedMsg struct {
		root   string
		status *gitx.RepoStatus
		branch string // branch name, or short hash on a detached HEAD
		err    error
	}
	fsBatchMsg []string
	cmdDoneMsg struct {
		name         string
		interactive  bool
		reloadConfig bool
		out          string
		err          error
	}
	trashDoneMsg struct {
		done    []string
		skipped int
		errs    []string
	}
	transferDoneMsg struct {
		kind   opKind
		items  []string
		target string
		res    fsops.Result
	}
	linkDoneMsg struct {
		url       string
		opened    bool
		untracked bool
		err       error
	}
	worktreeDoneMsg struct {
		dest string
		note string // status text to show once the explorer has switched
		err  error
	}
	worktreeRemovedMsg struct {
		repo   string
		dest   string
		forced bool
		err    error
	}
	clearStatusMsg struct{ seq int }
	// fuzzyCandsMsg is one streamed chunk of finder candidates. gen identifies
	// the walk that produced it so chunks from an abandoned search can be
	// dropped instead of resetting the current one.
	fuzzyCandsMsg struct {
		gen       int
		cands     []string
		done      bool
		truncated bool
	}
	// grepDebounceMsg fires once the Grep field has been quiet long enough to
	// be worth spawning ripgrep for.
	grepDebounceMsg struct{ gen int }
	// grepResultMsg is one batch of content matches, generation-guarded the
	// same way as fuzzyCandsMsg.
	grepResultMsg struct {
		gen     int
		hits    []search.Hit
		skipped int // matches dropped for sitting on an over-long line
		done    bool
		err     error
	}
)

type opKind int

const (
	opTrash opKind = iota
	opCopy
	opMove
	opWorktree
)

// pendingOp is a staged operation awaiting confirmation in modeConfirm.
type pendingOp struct {
	kind      opKind
	items     []string // one path for trash/worktree; marked paths otherwise
	targetDir string   // copy/move destination
	conflicts int      // destinations that already exist
	repoRoot  string   // opWorktree: the repo owning the worktree
	force     bool     // opWorktree: re-asking after a dirty-worktree refusal
}

type Model struct {
	cfg      *config.Config
	cfgPath  string
	stateDir string
	st       *state.State
	tr       *tree.Tree
	watcher  *fsops.Watcher
	plat     platform.Platform

	rows   []tree.Row
	cursor int
	scroll int
	width  int
	height int

	showHidden  bool
	showIgnored bool

	// scopeDir confines the finder to one root-relative directory; "" is the
	// whole tree. Session-only, and set at entry: "F" captures it from the
	// selection, "/" clears it, and resuming leaves it alone.
	scopeDir string

	// recent is this root's history of opened files, reloaded by loadRoot
	// alongside st.
	recent *state.Recent

	// prevRoot remembers where to return from the scratch or worktrees view
	// (session-only).
	prevRoot string

	repoRoots     map[string]string           // dir -> repo root ("" = none)
	statuses      map[string]*gitx.RepoStatus // repo root -> parsed status
	branches      map[string]string           // repo root -> branch/short hash
	statusPending map[string]bool

	mode    mode
	input   textinput.Model
	prompt  promptKind
	pending *pendingOp

	// worktreeRepo is the repo the pending worktree prompt creates into.
	worktreeRepo string

	marked    map[string]bool // absolute paths, session-only
	markOrder []string        // oldest first; the tail feeds {marked1}/{marked2}

	// Finder state. typeInput holds the file-type filter; finderField says
	// which input line has focus, and finderSrc where the candidates come from.
	typeInput   textinput.Model
	finderField finderField
	finderSrc   finderSource

	// lastPick is the row the finder was last left on; resumeWant is that row
	// still waiting to be re-selected as results stream back in.
	lastPick   finderPick
	resumeWant finderPick

	fuzzyCands    []string
	fuzzyAll      []fuzzy.Match // matches before the display cap
	fuzzyMatches  []fuzzy.Match // what the list shows, capped
	fuzzyQuery    string        // query the current matches were built from
	fuzzySel      int
	fuzzyScroll   int             // first visible match row
	fuzzyVisible  map[string]bool // rows on screen when fuzzy started
	fuzzyVisOrder []string        // same rows, in tree order

	fuzzyFilter    search.Filter
	fuzzyFilterRaw string // filter text the current walk was started with
	fuzzyFilterErr string

	// fuzzyLimitFactor multiplies the configured match cap. "ctrl+g" raises
	// it and it lasts the whole ft session, so reopening the finder keeps it.
	fuzzyLimitFactor int
	fuzzyCapped      bool // results were dropped at the match cap

	fuzzyGen     int // identifies the current walk; stale chunks are dropped
	fuzzyWalk    chan fuzzyChunk
	fuzzyCancel  chan struct{} // closed to stop the walker goroutine
	fuzzyWalking bool
	fuzzyTrunc   bool // the walk stopped at the candidate cap

	// Content search: grepInput non-empty switches the result list from file
	// names to matching lines. grepRows indexes grepHits after the Find query
	// has narrowed them.
	grepInput   textinput.Model
	grepHits    []search.Hit
	grepRows    []int
	grepRaw     string // pattern the current search was started with
	grepErr     string
	grepCapped  bool // hits were dropped at the match cap
	grepSkipped int  // matches on lines too long for the parser to buffer
	grepGen     int
	grepCh      chan search.Result
	grepCancel  context.CancelFunc
	grepRunning bool

	statusMsg string
	statusErr bool
	statusSeq int

	lastClickTime time.Time
	lastClickRow  int

	bindings   map[string]func() (tea.Model, tea.Cmd)
	actionKeys map[string]string

	// finderCmds maps a command's finder_key to its name. Separate from
	// bindings, which is normal-mode only.
	finderCmds map[string]string

	// Clickable header button x-ranges, recomputed each render.
	zoneHidden  [2]int
	zoneIgnored [2]int
}

func New(cfg *config.Config, cfgDir, root string, plat platform.Platform) (*Model, error) {
	stateDir := filepath.Join(cfgDir, "state")

	m := &Model{
		cfg:           cfg,
		cfgPath:       filepath.Join(cfgDir, "config.toml"),
		stateDir:      stateDir,
		plat:          plat,
		repoRoots:     map[string]string{},
		statuses:      map[string]*gitx.RepoStatus{},
		branches:      map[string]string{},
		statusPending: map[string]bool{},
		marked:        map[string]bool{},
		width:         80,
		height:        24,
		lastClickRow:  -1,
	}

	m.input = textinput.New()
	m.input.SetVirtualCursor(true)
	m.typeInput = textinput.New()
	m.typeInput.SetVirtualCursor(true)
	m.typeInput.Placeholder = "hcl, *.tf, infra/**/*.yaml"
	m.grepInput = textinput.New()
	m.grepInput.SetVirtualCursor(true)
	m.grepInput.Placeholder = "regexp searched with ripgrep"

	m.buildBindings()

	w, err := fsops.NewWatcher(time.Duration(cfg.General.WatchDebounceMs) * time.Millisecond)
	if err != nil {
		return nil, err
	}
	m.watcher = w

	if err := m.loadRoot(root); err != nil {
		return nil, err
	}
	return m, nil
}

// loadRoot points the model at a root directory and restores that root's
// persisted state (expansion, selection, scroll, toggle overrides). On
// error the model keeps its previous root, so view switches fail safely.
func (m *Model) loadRoot(root string) error {
	st := state.Load(m.stateDir, root)
	tr := tree.New(root, fsops.ReadDir)
	if err := tr.Expand(tr.Root); err != nil {
		return err
	}
	m.st, m.tr = st, tr
	// The history is root-relative like the finder's paths, so it belongs to
	// this root and is reloaded with it.
	m.recent = state.LoadRecent(m.stateDir, root)

	m.showHidden = m.cfg.General.ShowHidden
	if st.ShowHidden != nil {
		m.showHidden = *st.ShowHidden
	}
	m.showIgnored = m.cfg.General.ShowIgnored
	if st.ShowIgnored != nil {
		m.showIgnored = *st.ShowIgnored
	}
	// Every finder path is relative to the root, so none of them survive a
	// change of root: a bare "README.md" would otherwise resolve against the
	// new tree and silently select a different file.
	m.scopeDir = ""
	m.lastPick, m.resumeWant = finderPick{}, finderPick{}

	// Restore remembered expansion (parents come first in the saved list);
	// dirs deleted since last run are silently skipped.
	for _, rel := range st.Expanded {
		m.tr.ExpandRel(rel)
	}
	m.cursor, m.scroll = 0, 0
	m.reflatten()
	if st.Selected != "" {
		if n := m.tr.FindByPath(filepath.Join(root, filepath.FromSlash(st.Selected))); n != nil {
			for i, r := range m.rows {
				if r.Node == n {
					m.cursor = i
					break
				}
			}
		}
	}
	m.scroll = clamp(st.ScrollOffset, 0, max(0, len(m.rows)-1))
	m.ensureVisible()
	m.syncWatches()
	return nil
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitFs(m.watcher)}
	cmds = append(cmds, m.ensureStatusesForExpanded()...)
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(min(60, max(10, msg.Width-10)))
		m.typeInput.SetWidth(min(60, max(10, msg.Width-10)))
		m.grepInput.SetWidth(min(60, max(10, msg.Width-10)))
		m.clampScroll()
		m.ensureVisible()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg)

	case tea.MouseClickMsg:
		return m.handleClick(tea.Mouse(msg))

	case tea.MouseWheelMsg:
		return m.handleWheel(tea.Mouse(msg))

	case statusLoadedMsg:
		delete(m.statusPending, msg.root)
		if msg.err == nil {
			m.statuses[msg.root] = msg.status
		} else {
			m.statuses[msg.root] = nil // don't retry a failing repo on every expand
		}
		if msg.branch != "" {
			m.branches[msg.root] = msg.branch
		}
		m.reflatten() // ignored-file visibility may have changed
		return m, nil

	case fsBatchMsg:
		return m.handleFsBatch(msg)

	case cmdDoneMsg:
		return m.handleCmdDone(msg)

	case trashDoneMsg:
		return m.handleTrashDone(msg)

	case transferDoneMsg:
		return m.handleTransferDone(msg)

	case linkDoneMsg:
		if msg.err != nil {
			return m, m.note(msg.err.Error(), true)
		}
		verb := "Copied: "
		if msg.opened {
			verb = "Opened + copied: "
		}
		text := verb + msg.url
		if msg.untracked {
			text += "  (untracked — not on the remote)"
		}
		return m, m.note(text, false)

	case worktreeDoneMsg:
		if msg.err != nil {
			return m, m.note(msg.err.Error(), true)
		}
		return m.switchToWorktree(msg.dest, msg.note)

	case worktreeRemovedMsg:
		return m.handleWorktreeRemoved(msg)

	case clearStatusMsg:
		if msg.seq == m.statusSeq {
			m.statusMsg = ""
		}
		return m, nil

	case fuzzyCandsMsg:
		// A walk abandoned by esc, enter, or a changed filter keeps streaming
		// until it notices the cancel; its chunks must not land in the search
		// that replaced it.
		if msg.gen != m.fuzzyGen || m.mode != modeFuzzy {
			return m, nil
		}
		m.addFuzzyCands(msg.cands)
		if msg.done {
			m.fuzzyWalking, m.fuzzyTrunc = false, msg.truncated
			// Give up on restoring the row only if the walk is what feeds the
			// list. In content mode it runs alongside the search and usually
			// finishes first, so clearing here would cancel the restore before
			// ripgrep had produced anything to match against.
			if !m.grepping() {
				m.resumeWant = finderPick{}
			}
			return m, nil
		}
		return m, waitFuzzyWalk(m.fuzzyWalk, msg.gen)

	case grepDebounceMsg:
		if msg.gen != m.grepGen || m.mode != modeFuzzy || !m.grepping() {
			return m, nil
		}
		return m, m.startGrep(msg.gen)

	case grepResultMsg:
		if msg.gen != m.grepGen || m.mode != modeFuzzy {
			return m, nil
		}
		m.addGrepHits(msg.hits)
		m.grepSkipped += msg.skipped
		if msg.done {
			m.grepRunning = false
			m.resumeWant = finderPick{} // it never turned up; stop looking
			if msg.err != nil {
				m.grepErr = msg.err.Error()
			}
			return m, nil
		}
		if m.grepCapped {
			// Everything we can show, we have. Draining the rest of a
			// full-tree run to throw it away costs both processes real work,
			// and leaves "searching…" up next to a counter already saying it
			// is capped. ctrl+g re-runs the search when more is wanted.
			m.stopGrep()
			return m, nil
		}
		return m, waitGrep(m.grepCh, msg.gen)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch m.mode {
	case modeHelp:
		if s == "esc" || s == "q" || s == m.actionKeys["help"] {
			m.mode = modeNormal
		}
		return m, nil

	case modeConfirm:
		return m.handleConfirmKey(s)

	case modePrompt:
		switch s {
		case "esc":
			m.mode = modeNormal
			return m, nil
		case "enter":
			return m.commitPrompt()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case modeFuzzy:
		switch s {
		case "esc":
			m.stopFuzzyWalk()
			m.stopGrep()
			m.mode = modeNormal
			return m, nil
		case "enter":
			if m.finderSrc == srcRecent {
				return m.recentJumpAndOpen()
			}
			return m.fuzzyJump()
		case "up", "ctrl+p":
			m.moveFuzzySel(-1)
			return m, nil
		case "down", "ctrl+n":
			m.moveFuzzySel(1)
			return m, nil
		case "ctrl+u", "pgup":
			m.moveFuzzySel(-m.fuzzyVisibleRows() / 2)
			return m, nil
		case "ctrl+d":
			// textinput binds ctrl+d to delete-forward, and some terminals
			// send it for fn+backspace. Let it edit when there is something
			// to the right of the cursor to delete; otherwise — which is
			// where the cursor sits whenever you are browsing results — keep
			// it on half-page down.
			if m.finderCanDeleteForward() {
				return m.updateFinderInput(msg)
			}
			m.moveFuzzySel(m.fuzzyVisibleRows() / 2)
			return m, nil
		case "pgdown":
			m.moveFuzzySel(m.fuzzyVisibleRows() / 2)
			return m, nil
		case m.actionKeys["finder-more"]:
			return m, m.raiseFuzzyLimit()
		case m.actionKeys["finder-clear"]:
			return m, m.clearFinder()
		case m.actionKeys["finder-copy-command"]:
			return m.copyFinderCommand()
		case m.actionKeys["finder-next-field"]:
			return m, m.cycleFinderField(1)
		case m.actionKeys["finder-prev-field"]:
			return m, m.cycleFinderField(-1)
		}
		// Command chords come last, so a finder key can never be displaced
		// by a finder_key in the config.
		if name, ok := m.finderCmds[s]; ok {
			return m.runFinderCommand(name)
		}
		return m.updateFinderInput(msg)
	}

	if fn, ok := m.bindings[s]; ok {
		return fn()
	}
	return m, nil
}

// handlePaste feeds bracketed-paste text to the text input. A paste arrives as
// one PasteMsg rather than as key presses, so it has to be routed separately
// from handleKey or the clipboard is silently ignored in the fuzzy query and
// the prompts. textinput collapses newlines and tabs to spaces itself, so
// multi-line clipboard content stays on one line.
func (m *Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeFuzzy && m.mode != modePrompt {
		return m, nil // nothing is accepting text; a stray paste is not a command
	}
	if m.mode == modeFuzzy {
		return m.updateFinderInput(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateFinderInput feeds a key or paste to the focused finder field and
// recomputes whatever that field drives.
func (m *Model) updateFinderInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Editing any field means the user has moved on from where they left off,
	// so stop trying to restore that row.
	m.resumeWant = finderPick{}
	var cmd tea.Cmd
	switch m.finderField {
	case fieldType:
		m.typeInput, cmd = m.typeInput.Update(msg)
		return m, tea.Batch(cmd, m.applyTypeFilter())
	case fieldGrep:
		m.grepInput, cmd = m.grepInput.Update(msg)
		return m, tea.Batch(cmd, m.applyGrep())
	default:
		m.input, cmd = m.input.Update(msg)
		m.refuzzy()
		return m, cmd
	}
}

// buildBindings maps keys to actions: user command keys first, then
// configurable actions (which win conflicts), then fixed navigation.
func (m *Model) buildBindings() {
	b := map[string]func() (tea.Model, tea.Cmd){}

	m.finderCmds = map[string]string{}
	for name, c := range m.cfg.Commands {
		if c.Key != "" {
			b[c.Key] = func() (tea.Model, tea.Cmd) { return m.runCommand(name) }
		}
		if c.FinderKey != "" {
			m.finderCmds[c.FinderKey] = name
		}
	}

	defaults := map[string]string{
		"quit":           "q",
		"toggle-hidden":  ".",
		"toggle-ignored": "i",
		"reload":         "", // F5 reloads; listed so [keys] can still bind one
		"reveal":         "o",
		"copy-abs":       "y",
		"copy-rel":       "Y",
		"fuzzy":          "/",
		"fuzzy-here":     "F", // the finder, confined to the selected directory
		// Finder-local: cycles the "/" input lines. Listed here so [keys] can
		// remap it, but deliberately absent from the actions map below —
		// m.bindings is normal-mode only.
		"finder-next-field":   "tab",
		"finder-prev-field":   "shift+tab",
		"finder-more":         "ctrl+g",
		"finder-copy-command": "ctrl+y",
		"finder-clear":        "ctrl+l",
		// finder-resume is a normal-mode action, so unlike the finder-local
		// keys above it does belong in the actions map below.
		"finder-resume": "f",
		"recent":        "b", // the finder over this root's opened-file history
		"new-file":      "a",
		"new-dir":       "A",
		"rename":        "R",
		"delete":        "d",
		"collapse-all":  "H",
		"edit-config":   "C",
		"help":          "?",
		"mark":          "space",
		"clear-marks":   "esc",
		"copy-here":     "p",
		"move-here":     "m",
		"scratch":       "s",
		"scratch-new":   "S",
		"copy-url":      "u",
		"open-url":      "U",
		"worktrees":     "w",
		"worktree-new":  "W",
	}
	m.actionKeys = map[string]string{}
	for action, key := range defaults {
		if o, ok := m.cfg.Keys[action]; ok && o != "" {
			key = o
		}
		m.actionKeys[action] = key
	}

	actions := map[string]func() (tea.Model, tea.Cmd){
		"quit":           m.quit,
		"toggle-hidden":  m.toggleHidden,
		"toggle-ignored": m.toggleIgnored,
		"reload":         m.reloadAll,
		"reveal":         m.reveal,
		"copy-abs":       m.copyAbs,
		"copy-rel":       m.copyRel,
		"fuzzy":          m.startFuzzy,
		"fuzzy-here":     m.startFuzzyHere,
		"finder-resume":  m.resumeFuzzy,
		"recent":         m.startRecent,
		"new-file":       func() (tea.Model, tea.Cmd) { return m.startPrompt(promptNewFile) },
		"new-dir":        func() (tea.Model, tea.Cmd) { return m.startPrompt(promptNewDir) },
		"rename":         func() (tea.Model, tea.Cmd) { return m.startPrompt(promptRename) },
		"delete":         m.confirmDelete,
		"collapse-all":   m.collapseAll,
		"edit-config":    m.editConfig,
		"help":           m.toggleHelp,
		"mark":           m.toggleMark,
		"clear-marks":    m.escKey,
		"copy-here":      func() (tea.Model, tea.Cmd) { return m.stageTransfer(opCopy) },
		"move-here":      func() (tea.Model, tea.Cmd) { return m.stageTransfer(opMove) },
		"scratch":        m.toggleScratch,
		"scratch-new":    m.scratchNew,
		"copy-url":       func() (tea.Model, tea.Cmd) { return m.linkAction(false) },
		"open-url":       func() (tea.Model, tea.Cmd) { return m.linkAction(true) },
		"worktrees":      m.toggleWorktrees,
		"worktree-new":   m.worktreeNew,
	}
	for action, fn := range actions {
		if key := m.actionKeys[action]; key != "" {
			b[key] = fn
		}
	}

	b["ctrl+c"] = m.quit
	b["f5"] = m.reloadAll
	b["up"], b["k"] = m.cursorUp, m.cursorUp
	b["down"], b["j"] = m.cursorDown, m.cursorDown
	b["left"], b["h"] = m.leftKey, m.leftKey
	b["right"], b["l"] = m.rightKey, m.rightKey
	b["enter"] = m.enterKey
	b["g"], b["home"] = m.gotoTop, m.gotoTop
	b["G"], b["end"] = m.gotoBottom, m.gotoBottom
	b["ctrl+d"], b["pgdown"] = m.halfPageDown, m.halfPageDown
	b["ctrl+u"], b["pgup"] = m.halfPageUp, m.halfPageUp
	m.bindings = b
}

func waitFs(w *fsops.Watcher) tea.Cmd {
	return func() tea.Msg {
		batch, ok := <-w.Events
		if !ok {
			return nil
		}
		return fsBatchMsg(batch)
	}
}

// visible is the row filter applied when flattening the tree.
func (m *Model) visible(n *tree.Node) bool {
	if !m.showHidden && len(n.Name) > 0 && n.Name[0] == '.' {
		return false
	}
	if !m.showIgnored && m.nodeCode(n) == gitx.Ignored {
		return false
	}
	return true
}

func (m *Model) selected() *tree.Node {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].Node
}

// reflatten recomputes visible rows, keeping the cursor on the same node
// when it survives (or clamped nearby when it doesn't).
func (m *Model) reflatten() {
	var selPath string
	if sel := m.selected(); sel != nil {
		selPath = sel.Path
	}
	m.rows = m.tr.Flatten(m.visible)
	found := false
	if selPath != "" {
		for i, r := range m.rows {
			if r.Node.Path == selPath {
				m.cursor, found = i, true
				break
			}
		}
	}
	if !found {
		m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
	}
	m.clampScroll()
	m.ensureVisible()
}

func (m *Model) treeHeight() int {
	return max(1, m.height-2) // header + status bar
}

func (m *Model) clampScroll() {
	m.scroll = clamp(m.scroll, 0, max(0, len(m.rows)-m.treeHeight()))
}

func (m *Model) ensureVisible() {
	h := m.treeHeight()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+h {
		m.scroll = m.cursor - h + 1
	}
	m.clampScroll()
}

// syncWatches points the fs watcher at every visible expanded directory.
func (m *Model) syncWatches() {
	var dirs []string
	for _, r := range m.rows {
		if r.Node.IsDir && r.Node.Expanded {
			dirs = append(dirs, r.Node.Path)
		}
	}
	m.watcher.Sync(dirs)
}

// saveState persists expansion, selection, scroll, and toggles. Best-effort:
// a failed save should never interrupt interaction.
func (m *Model) saveState() {
	m.st.Expanded = m.tr.ExpandedRels()
	if sel := m.selected(); sel != nil {
		m.st.Selected = m.tr.Rel(sel.Path)
	}
	m.st.ScrollOffset = m.scroll
	h, ig := m.showHidden, m.showIgnored
	m.st.ShowHidden, m.st.ShowIgnored = &h, &ig
	_ = m.st.Save(m.stateDir)
}

// note sets a transient status-bar message that clears after a few seconds.
// Newlines are folded away: git and command output is multi-line, and the
// status bar is a single row of the rendered frame.
func (m *Model) note(s string, isErr bool) tea.Cmd {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " "))
	m.statusMsg, m.statusErr = s, isErr
	m.statusSeq++
	seq := m.statusSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{seq: seq} })
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(hi, max(lo, v))
}
