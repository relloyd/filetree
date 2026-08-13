package app

import (
	"fmt"
	"sort"

	"github.com/relloyd/filetree/internal/config"
)

// defaultActionKeys is the action half of the key namespace, under the name
// the rest of this package has always used for it. The table itself lives in
// internal/config so that the starter config can generate its [keys] reference
// from it; config cannot import this package.
var defaultActionKeys = config.DefaultActionKeys

// mergeCommandKeys puts the commands into the same name→key namespace as the
// actions, so that [keys] can move a command's key by name.
//
// Before this, a command's key could only be changed by copying its whole
// definition into the config, which is most of why a config file grew. One
// namespace also means a command and an action wanting the same key is settled
// by resolveActionKeys' ordinary rules and reported like any other clash,
// rather than the command silently never running.
//
// A command named after an action is refused instead of merged: one name
// cannot mean two things, and letting the command win would take a key off an
// action with nothing said about it.
func mergeCommandKeys(actions, commands map[string]string) (map[string]string, []keyConflict) {
	out := make(map[string]string, len(actions)+len(commands))
	for name, key := range actions {
		out[name] = key
	}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	var conflicts []keyConflict
	for _, name := range names {
		if _, taken := actions[name]; taken {
			conflicts = append(conflicts, keyConflict{
				key: commands[name], kept: "keys." + name, refused: "commands." + name,
				detail: "a command cannot take an action's name",
			})
			continue
		}
		out[name] = commands[name]
	}
	return out, conflicts
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
			detail:  "no such action or command",
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
