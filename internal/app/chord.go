package app

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Chords are bindings of more than one key, written with a space between them:
// "n right" is n, then the right arrow. The first key does nothing on its own
// but wait, with the status bar listing what can follow it; the next key either
// completes a binding, waits for more, or cancels.
//
// They are ordinary entries in m.bindings, keyed by the whole sequence, so
// [keys] remaps one the way it remaps anything else and the conflict reporting
// needs no case of its own. What is extra is m.chordPrefixes — every proper
// prefix of a bound sequence — and m.chord, the prefix typed so far.
//
// There is no timeout. A chord waits until it is finished or cancelled, which
// esc and ctrl+c do; any other key cancels it as well and is swallowed rather
// than run, since a key typed in the middle of a chord was not meant for the
// tree. A mouse click or wheel cancels it too.

// canonKey writes a key the way m.bindings is keyed, so "n  right" in a config
// is the same binding as "n right".
func canonKey(k string) string {
	return strings.Join(strings.Fields(k), " ")
}

// canonKeys is canonKey over a name→key table, returning a copy.
func canonKeys(keys map[string]string) map[string]string {
	if keys == nil {
		return nil
	}
	out := make(map[string]string, len(keys))
	for name, key := range keys {
		out[name] = canonKey(key)
	}
	return out
}

// chordAliases are the letters that stand in for an arrow inside a chord, as
// they do in the tree. Tried only when the letter itself completes nothing, so
// a binding that names "n l" outright still gets it.
var chordAliases = map[string]string{
	"h": "left",
	"j": "down",
	"k": "up",
	"l": "right",
}

// arrowGlyphs are how the help page and the status bar print the arrow keys.
var arrowGlyphs = map[string]string{
	"right": "→",
	"down":  "↓",
	"up":    "↑",
	"left":  "←",
}

// keyLabel is a key as the help page and status bar print it: a chord's arrows
// drawn as arrows. A single key is left as it is written, so "left" on its own
// still reads as the key's name.
func keyLabel(k string) string {
	parts := strings.Fields(k)
	if len(parts) < 2 {
		return k
	}
	for i, p := range parts {
		if g, ok := arrowGlyphs[p]; ok {
			parts[i] = g
		}
	}
	return strings.Join(parts, " ")
}

// settleChords finds every prefix the bound sequences need, and refuses what
// cannot coexist with them. A key that is bound on its own and also starts a
// chord can only be one of the two:
//
//   - Navigation wins, because it cannot be given up — there is no [keys] entry
//     for it — so a chord starting with "j" is the one refused.
//   - Otherwise the chord wins. It is several bindings rather than one, and
//     whoever wrote "n right" knew what its first key was.
//
// The same rule covers one chord being the prefix of another: "n x" bound and
// "n x y" bound leaves "n x" as the prefix. And a chord that continues with
// esc or ctrl+c is refused outright, since those cancel before anything is
// looked up.
//
// Whatever loses is unbound in m.actionKeys too, so the help page does not go
// on listing a key that no longer does what it says.
func (m *Model) settleChords(b map[string]func() (tea.Model, tea.Cmd), owner map[string]string) {
	m.chordPrefixes = map[string]bool{}
	seqs := make([]string, 0)
	for key := range b {
		if strings.Contains(key, " ") {
			seqs = append(seqs, key)
		}
	}
	// Shortest first, so "n x" is settled as a binding before "n x y" asks to
	// make it a prefix; ties in alphabetical order, so the outcome is fixed.
	sort.Slice(seqs, func(i, j int) bool {
		if a, c := strings.Count(seqs[i], " "), strings.Count(seqs[j], " "); a != c {
			return a < c
		}
		return seqs[i] < seqs[j]
	})
	unbind := func(key string) {
		if _, name, ok := strings.Cut(owner[key], "."); ok && m.actionKeys[name] == key {
			m.actionKeys[name] = ""
		}
		delete(b, key)
		delete(owner, key)
	}
	for _, key := range seqs {
		parts := strings.Fields(key)
		if slices.Contains(parts[1:], "esc") || slices.Contains(parts[1:], "ctrl+c") {
			m.keyConflicts = append(m.keyConflicts, keyConflict{
				key: key, refused: owner[key],
				detail: "esc and ctrl+c cancel a chord, so cannot continue one",
			})
			unbind(key)
			continue
		}
		refused := false
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], " ")
			held, taken := owner[prefix]
			if !taken {
				continue
			}
			if held == "navigation" {
				m.keyConflicts = append(m.keyConflicts, keyConflict{
					key: key, kept: held, refused: owner[key],
					detail: fmt.Sprintf("%q is navigation, so cannot start a chord", prefix),
				})
				unbind(key)
				refused = true
				break
			}
			m.keyConflicts = append(m.keyConflicts, keyConflict{
				key: prefix, kept: owner[key], refused: held,
				detail: fmt.Sprintf("%s never runs: %q starts a chord", held, prefix),
			})
			unbind(prefix)
		}
		if refused {
			continue
		}
		for i := 1; i < len(parts); i++ {
			m.chordPrefixes[strings.Join(parts[:i], " ")] = true
		}
	}
}

