package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/filetree/internal/tmux"
)

// windowTitle is the terminal title: the root's name and, when the root is in
// a repository, its branch. The branch comes from m.branches, so the title
// refreshes wherever the status bar's branch does and costs no git call.
func (m *Model) windowTitle() string {
	root := m.tr.Root.Path
	return formatTitle(filepath.Base(root), m.branches[m.repoRootFor(root)])
}

// formatTitle is windowTitle without the model, for the table test. "ft"
// leads so a tab still says what it is when a long branch is cut off, and so
// the value can never start with "-", which set-option would read as a flag.
func formatTitle(name, branch string) string {
	t := "ft — " + name
	if branch != "" {
		t += " ⎇ " + branch
	}
	return t
}

// titleSetMsg reports that a write of the title to tmux has finished.
type titleSetMsg struct{ title string }

// publishTitle copies the title to ft's tmux session (tmux.TitleOption), where
// the set-titles-string tmux.Wrap installs passes it on to the terminal's tab.
// The title in tea.View alone is not enough inside tmux: tmux keeps a pane's
// title to itself unless set-titles is on, and even then shows the *focused*
// pane's.
//
// One write in flight at a time, like the session poll: the title changes in
// quick succession at startup (the root's name, then its branch once git
// status lands), and two concurrent set-options could finish in either order,
// leaving the older title on the tab. A change made while a write is in flight
// is picked up by the titleSetMsg that ends it, since every message passes
// back through Update.
//
// Written whenever ft is in tmux, not only in a session it created: the option
// is inert unless a set-titles-string reads it, and that lets a session of the
// user's own opt in with one line of config.
func (m *Model) publishTitle() tea.Cmd {
	if m.selfPane == "" || m.titleBusy {
		return nil
	}
	title := m.windowTitle()
	if title == m.titleSent {
		return nil
	}
	m.titleBusy = true
	pane := m.selfPane
	return func() tea.Msg {
		// A failed write is not retried: it would fail again on every message,
		// and a tab that says "ft" is the state this feature started from.
		_ = tmux.SetTitle(pane, title)
		return titleSetMsg{title: title}
	}
}
