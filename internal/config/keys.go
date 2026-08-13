package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultActionKeys is every configurable action and the key it answers to out
// of the box. [keys] in the config overrides an entry by name; a name absent
// from here and from the command catalogue is not bindable, and the resolver
// says so rather than letting the line sit there doing nothing.
//
// An empty default means "unbound unless [keys] gives it a key" — reload is the
// only one, since F5 covers it from the fixed navigation set.
//
// It lives here rather than beside the resolver in internal/app because the
// starter config generates its [keys] documentation from it, and this package
// cannot import that one.
var DefaultActionKeys = map[string]string{
	"quit":           "q",
	"toggle-hidden":  ".",
	"toggle-ignored": "i",
	"reload":         "",
	"reveal":         "o",
	"copy-abs":       "y",
	"copy-rel":       "Y",
	"fuzzy":          "/",
	"fuzzy-here":     "F",
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
	"recent":        "b",
	"bookmarks":     "B",
	"tmux-sessions": "T",
	"new-file":      "a",
	"new-dir":       "A",
	"rename":        "R",
	"delete":        "d",
	"collapse-all":  "H",
	"edit-config":   "C",
	"reload-config": "alt+c",
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

// actionNotes are the trailing comments the generated [keys] block carries,
// for the actions whose name does not say enough on its own. Commands need no
// entry here: the catalogue already describes each one.
var actionNotes = map[string]string{
	"reload":              `unbound: F5 always reloads; set a key to add one`,
	"fuzzy-here":          `the finder, confined to the selected dir`,
	"finder-next-field":   `move between the "/" finder's input lines`,
	"finder-more":         `raise fuzzy_max_matches for this session`,
	"finder-copy-command": `copy the rg command behind Type/Grep`,
	"finder-clear":        `empty the finder fields`,
	"finder-resume":       `reopen the finder where you left it`,
	"recent":              `the finder over recently opened files`,
	"bookmarks":           `the finder over this repo's line bookmarks`,
	"tmux-sessions":       `the named agent sessions; enter reattaches`,
	"edit-config":         `opens it in the default command`,
	"reload-config":       `re-read it after editing somewhere ft cannot see`,
}

// Starter is the file written to ~/.filetree/config.toml on first run: the
// static preamble plus a commented [keys] block naming everything that can be
// rebound.
//
// The block is generated rather than written out by hand. It used to be a
// literal inside starterTOML, which meant the one list a user reads to learn
// an action's name had to be kept in step, by hand, with a table in another
// package — and a name missing from it was a binding nobody could find. Now
// anything added to DefaultActionKeys or to the catalogue appears here for
// free, and the commands are listed too, which the hand-written block never
// managed at all.
func Starter() string {
	return starterTOML + keysBlock()
}

// keysBlock renders the commented [keys] reference. Everything in it is a
// comment, so it changes nothing about how the file parses.
func keysBlock() string {
	var b strings.Builder
	b.WriteString("\n# Every name below can be given a different key. Uncomment [keys] and the\n")
	b.WriteString("# lines you want to change; the values shown are the defaults.\n#\n")
	b.WriteString("# [keys]\n")

	names := make([]string, 0, len(DefaultActionKeys))
	for name := range DefaultActionKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	nameW, keyW := columns(names, DefaultActionKeys)
	for _, name := range names {
		b.WriteString(keyLine(name, DefaultActionKeys[name], actionNotes[name], nameW, keyW))
	}

	b.WriteString("#\n# Commands — the same table, the same rule:\n")
	cmdNames := make([]string, 0, len(Builtin))
	notes := make(map[string]string, len(Builtin))
	keys := make(map[string]string, len(Builtin))
	for _, c := range Builtin {
		cmdNames = append(cmdNames, c.Name)
		notes[c.Name], keys[c.Name] = c.Desc, c.Key
	}
	sort.Strings(cmdNames)
	nameW, keyW = columns(cmdNames, keys)
	for _, name := range cmdNames {
		b.WriteString(keyLine(name, keys[name], notes[name], nameW, keyW))
	}
	return b.String()
}

// keyLine is one commented `name = "key"` with its note aligned past the
// widest of both columns, so the notes read down the page as a column rather
// than a ragged edge.
func keyLine(name, key, note string, nameW, keyW int) string {
	line := fmt.Sprintf("# %-*s = %-*s", nameW, name, keyW, strconv.Quote(key))
	if note == "" {
		return strings.TrimRight(line, " ") + "\n"
	}
	return line + "  # " + note + "\n"
}

// columns measures the two fields so both can be padded to a fixed width.
func columns(names []string, keys map[string]string) (nameW, keyW int) {
	for _, name := range names {
		nameW = max(nameW, len(name))
		keyW = max(keyW, len(strconv.Quote(keys[name])))
	}
	return nameW, keyW
}
