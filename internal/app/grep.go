package app

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/search"
)

// grepDebounce is how long the finder waits before running ripgrep. A regex is
// typed one character at a time and every keystroke would otherwise spawn a
// process — and half-typed patterns like "(" are not even valid.
const grepDebounce = 150 * time.Millisecond

// grepping reports whether the finder is in content mode. It is derived from
// the Grep field rather than being a mode of its own, so View, handleKey and
// the mouse handler keep their single modeFuzzy arm.
func (m *Model) grepping() bool { return m.grepInput.Value() != "" }

// grepMaxPerFile caps matches per file so one generated file cannot flood the
// list. A configured 0 means no limit.
func (m *Model) grepMaxPerFile() int {
	if m.cfg == nil {
		return config.DefaultFuzzyGrepMaxPerFile
	}
	return m.cfg.General.FuzzyGrepMaxPerFile
}

// applyGrep reacts to an edit of the Grep field. Cursor movement leaves the
// text alone and must not restart anything.
func (m *Model) applyGrep() tea.Cmd {
	if m.grepInput.Value() == m.grepRaw {
		return nil
	}
	m.grepRaw = m.grepInput.Value()
	return m.scheduleGrep()
}

// scheduleGrep cancels the running search and arms the debounce for a new one.
func (m *Model) scheduleGrep() tea.Cmd {
	m.stopGrep() // bumps the generation, so results in flight are now stale
	m.grepHits, m.grepRows, m.grepErr = nil, nil, ""
	m.grepCapped, m.grepSkipped = false, 0
	m.fuzzySel, m.fuzzyScroll = 0, 0
	if !m.grepping() {
		return nil
	}
	m.grepRunning = true
	gen := m.grepGen
	return tea.Tick(grepDebounce, func(time.Time) tea.Msg { return grepDebounceMsg{gen: gen} })
}

// startGrep launches ripgrep for the current Grep pattern, scoped by the same
// type filter the file list uses and by the tree's hidden/ignored toggles.
func (m *Model) startGrep(gen int) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.grepCancel = cancel
	ch := make(chan search.Result, 4)
	m.grepCh = ch
	go search.Run(ctx, m.tr.Root.Path, m.grepQuery(), ch)
	return waitGrep(ch, gen)
}

// waitGrep receives one batch of hits, re-armed by the Update handler until
// the search reports done — the same shape as the candidate walk.
func waitGrep(ch <-chan search.Result, gen int) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return nil
		}
		return grepResultMsg{gen: gen, hits: r.Hits, skipped: r.Skipped, done: r.Done, err: r.Err}
	}
}

// stopGrep kills a running ripgrep. Leaving the finder or retyping the pattern
// should not leave a search of a huge root grinding away.
//
// It bumps the generation, so stopping and superseding are the same thing: a
// batch still in flight can never be mistaken for the current search by a
// caller that only stopped (startFuzzy, fuzzyJump, esc). Without that, a stale
// batch delivered into a reopened finder would re-arm on the nilled channel and
// block forever.
func (m *Model) stopGrep() {
	if m.grepCancel != nil {
		m.grepCancel()
		m.grepCancel = nil
	}
	m.grepCh = nil
	m.grepRunning = false
	m.grepGen++
}

// addGrepHits appends a batch, stopping at the display cap. Hits past the cap
// are dropped rather than held, so raising the limit has to search again.
func (m *Model) addGrepHits(hits []search.Hit) {
	limit := m.fuzzyLimit()
	for _, h := range hits {
		if len(m.grepHits) >= limit {
			m.grepCapped = true
			break
		}
		m.grepHits = append(m.grepHits, h)
	}
	m.rebuildGrepRows()
}

// grepQuery is the search the Grep field currently describes. Both the run and
// the displayed command build from this one value, so what the status bar
// shows is what ripgrep was given.
func (m *Model) grepQuery() search.Query {
	return search.Query{
		Regex:      m.grepInput.Value(),
		Filter:     m.fuzzyFilter,
		Hidden:     m.showHidden,
		NoIgnore:   m.showIgnored,
		MaxPerFile: m.grepMaxPerFile(),
		Path:       m.scopeRel(),
	}
}

