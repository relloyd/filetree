// Package app is the Bubble Tea program: model, update loop, and rendering
// for the file tree.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
	"github.com/relloyd/filetree/internal/tmux"
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

// RevealMsg asks the tree to put its cursor on Path. It arrives from outside
// the process — an editor bound to "ft jump <file>" — by way of
// Program.Send, which is the only goroutine-safe door into the model.
//
// These two are exported because main builds them: the socket listener lives
// there, next to the Program handle it needs.
type RevealMsg struct {
	Path string
	// Reply carries the outcome back to the waiting client, and MUST be
	// buffered. The sender gives up after a timeout, and an unbuffered channel
	// would then wedge the Update loop forever on a receive nobody is left to
	// make.
	Reply chan<- RevealResult
}

// RevealResult is what the jump command exits on. Reason is written for a
// person: it ends up in the editor's status line.
type RevealResult struct {
	OK     bool
	Reason string
}

type opKind int

const (
	opTrash opKind = iota
	opCopy
	opMove
	opWorktree
	opKillSession
)

// pendingOp is a staged operation awaiting confirmation in modeConfirm.
type pendingOp struct {
	kind      opKind
	items     []string // one path for trash/worktree; marked paths otherwise
	targetDir string   // copy/move destination
	conflicts int      // destinations that already exist
	repoRoot  string   // opWorktree: the repo owning the worktree
	force     bool     // opWorktree: re-asking after a dirty-worktree refusal
	session   string   // opKillSession: the tmux session to kill
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

	// selfPane is ft's own $TMUX_PANE, empty outside tmux. It is what "beside
	// me" is measured from when an agent session is opened in a pane, and it
	// is captured once at startup for the same reason ipc.Serve captures it:
	// only this process knows where it is running.
	selfPane string

	// finderPane is the resize ft made to its own pane for the finder, held
	// only while the finder is open. See syncFinderPaneWidth.
	finderPane finderPane

	// scopeDir confines the finder to one root-relative directory; "" is the
	// whole tree. Session-only, and set at entry: "F" captures it from the
	// selection, "/" clears it, and resuming leaves it alone.
	scopeDir string

	// recent is this root's history of opened files, reloaded by loadRoot
	// alongside st.
	recent *state.Recent

	// Bookmark view state. bmRows indexes bmAll after the query has narrowed
	// it, the same indirection content mode uses for grepRows/grepHits, and
	// bmMatched carries what the query matched so the rows can highlight it.
	// bmAll is re-read and re-resolved on entry, since "ft bookmark" writes
	// the store from its own process while this one is running.
	//
	// bmInput is the view's own query field rather than the shared one. The two
	// views remember where they were independently — "B" comes back to the
	// bookmarks you were filtering, "f" to the tree search you were running —
	// and one input cannot hold both.
	bmInput    textinput.Model
	bmAll      []resolvedBookmark
	bmRows     []int
	bmMatched  [][]int
	bmSort     bookmarkSort
	bmAllRepos bool // ctrl+s: every project's store, not just this one
	bmHidden   int  // bookmarks in other stores, when narrowed to this one

	// The named tmux sessions agent tools run in ("T"). Like the bookmark
	// view this keeps its own query field, so "T" comes back to the sessions
	// you were filtering rather than to the tree search.
	//
	// tmuxAll is re-read on entry and after every kill: the sessions belong to
	// the tmux server, and any other ft — or the user, at a shell — can change
	// the list while this one is showing it.
	tmuxInput   textinput.Model
	tmuxAll     []tmux.Session
	tmuxRows    []int
	tmuxMatched [][]int
	tmuxErr     string // what List had to say, if anything

	// homeRoot is the project root to return to from the scratch or worktrees
	// view (session-only). Remembered once, on entering the first of them, and
	// not overwritten by the second: the two views are siblings, so there is no
	// stack of them to unwind — Esc goes home from either.
	homeRoot string

	// onRoot is told the new root every time it changes, so the jump listener
	// can say which tree this instance is showing without a trip through
	// Update. nil unless something asked for it.
	onRoot func(root string)

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

	// keyConflicts is what buildBindings had to refuse: reported once in the
	// status bar and listed in full under the help key.
	keyConflicts []keyConflict

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
		selfPane:      os.Getenv("TMUX_PANE"),
	}

	m.input = textinput.New()
	m.input.SetVirtualCursor(true)
	m.typeInput = textinput.New()
	m.typeInput.SetVirtualCursor(true)
	m.typeInput.Placeholder = "hcl, *.tf, infra/**/*.yaml"
	m.grepInput = textinput.New()
	m.grepInput.SetVirtualCursor(true)
	m.grepInput.Placeholder = "regexp searched with ripgrep"
	m.bmInput = textinput.New()
	m.bmInput.SetVirtualCursor(true)
	m.bmInput.Placeholder = "path or line contents"
	m.tmuxInput = textinput.New()
	m.tmuxInput.SetVirtualCursor(true)
	m.tmuxInput.Placeholder = "repo, branch or tool"

	m.buildBindings()

	w, err := fsops.NewWatcher(time.Duration(cfg.General.WatchDebounceMs) * time.Millisecond)
	if err != nil {
		return nil, err
	}
	m.watcher = w

	if err := m.loadRoot(root, nil); err != nil {
		return nil, err
	}
	return m, nil
}

