package app

import (
	"path/filepath"
	"testing"
)

func TestFormatTitle(t *testing.T) {
	cases := []struct {
		name, branch, want string
	}{
		{"filetree", "main", "ft — filetree ⎇ main"},
		{"filetree", "feat/window-title", "ft — filetree ⎇ feat/window-title"},
		{"filetree", "1a2b3c4d", "ft — filetree ⎇ 1a2b3c4d"}, // detached HEAD
		{"Downloads", "", "ft — Downloads"},                  // not a repository
		{"-odd", "", "ft — -odd"},                            // never a leading "-"
	}
	for _, tc := range cases {
		if got := formatTitle(tc.name, tc.branch); got != tc.want {
			t.Errorf("formatTitle(%q, %q) = %q, want %q", tc.name, tc.branch, got, tc.want)
		}
	}
}

// The branch is the root's repository's, which for a root below the top of a
// checkout is a different directory from the root itself.
func TestWindowTitleNamesRootAndBranch(t *testing.T) {
	repo := t.TempDir()
	m := rootedModel(t, repo)
	base := filepath.Base(repo)

	m.repoRoots[repo] = ""
	if got, want := m.windowTitle(), "ft — "+base; got != want {
		t.Errorf("outside a repository: %q, want %q", got, want)
	}

	m.repoRoots[repo] = filepath.Dir(repo)
	m.branches[filepath.Dir(repo)] = "main"
	if got, want := m.windowTitle(), "ft — "+base+" ⎇ main"; got != want {
		t.Errorf("inside a repository: %q, want %q", got, want)
	}
}

// The returned commands are never run here: they would write to whatever
// tmux session the test happens to be running in.
func TestPublishTitleKeepsOneWriteInFlight(t *testing.T) {
	repo := t.TempDir()
	m := rootedModel(t, repo)
	m.repoRoots[repo] = repo

	m.selfPane = ""
	if m.publishTitle() != nil {
		t.Fatal("outside tmux there is nowhere to publish, but a write was started")
	}

	m.selfPane = "%0"
	if m.publishTitle() == nil {
		t.Fatal("first title was not published")
	}
	first := m.windowTitle()

	// The branch lands while the first write is still in flight.
	m.branches[repo] = "main"
	if m.publishTitle() != nil {
		t.Fatal("a second write started while the first was in flight")
	}

	// The first write finishing must start the one that was held back.
	if _, cmd := m.Update(titleSetMsg{title: first}); cmd == nil {
		t.Fatal("the held-back title was never published")
	}
	if !m.titleBusy {
		t.Fatal("the follow-up write is not marked in flight")
	}

	// And once that lands, there is nothing left to say.
	if _, cmd := m.Update(titleSetMsg{title: m.windowTitle()}); cmd != nil {
		t.Error("an unchanged title was published again")
	}
	if m.titleBusy {
		t.Error("still marked in flight after the last write finished")
	}
}