// finderCommand renders what the finder is doing as a shell command, for
// checking its results against ripgrep directly. With a pattern typed that is
// a content search; with only a Type filter it is `rg --files`, listing the
// files the filter selects. With neither there is nothing to express, and the
// status bar goes back to showing the selection.
//
// It differs from what Run executes in two deliberate ways: the output flags
// are human-readable rather than --json, and the search path is passed as an
// absolute argument instead of being the working directory. Neither changes
// which files or lines match, so the command is self-contained — paste it into
// any shell and it reproduces the result set, printed with absolute paths
// rather than root-relative ones. When the finder is scoped, that path is the
// scope directory, so the command stays a faithful description of the list.
//
// One caveat for the --files form: the finder's own list comes from the walk
// in fuzzy.go, which applies the same globs but takes gitignore state from the
// cached `git status` and stops at the candidate cap. So it answers "does my
// Type filter select what I think it does", not "is the walk complete".
func (m *Model) finderCommand() string {
	q := m.grepQuery()
	var args []string
	switch {
	case m.grepping():
		args = search.DisplayArgs(q)
	case !q.Filter.Empty():
		args = search.FileListArgs(q)
	default:
		return ""
	}
	parts := []string{"rg"}
	for _, a := range args {
		parts = append(parts, config.ShellQuote(a))
	}
	return strings.Join(append(parts, config.ShellQuote(m.scopeAbs())), " ")
}

// copyFinderCommand puts the command on the clipboard, since the status bar
// can only show as much of it as fits.
func (m *Model) copyFinderCommand() (tea.Model, tea.Cmd) {
	cmd := m.finderCommand()
	if cmd == "" {
		return m, nil
	}
	if err := m.plat.CopyToClipboard(cmd); err != nil {
		return m, m.note(err.Error(), true)
	}
	return m, m.note("Copied: "+cmd, false)
}

// finderCapped reports whether results were left out because of the match cap,
// in whichever mode the finder is in.
func (m *Model) finderCapped() bool {
	if m.grepping() {
		return m.grepCapped
	}
	return m.fuzzyCapped
}

// rebuildGrepRows applies the Find query on top of the content hits: the Type
// filter picks the files, the pattern picks the lines, and the query narrows
// which of them are shown — the fd-then-rg combination on one screen.
func (m *Model) rebuildGrepRows() {
	m.grepRows = m.grepRows[:0]
	if q := m.input.Value(); q == "" {
		for i := range m.grepHits {
			m.grepRows = append(m.grepRows, i)
		}
	} else {
		paths := make([]string, len(m.grepHits))
		for i, h := range m.grepHits {
			paths[i] = h.Path
		}
		for _, mt := range fuzzy.Find(q, paths) {
			m.grepRows = append(m.grepRows, mt.Index)
		}
	}
	m.fuzzySel = clamp(m.fuzzySel, 0, max(0, len(m.grepRows)-1))
	m.fuzzyScroll = clamp(m.fuzzyScroll, 0, max(0, len(m.grepRows)-m.fuzzyVisibleRows()))
	m.applyResumeTarget()
}

// finderLen is how many rows the result list has, whichever mode it is in.
func (m *Model) finderLen() int {
	if m.grepping() {
		return len(m.grepRows)
	}
	return len(m.fuzzyMatches)
}

// finderPath is the root-relative path row i points at, or "" if there is no
// such row. Content rows carry a line number as well, but enter jumps to the
// file: the line is context for choosing, not a destination.
func (m *Model) finderPath(i int) string {
	if i < 0 || i >= m.finderLen() {
		return ""
	}
	if m.grepping() {
		return m.grepHits[m.grepRows[i]].Path
	}
	return m.fuzzyMatches[i].Str
}