// loadRoot points the model at a root directory and restores that root's
// SetRootObserver registers f to be told the tree's root, now and on every
// change. It is a setter rather than another parameter to New because the only
// caller is main and every test would otherwise have to thread a nil through.
func (m *Model) SetRootObserver(f func(root string)) {
	m.onRoot = f
	if f != nil && m.tr != nil {
		f(m.tr.Root.Path)
	}
}

// persisted state (expansion, selection, scroll, toggle overrides). On
// error the model keeps its previous root, so view switches fail safely.
//
// seed is expansion to open on a root that has none of its own — see the
// comment where it is applied. Callers with nothing to carry pass nil.
func (m *Model) loadRoot(root string, seed []string) error {
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
	//
	// A root with nothing of its own takes the caller's seed instead, which is
	// what stops re-rooting into a subtree collapsing the tree you were just
	// looking at: the dirs you had open are handed down, renamed to the new
	// root. A root you have been in before keeps its own memory — the seed is
	// a first impression, not an override.
	rels := st.Expanded
	if len(seed) > 0 && !expandsAnything(rels) {
		rels = seed
	}
	for _, rel := range rels {
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
	// Announced here rather than at each caller because every re-root — ">",
	// the scratch and worktree views, Esc home — funnels through this one
	// function, and only after it has certainly succeeded.
	if m.onRoot != nil {
		m.onRoot(root)
	}
	return nil
}

// expandsAnything reports whether a saved expansion opens more than the root
// itself, which is what tells a root visited before from one never seen. A
// root that really was left with everything collapsed reads as fresh, and
// taking a seed is the right answer for it either way.
func expandsAnything(rels []string) bool {
	for _, rel := range rels {
		if rel != "." {
			return true
		}
	}
	return false
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitFs(m.watcher)}
	cmds = append(cmds, m.ensureStatusesForExpanded()...)
	if s := m.configNote(); s != "" {
		cmds = append(cmds, m.note(s, true))
	}
	return tea.Batch(cmds...)
}

// configNote summarises what ft could not take at face value — settings that
// decoded into nothing, and the key clashes buildBindings had to settle — for
// the status bar, and is empty when there are none. The detail lives under the
// help key: the status message clears itself after a few seconds, which is long
// enough to notice a problem and not long enough to read a list of them.
func (m *Model) configNote() string {
	unknown, conflicts := m.cfg.Unknown, m.keyConflicts
	help := m.actionKeys["help"]
	switch n := len(unknown) + len(conflicts); {
	case n == 0:
		return ""
	case n == 1 && len(conflicts) == 1:
		return fmt.Sprintf("key conflict — %s (press %s)", conflicts[0], help)
	case n == 1:
		return fmt.Sprintf("unknown setting %s — nothing reads it (press %s)", unknown[0], help)
	default:
		return fmt.Sprintf("%d config warnings — press %s for details", n, help)
	}
}

// Update is a thin wrapper around update that watches the one mode transition
// with a side effect outside ft: entering and leaving the finder resizes ft's
// own tmux pane.
//
// It is done here, by comparing the mode before and after, rather than at the
// transitions themselves. There are three ways into the finder and sixteen
// assignments of modeNormal scattered across six files, so hooking the
// transitions would mean sixteen chances to forget one — and the seventeenth
// would be silent, leaving a pane stuck wide. Derived from the mode, it cannot
// drift.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.mode
	next, cmd := m.update(msg)
	if entering, crossed := finderBoundary(before, m.mode); crossed {
		m.syncFinderPaneWidth(entering)
	}
	return next, cmd
}

