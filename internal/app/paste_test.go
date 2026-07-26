package app

import (
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func pasteModel(md mode) *Model {
	m := &Model{mode: md, height: 20, input: textinput.New()}
	m.input.Focus()
	return m
}

// A bracketed paste arrives as its own message, not as key presses, so the
// fuzzy query only sees the clipboard if Update routes PasteMsg to the input.
func TestPasteIntoFuzzyQuery(t *testing.T) {
	m := pasteModel(modeFuzzy)
	m.fuzzyCands = []string{"internal/app/app.go", "README.md"}
	m.refuzzy()

	m.Update(tea.PasteMsg{Content: "app.go"})

	if got := m.input.Value(); got != "app.go" {
		t.Fatalf("query = %q, want %q", got, "app.go")
	}
	// The paste must re-run the search, not just fill the box.
	if len(m.fuzzyMatches) != 1 || m.fuzzyMatches[0].Str != "internal/app/app.go" {
		t.Errorf("matches = %+v, want only internal/app/app.go", m.fuzzyMatches)
	}
}

// Pasting appends at the cursor rather than replacing what's typed.
func TestPasteAppendsToTypedQuery(t *testing.T) {
	m := pasteModel(modeFuzzy)
	m.input.SetValue("internal/")
	m.input.CursorEnd()

	m.Update(tea.PasteMsg{Content: "app"})

	if got := m.input.Value(); got != "internal/app" {
		t.Errorf("query = %q, want %q", got, "internal/app")
	}
}

// Prompts (new file, rename, worktree branch) share the same input and were
// equally deaf to the clipboard.
func TestPasteIntoPrompt(t *testing.T) {
	m := pasteModel(modePrompt)

	m.Update(tea.PasteMsg{Content: "notes.md"})

	if got := m.input.Value(); got != "notes.md" {
		t.Errorf("prompt value = %q, want %q", got, "notes.md")
	}
}

// A multi-line clipboard must not corrupt a single-line input.
func TestPasteCollapsesNewlines(t *testing.T) {
	m := pasteModel(modeFuzzy)

	m.Update(tea.PasteMsg{Content: "one\ntwo\tthree"})

	if got := m.input.Value(); got != "one two three" {
		t.Errorf("value = %q, want newlines and tabs collapsed to spaces", got)
	}
}

// Outside a text-entry mode a stray paste must not leak into the input or be
// mistaken for keybindings.
func TestPasteIgnoredInNormalMode(t *testing.T) {
	m := pasteModel(modeNormal)

	m.Update(tea.PasteMsg{Content: "qqq"})

	if got := m.input.Value(); got != "" {
		t.Errorf("normal-mode paste reached the input: %q", got)
	}
}
