package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keyOf is the resolved key for one action, for tests that only care about one.
func keyOf(t *testing.T, overrides map[string]string, action string) (string, []keyConflict) {
	t.Helper()
	keys, conflicts := resolveActionKeys(defaultActionKeys, overrides)
	return keys[action], conflicts
}

// With nothing overridden the shipped defaults must survive untouched, and must
// not fight each other: a duplicate among the defaults themselves would be a
// clash nobody could resolve from the config.
func TestResolveActionKeysDefaultsAreConflictFree(t *testing.T) {
	keys, conflicts := resolveActionKeys(defaultActionKeys, nil)
	if len(conflicts) != 0 {
		t.Errorf("the shipped defaults conflict: %v", conflicts)
	}
	for action, want := range defaultActionKeys {
		if got := keys[action]; got != want {
			t.Errorf("%s = %q, want %q", action, got, want)
		}
	}
}

func TestResolveActionKeys(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides map[string]string
		want      map[string]string // action -> resolved key
		conflicts int
		says      string // substring the conflict has to contain
	}{{
		name:      "a free key is simply taken",
		overrides: map[string]string{"rename": "f2"},
		want:      map[string]string{"rename": "f2", "worktree-new": "W"},
	}, {
		// The point of refusing rather than obeying: both actions keep a key.
		name:      "an override onto a key another action holds is refused",
		overrides: map[string]string{"worktree-new": "R"},
		want:      map[string]string{"rename": "R", "worktree-new": "W"},
		conflicts: 1,
		says:      `keys.worktree-new stays on "W"`,
	}, {
		// Naming both sides is how you really move a key, so it has to work
		// without a murmur — including when the two simply trade.
		name:      "a swap is not a clash",
		overrides: map[string]string{"rename": "W", "worktree-new": "R"},
		want:      map[string]string{"rename": "W", "worktree-new": "R"},
	}, {
		name:      "moving one action out of the way frees its key",
		overrides: map[string]string{"rename": "f2", "worktree-new": "R"},
		want:      map[string]string{"rename": "f2", "worktree-new": "R"},
	}, {
		// Neither owns "x" by default, so the tie goes to the first by name and
		// the loser falls back rather than ending up unbound.
		name:      "two overrides onto one free key",
		overrides: map[string]string{"rename": "x", "delete": "x"},
		want:      map[string]string{"delete": "x", "rename": "R"},
		conflicts: 1,
		says:      "keys.rename stays",
	}, {
		name:      "an unknown action name is reported, not silently ignored",
		overrides: map[string]string{"worktreenew": "R"},
		want:      map[string]string{"rename": "R", "worktree-new": "W"},
		conflicts: 1,
		says:      "no such action",
	}, {
		// The finder owns its keys only while it is open, so "tab" cycling its
		// fields is no reason to refuse "tab" in the tree.
		name:      "the finder's keys are a separate namespace",
		overrides: map[string]string{"quit": "tab"},
		want:      map[string]string{"quit": "tab", "finder-next-field": "tab"},
	}, {
		name:      "within the finder they still contend",
		overrides: map[string]string{"finder-clear": "tab"},
		want:      map[string]string{"finder-next-field": "tab", "finder-clear": "ctrl+o"},
		conflicts: 1,
		says:      "keys.finder-clear stays",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			keys, conflicts := resolveActionKeys(defaultActionKeys, tc.overrides)
			for action, want := range tc.want {
				if got := keys[action]; got != want {
					t.Errorf("%s = %q, want %q", action, got, want)
				}
			}
			if len(conflicts) != tc.conflicts {
				t.Fatalf("got %d conflicts, want %d: %v", len(conflicts), tc.conflicts, conflicts)
			}
			if tc.says != "" && !strings.Contains(conflicts[0].String(), tc.says) {
				t.Errorf("conflict = %q, want it to mention %q", conflicts[0], tc.says)
			}
		})
	}
}

// Every action must come out with a key unless it ships without one, whatever
// the config asks for. This is the property the whole refuse-the-override
// policy exists to hold: a clash may cost you the binding you asked for, never
// the one you already had.
func TestResolveActionKeysNeverStrandsAnAction(t *testing.T) {
	for _, overrides := range []map[string]string{
		{"worktree-new": "R"},
		{"rename": "d", "delete": "R"},
		{"rename": "x", "delete": "x", "quit": "x"},
		{"nonsense": "R"},
	} {
		keys, _ := resolveActionKeys(defaultActionKeys, overrides)
		for action, def := range defaultActionKeys {
			if def != "" && keys[action] == "" {
				t.Errorf("overrides %v left %s unbound", overrides, action)
			}
		}
	}
}

// The commented [keys] block in the starter is the only list of action names a
// user ever sees, so an action missing from it is an action nobody can rebind
// — they would have to read the source to learn the name. Adding one is two
// edits in two packages, which is exactly the pair that drifts.
func TestEveryActionIsDocumentedInTheStarter(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "config", "starter.go"))
	if err != nil {
		t.Fatal(err)
	}
	for action := range defaultActionKeys {
		if !strings.Contains(string(src), "\n# "+action+" = ") {
			t.Errorf("commands.%s is bindable but absent from the starter's [keys] block", action)
		}
	}
}