// finderBoundary reports whether a mode change crossed into or out of the
// finder, and which way. Moving between two modes that are both the finder, or
// neither, crosses nothing: "/" to "F" is one finder session reopened, not a
// close and a reopen, and must not resize the pane twice.
func finderBoundary(before, after mode) (entering, crossed bool) {
	if (before == modeFuzzy) == (after == modeFuzzy) {
		return false, false
	}
	return after == modeFuzzy, true
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Every finder input, not just the tree's three: at width 0 textinput
		// collapses a placeholder to its first rune and stops scrolling a long
		// value, so a field left out of this list quietly misbehaves.
		w := min(60, max(10, msg.Width-10))
		m.input.SetWidth(w)
		m.typeInput.SetWidth(w)
		m.grepInput.SetWidth(w)
		m.bmInput.SetWidth(w)
		m.tmuxInput.SetWidth(w)
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

	case RevealMsg:
		return m.handleReveal(msg)

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

	// ctrl+c backs out of an overlay the way esc does, and only quits once the
	// tree itself has the keyboard. Every mode below already handles esc and
	// returns before the m.bindings lookup at the bottom — which is exactly why
	// ctrl+c used to be inert in them — so a new mode inherits this by handling
	// esc and nothing else. It matters more now that ctrl+c is the only way out:
	// a press must never be the one that discards a half-typed rename.
	if s == "ctrl+c" && m.mode != modeNormal {
		s = "esc"
	}

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
			switch m.finderSrc {
			case srcRecent:
				return m.recentJumpAndOpen()
			case srcBookmark:
				return m.bookmarkJumpAndOpen()
			case srcTmux:
				return m.attachSession()
			}
			return m.fuzzyJump()
		case "ctrl+s":
			if m.finderSrc == srcBookmark {
				m.toggleBookmarkScope()
				return m, nil
			}
		case "ctrl+x":
			if m.finderSrc == srcBookmark {
				return m, m.forgetBookmark()
			}
			if m.finderSrc == srcTmux {
				return m.killSession()
			}
		case "ctrl+w":
			// Only the session list claims this; everywhere else it stays
			// textinput's delete-word-backward.
			if m.finderSrc == srcTmux {
				return m.paneSession()
			}
		case "alt+n":
			if m.finderSrc == srcTmux {
				return m.newSessionHere()
			}
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
	if m.finderSrc == srcBookmark {
		m.bmInput, cmd = m.bmInput.Update(msg)
		m.rebuildBookmarkRows()
		m.fuzzySel, m.fuzzyScroll = 0, 0
		return m, cmd
	}
	if m.finderSrc == srcTmux {
		m.tmuxInput, cmd = m.tmuxInput.Update(msg)
		m.rebuildTmuxRows()
		m.fuzzySel, m.fuzzyScroll = 0, 0
		return m, cmd
	}
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
//
// Every pass runs in sorted order and claims through claim(), which records
// what it displaced. Two things wanting one key is not an error worth refusing
// to start over, but it used to be invisible *and* undecided: the actions pass
// was a plain map range, so the winner was whichever Go's randomised iteration
// reached last and could differ between launches. Now the precedence is fixed —
// commands lose to actions, everything loses to navigation — and each casualty
// is on m.keyConflicts for the status bar and the help page to report.
func (m *Model) buildBindings() {
	b := map[string]func() (tea.Model, tea.Cmd){}
	owner := map[string]string{}
	m.keyConflicts = nil

	claim := func(key, who string, fn func() (tea.Model, tea.Cmd)) {
		if key == "" {
			return
		}
		if held, taken := owner[key]; taken {
			m.keyConflicts = append(m.keyConflicts, keyConflict{
				key: key, kept: who, refused: held, detail: held + " never runs",
			})
		}
		b[key], owner[key] = fn, who
	}

	// Commands share the action key namespace, so they are resolved before
	// anything is claimed: a command's key comes out of m.actionKeys, which is
	// what lets "[keys] claude-popup = ..." move it.
	cmdKeys := make(map[string]string, len(m.cfg.Commands))
	for name, c := range m.cfg.Commands {
		cmdKeys[name] = c.Key
	}
	defaults, nameClashes := mergeCommandKeys(defaultActionKeys, cmdKeys)
	keys, conflicts := resolveActionKeys(defaults, m.cfg.Keys)
	m.actionKeys = keys
	m.keyConflicts = append(m.keyConflicts, nameClashes...)
	m.keyConflicts = append(m.keyConflicts, conflicts...)

	m.finderCmds = map[string]string{}
	for _, name := range sortedCommands(m.cfg.Commands) {
		c := m.cfg.Commands[name]
		claim(m.actionKeys[name], "commands."+name, func() (tea.Model, tea.Cmd) { return m.runCommand(name) })
		if c.FinderKey == "" {
			continue
		}
		if held, taken := m.finderCmds[c.FinderKey]; taken {
			// First in sorted order keeps it, so the loser is this one.
			m.keyConflicts = append(m.keyConflicts, keyConflict{
				key: c.FinderKey, kept: "commands." + held, refused: "commands." + name,
				detail: "in the finder",
			})
			continue
		}
		m.finderCmds[c.FinderKey] = name
	}

	// A finder_key that lands on a remapped finder-local key: config.Load
	// checks finderReservedKeys, which is the *default* set, so a [keys] line
	// moving one of them is the case it cannot see.
	for _, action := range []string{"finder-next-field", "finder-prev-field", "finder-more", "finder-copy-command", "finder-clear"} {
		key := m.actionKeys[action]
		if name, taken := m.finderCmds[key]; taken && key != "" {
			m.keyConflicts = append(m.keyConflicts, keyConflict{
				key: key, kept: "keys." + action, refused: "commands." + name,
				detail: "the finder answers this key itself",
			})
			delete(m.finderCmds, key)
		}
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
		"bookmarks":      m.startBookmarks,
		"tmux-sessions":  m.startTmuxSessions,
		"detach-pane":    m.detachAgentPane,
		"new-file":       func() (tea.Model, tea.Cmd) { return m.startPrompt(promptNewFile) },
		"new-dir":        func() (tea.Model, tea.Cmd) { return m.startPrompt(promptNewDir) },
		"rename":         func() (tea.Model, tea.Cmd) { return m.startPrompt(promptRename) },
		"delete":         m.confirmDelete,
		"collapse-all":   m.collapseAll,
		"edit-config":    m.editConfig,
		"reload-config":  m.reloadConfig,
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
		"root-here":      m.rootHere,
	}
	names := make([]string, 0, len(actions))
	for action := range actions {
		names = append(names, action)
	}
	sort.Strings(names)
	for _, action := range names {
		claim(m.actionKeys[action], "keys."+action, actions[action])
	}

	// Fixed navigation, claimed last because it cannot be given up: there is no
	// [keys] entry for it, so anything it displaces would otherwise be gone with
	// no way to ask for it back.
	for _, nav := range []struct {
		keys []string
		fn   func() (tea.Model, tea.Cmd)
	}{
		{[]string{"ctrl+c"}, m.quit},
		{[]string{"f5"}, m.reloadAll},
		{[]string{"up", "k"}, m.cursorUp},
		{[]string{"down", "j"}, m.cursorDown},
		{[]string{"left", "h"}, m.leftKey},
		{[]string{"right", "l"}, m.rightKey},
		{[]string{"enter"}, m.enterKey},
		// shift+enter reaches ft only where the terminal reports modified keys
		// — inside tmux that means "set -s extended-keys on", which is off by
		// default. It is bound here rather than as root-here's default so the
		// action always has a key that works; where the chord does not arrive
		// it is a plain enter, handled by the line above.
		{[]string{"shift+enter"}, m.rootHere},
		{[]string{"g", "home"}, m.gotoTop},
		{[]string{"G", "end"}, m.gotoBottom},
		{[]string{"ctrl+d", "pgdown"}, m.halfPageDown},
		{[]string{"ctrl+u", "pgup"}, m.halfPageUp},
	} {
		for _, key := range nav.keys {
			claim(key, "navigation", nav.fn)
		}
	}
	m.bindings = b
}

// sortedCommands is the command names in a fixed order, so which of two
// commands sharing a key wins is decided here rather than by map iteration.
func sortedCommands(cmds map[string]config.Command) []string {
	names := make([]string, 0, len(cmds))
	for name := range cmds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// treeHeight is the whole body region between the header and the status bar.
// It keeps that meaning — the help and finder screens fill all of it — and the
// tree's own row budget is treeVisibleRows, which is this minus whatever the
// sticky parents pin above it.
func (m *Model) treeHeight() int {
	return max(1, m.height-2) // header + status bar
}

// stickyCap is the most lines the pinned parents may take: a third of the
// body, so the deep end of a tree can never crowd out the tree. Plain division
// is doing real work here — it lands on 0 for a body of one or two lines,
// which is what keeps stickyLines strictly below treeHeight without a second
// clamp, and so what makes treeVisibleRows provably at least 1. A max(1, …)
// would reserve the only line there and push the frame a row over m.height.
func (m *Model) stickyCap() int { return m.treeHeight() / 3 }

// stickyRows is the ancestor chain of the topmost visible row, root first and
// without the tree root, which the header line already names.
//
// It is anchored on the scroll offset rather than on the cursor because these
// lines exist to say what is above the top of the pane. Anchoring on the
// cursor would keep naming its parents after the wheel had scrolled it away,
// labelling rows with a directory none of them are in. Nothing is lost by the
// choice: rows are a depth-first flatten, so an ancestor of the cursor that
// has scrolled off the top is necessarily an ancestor of the top row too.
//
// Read straight off cfg at the point of use, the way clearMarksAfter is: no
// model mirror and no per-root state, so "alt+c" picks up an edit live. A nil
// config is off, which is what leaves the bare-Model test fixtures alone.
func (m *Model) stickyRows() []tree.Row {
	if m.cfg == nil || !m.cfg.General.StickyParents {
		return nil
	}
	// reflatten can shrink the list a moment before clampScroll catches up.
	if m.scroll < 0 || m.scroll >= len(m.rows) {
		return nil
	}
	rows := tree.Ancestors(m.rows[m.scroll])
	if n := m.stickyCap(); len(rows) > n {
		rows = rows[len(rows)-n:] // keep the nearest: they name the immediate containers
	}
	return rows
}

// stickyLines is how many body lines the pinned block occupies. It is the
// length of what renderStickyRows will draw and nothing else: an independent
// formula could disagree with the render by a line and push the bottom tree
// row off the frame.
func (m *Model) stickyLines() int { return len(m.stickyRows()) }

// treeVisibleRows is how many tree rows fit under the pinned parents — the
// tree's answer to fuzzyVisibleRows. No max(1, …): stickyCap guarantees the
// subtraction leaves at least one row, and guarantees the two halves add back
// up to treeHeight exactly, which is what the frame invariant rests on.
func (m *Model) treeVisibleRows() int { return m.treeHeight() - m.stickyLines() }

// clampScroll keeps the scroll offset inside the list. Both ends move now:
// how far down the last row may sit depends on how many parents that row
// pins, so this settles rather than clamping once.
//
// It terminates. A pass that changes anything has found a window taller than
// the one before it, and there are only stickyCap()+1 possible heights.
func (m *Model) clampScroll() {
	for range m.stickyCap() + 2 {
		s := clamp(m.scroll, 0, max(0, len(m.rows)-m.treeVisibleRows()))
		if s == m.scroll {
			return
		}
		m.scroll = s
	}
}

// ensureVisible scrolls until the cursor sits inside the content window, which
// is the body minus whatever the top row pins above it. The two decide each
// other — the window's height comes from the row scrolled to, and the row
// scrolled to comes from the window's height — so this settles by iteration.
//
// It terminates. The "cursor above" branch puts the cursor at the top and is
// done, since no window is shorter than one line. The "cursor below" branch
// moves the offset strictly down the list and never past the cursor, and only
// runs again when the new window is *shorter* than the last, which can happen
// at most stickyCap() times before there is no shorter height left. The bound
// is doubled because a clamp may land between two such passes.
//
// The two settle points cannot fight, which is what makes "G" work on a deep
// tree: at a fixed point scroll == cursor-h+1, so len(rows)-h >= scroll for any
// cursor inside the list, and clampScroll has nothing left to do. The trailing
// fallback is unreachable, and is there so an arithmetic mistake shows up as a
// jump rather than as a hang.
func (m *Model) ensureVisible() {
	for range 2*m.stickyCap() + 2 {
		h := m.treeVisibleRows()
		switch {
		case m.cursor < m.scroll:
			m.scroll = m.cursor
		case m.cursor >= m.scroll+h:
			m.scroll = m.cursor - h + 1
		default:
			s := m.scroll
			m.clampScroll()
			if m.scroll == s {
				return
			}
		}
	}
	m.scroll = m.cursor // correct for any window: the cursor becomes row 0 of it
	m.clampScroll()
}

// rowAtY maps a body line to the row of m.rows drawn on it: the pinned parents
// first, then the scrolling window.
//
// Pinned lines report the row they really are — they are rows above the scroll
// offset, not copies of them — so selection, the double-click bookkeeping and
// the chevron hit test need no case of their own, and clicking a pinned parent
// takes the cursor to it exactly the way clicking any other row does.
func (m *Model) rowAtY(y int) (int, bool) {
	if y < 1 || y > m.treeHeight() {
		return 0, false
	}
	sticky := m.stickyRows()
	if y <= len(sticky) {
		// Ancestors are not contiguous above the scroll offset, so their
		// indices are found rather than computed. Bounded by the offset, and
		// run once per click.
		n := sticky[y-1].Node
		for i := m.scroll - 1; i >= 0; i-- {
			if m.rows[i].Node == n {
				return i, true
			}
		}
		return 0, false
	}
	idx := m.scroll + (y - 1 - len(sticky))
	if idx < 0 || idx >= len(m.rows) {
		return 0, false
	}
	return idx, true
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