// startChord is the first key of a chord: nothing runs yet, and the status
// bar's message makes way for the list of what can follow.
func (m *Model) startChord(prefix string) (tea.Model, tea.Cmd) {
	m.chord = prefix
	m.statusMsg = ""
	return m, nil
}

// continueChord takes the key after a chord's prefix.
func (m *Model) continueChord(s string) (tea.Model, tea.Cmd) {
	prefix := m.chord
	m.chord = ""
	if s == "esc" || s == "ctrl+c" {
		return m, nil
	}
	seq := prefix + " " + s
	if alias, ok := chordAliases[s]; ok && !m.chordKnows(seq) {
		seq = prefix + " " + alias
	}
	if fn, ok := m.bindings[seq]; ok {
		return fn()
	}
	if m.chordPrefixes[seq] {
		return m.startChord(seq)
	}
	return m, m.note(fmt.Sprintf("%s is not bound", keyLabel(prefix+" "+s)), true)
}

// chordKnows reports whether seq means something: a binding, or the start of
// a longer one.
func (m *Model) chordKnows(seq string) bool {
	_, bound := m.bindings[seq]
	return bound || m.chordPrefixes[seq]
}

// chordLabels are the words the status bar gives the keys that can follow
// "n". Anything not named here — a chord of the user's own — is shown by the
// name of what it runs, which is long but never wrong.
var chordLabels = map[string]string{
	"keys.shell-right":     "beside",
	"keys.shell-down":      "below",
	"keys.shell-up":        "above",
	"keys.shell-left":      "left edge",
	"commands.shell-popup": "popup",
}

// chordOrder puts the arrows first, in the order the chord was designed
// around — the common case, then the two that divide the pane, then the
// other side — and everything else after them alphabetically.
var chordOrder = map[string]int{"right": 0, "down": 1, "up": 2, "left": 3}

// chordHints is what can follow the prefix typed so far, for the status bar.
func (m *Model) chordHints() []keyHint {
	depth := strings.Count(m.chord, " ") + 1
	seen := map[string]bool{}
	var next []string
	labels := map[string]string{}
	for key, who := range m.bindingOwner {
		if !strings.HasPrefix(key, m.chord+" ") {
			continue
		}
		parts := strings.Fields(key)
		k := parts[depth]
		if seen[k] {
			continue
		}
		seen[k] = true
		next = append(next, k)
		switch {
		case len(parts) > depth+1:
			labels[k] = "…"
		case chordLabels[who] != "":
			labels[k] = chordLabels[who]
		default:
			_, name, _ := strings.Cut(who, ".")
			labels[k] = name
		}
	}
	sort.Slice(next, func(i, j int) bool {
		oi, iok := chordOrder[next[i]]
		oj, jok := chordOrder[next[j]]
		if iok != jok {
			return iok
		}
		if iok {
			return oi < oj
		}
		return next[i] < next[j]
	})
	hints := make([]keyHint, 0, len(next)+1)
	for _, k := range next {
		label := k
		if g, ok := arrowGlyphs[k]; ok {
			label = g
		}
		hints = append(hints, keyHint{label, labels[k]})
	}
	return append(hints, keyHint{"esc", "cancel"})
}

// shellRightDesc is the help line for shell-right, which is also where the
// page says that hjkl work inside a chord — but only while the key is a chord
// ending in an arrow. Moved to "N", the note would describe nothing.
func (m *Model) shellRightDesc() string {
	const desc = "shell beside the tree"
	parts := strings.Fields(m.actionKeys["shell-right"])
	if len(parts) < 2 {
		return desc
	}
	if _, arrow := arrowGlyphs[parts[len(parts)-1]]; !arrow {
		return desc
	}
	return desc + "; hjkl work as arrows"
}
