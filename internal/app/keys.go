package app

import (
	"fmt"
	"sort"
)

// defaultActionKeys is every configurable action and the key it answers to out
// of the box. [keys] in the config overrides an entry by name; a name absent
// from here is not an action, and resolveActionKeys says so rather than letting
// the line sit there doing nothing.
//
// An empty default means "unbound unless [keys] gives it a key" — reload is the
// only one, since F5 covers it from the fixed navigation set.
var defaultActionKeys = map[string]string{
	"quit":           "q",
	"toggle-hidden":  ".",
	"toggle-ignored": "i",
	"reload":         "",
	"reveal":         "o",
	"copy-abs":       "y",
	"copy-rel":       "Y",
	"fuzzy":          "/",
	"fuzzy-here":     "F", // the finder, confined to the selected directory
	// Finder-local: these cycle and edit the "/" input lines. Listed so [keys]
	// can remap them, but deliberately absent from the actions map in
	// buildBindings — m.bindings is normal-mode only, which is what leaves a
	// key like "tab" free for a command to own.
	"finder-next-field":   "tab",
	"finder-prev-field":   "shift+tab",
	"finder-more":         "ctrl+g",
	"finder-copy-command": "ctrl+y",
	"finder-clear":        "ctrl+o",
	// finder-resume is a normal-mode action, so unlike the finder-local keys
	// above it does belong in the actions map.
	"finder-resume": "f",
	"recent":        "b", // the finder over this root's opened-file history
	"bookmarks":     "B", // the finder over this repo's line bookmarks
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

// keyConflict is one key wanted by two things. kept and refused name them the
// way the config does — "keys.rename", "commands.lazygit-popup", "navigation" —
// and detail says what became of the loser.
type keyConflict struct {
	key     string
	kept    string
	refused string
	detail  string
}

// An empty kept means nothing wanted the key: the line names something that is
// not an action at all, so there is no contest to describe.
func (c keyConflict) String() string {
	s := fmt.Sprintf("%s: %q", c.refused, c.key)
	if c.kept != "" {
		s = fmt.Sprintf("%q: %s keeps it, %s refused", c.key, c.kept, c.refused)
	}
	if c.detail != "" {
		s += " — " + c.detail
	}
	return s
}

// finderLocalActions are the actions the "/" finder answers itself. They share
// no keys with the rest: the finder has the keyboard while it is open and the
// tree has it the rest of the time, so "tab" cycling the finder's fields does
// not stop a command — or another action — owning "tab" in the tree. They are
// resolved as their own namespace for that reason.
var finderLocalActions = map[string]bool{
	"finder-next-field":   true,
	"finder-prev-field":   true,
	"finder-more":         true,
	"finder-copy-command": true,
	"finder-clear":        true,
}

// resolveActionKeys settles which key each action answers to, given the shipped
// defaults and the user's [keys] overrides.
//
// A key belongs to one action. An override onto a key another action already
// holds is refused rather than obeyed, because obeying it would leave that other
// action with no key at all and no way to reach it — a config typo could hide
// "rename" completely. Refusing keeps every action reachable and says what it
// did, and deliberate remapping still works: name both sides and each key is
// claimed once, so a swap goes through untouched.
//
// Everything is decided over sorted action names so the outcome never depends
// on map order, which is the bug this replaces: binding used to be a plain map
// range, so a clash was won by whichever action Go's randomised iteration
// reached last, and could change from one launch to the next.
func resolveActionKeys(defaults, overrides map[string]string) (map[string]string, []keyConflict) {
	var conflicts []keyConflict

	// An override for something that is not an action is inert. It looks like a
	// working line in the config, so it has to be called out.
	unknown := make([]string, 0, len(overrides))
	for name := range overrides {
		if _, ok := defaults[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		conflicts = append(conflicts, keyConflict{
			key:     overrides[name],
			refused: "keys." + name,
			detail:  "no such action",
		})
	}

	out := map[string]string{}
	for _, finder := range []bool{false, true} {
		keys, c := resolveGroup(defaults, overrides, finder)
		for name, key := range keys {
			out[name] = key
		}
		conflicts = append(conflicts, c...)
	}
	return out, conflicts
}

// resolveGroup applies the rules above within one namespace: the tree's keys
// (finder=false) or the finder's own (finder=true).
func resolveGroup(defaults, overrides map[string]string, finder bool) (map[string]string, []keyConflict) {
	names := make([]string, 0, len(defaults))
	for name := range defaults {
		if finderLocalActions[name] == finder {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var conflicts []keyConflict

	// What each action would answer to if it were the only one asking.
	want := map[string]string{}
	for _, name := range names {
		key := defaults[name]
		if o, ok := overrides[name]; ok && o != "" {
			key = o
		}
		want[name] = key
	}

	// One pass in sorted order, so a key is settled by the first action with a
	// claim on it. A key wanted by exactly one action never reaches the conflict
	// path at all, which is what makes a swap work.
	owner := map[string]string{} // key -> action holding it
	out := map[string]string{}
	var contested []string
	for _, name := range names {
		key := want[name]
		if key == "" {
			out[name] = ""
			continue
		}
		held, taken := owner[key]
		if !taken {
			owner[key], out[name] = name, key
			continue
		}
		// Both want it. The one whose default it is has the better claim: the
		// other is asking for a key that was already spoken for.
		if defaults[name] == key && defaults[held] != key {
			owner[key], out[name] = name, key
			out[held] = ""
			contested = append(contested, held)
			conflicts = append(conflicts, keyConflict{key: key, kept: "keys." + name, refused: "keys." + held})
			continue
		}
		out[name] = ""
		contested = append(contested, name)
		conflicts = append(conflicts, keyConflict{key: key, kept: "keys." + held, refused: "keys." + name})
	}

	// A refused action falls back to its own default, if that is still free.
	for _, name := range contested {
		def := defaults[name]
		if def == "" || out[name] != "" {
			continue
		}
		if _, taken := owner[def]; !taken {
			owner[def], out[name] = name, def
		}
	}
	for i, c := range conflicts {
		if name, ok := actionOf(c.refused); ok {
			if out[name] != "" {
				conflicts[i].detail = fmt.Sprintf("keys.%s stays on %q", name, out[name])
			} else if c.detail == "" {
				conflicts[i].detail = fmt.Sprintf("keys.%s has no key now", name)
			}
		}
	}
	return out, conflicts
}

// actionOf undoes the "keys." prefix put on an action name above.
func actionOf(label string) (string, bool) {
	const prefix = "keys."
	if len(label) > len(prefix) && label[:len(prefix)] == prefix {
		return label[len(prefix):], true
	}
	return "", false
}
