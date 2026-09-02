package tmux

import (
	"strconv"
	"strings"
	"time"

	"github.com/relloyd/filetree/internal/storekey"
)

// DefaultPrefix marks the sessions this feature owns. Everything ft creates
// carries it — the self-relaunch in exec.go and every popup in the catalogue
// as well as the agent sessions — so the prefix is what separates "a session
// ft opened" from "a session that happens to exist".
const DefaultPrefix = "ft/"

// A name is <prefix><kind>/<rest>, and the kind says how to read the rest.
// Kind first, always: it is what lets a name be understood without counting
// its slashes, and it is what the picker's query narrows on.
const (
	KindAgent   = "agent"   // <repo>/<branch>/<tool>, from SessionName
	KindTree    = "tree"    // ft itself, from PlaceName over the tree root
	KindShell   = "shell"   // the shell popup, keyed by directory
	KindLazygit = "lazygit" // the lazygit popup, keyed by checkout
	KindBlame   = "blame"   // lazygit's blame view, keyed by file
	KindDiff    = "diff"    // the diff pager, keyed by file
)

// kindParts is how many components follow the kind, per kind. A name with the
// wrong number for its kind does not parse — better an unparsed row than one
// showing a branch that is really a directory.
var kindParts = map[string]int{
	KindAgent:   3,
	KindTree:    1,
	KindShell:   1,
	KindLazygit: 1,
	KindBlame:   1,
	KindDiff:    1,
}

// Session is one tmux session carrying the prefix, as listed by List.
//
// Kind and the fields it explains are filled in only when the name parses; a
// session someone created by hand under the prefix — or one left over from
// before the naming convention changed — keeps its Name and leaves them empty
// rather than being hidden.
type Session struct {
	Name   string
	Kind   string
	Repo   string // KindAgent
	Branch string // KindAgent
	Tool   string // KindAgent
	Slug   string // every other kind: "<basename>-<hash>"

	Dir      string    // #{session_path}: where the session was created
	Command  string    // #{pane_current_command} of the active pane
	Attached int       // how many clients have it open
	Windows  int       //
	Activity time.Time // #{session_activity}: last time it did anything
	Alert    bool      // a bell is pending — the tool wants attention
}

// Parsed reports whether the name followed the convention.
func (s Session) Parsed() bool { return s.Kind != "" }

// Label is the name with the prefix taken off, which is the same for every row
// and so says nothing. An unparsed name is shown whole.
func (s Session) Label(prefix string) string {
	return strings.TrimPrefix(s.Name, prefix)
}

// SessionName builds the agent session name for a repo, branch and tool. An
// empty tool leaves the stem, which is what {session} expands to so the
// catalogue can append a tool of its own.
//
// Each component is sanitised, because tmux does not store the name it is
// given: "." and ":" are silently rewritten to "_" (verified on tmux 3.4), so
// a name built with either in it would come back from list-sessions looking
// different, and `new-session -A` could no longer find what it created. Doing
// the rewrite here means what we ask for is what tmux stores.
func SessionName(prefix, repo, branch, tool string) string {
	if repo == "" || branch == "" {
		return ""
	}
	parts := []string{KindAgent, sanitise(repo), sanitise(branch)}
	if tool != "" {
		parts = append(parts, sanitise(tool))
	}
	return prefix + strings.Join(parts, "/")
}

// PlaceName is the session name for everything that belongs to a *place*
// rather than to a repo and branch: the tree itself, a shell, a popup. The
// path decides which place — the tree root, the selection's directory, the
// file being diffed — and the caller decides which of those is the identity
// worth reattaching to.
func PlaceName(prefix, kind, path string) string {
	if kind == "" || path == "" {
		return ""
	}
	return prefix + kind + "/" + Slug(path)
}

// Slug names a path in one component: its basename, plus a hash of the whole
// path so two directories called "web" are two sessions and not one.
//
// storekey is the same rule the state and bookmark files are named by, so a
// session and the files that go with it read alike. Its output still goes
// through sanitise: storekey allows "." in a basename and tmux does not.
func Slug(path string) string {
	if path == "" {
		return ""
	}
	return sanitise(storekey.Name(path, ""))
}

// UniqueName is base, or base with the lowest free "-2", "-3" on the end.
//
// It exists for the tree session, which is the one name ft cannot simply
// reattach to when it is taken: a second ft on the same directory is a second
// tree, not a second view of the first.
func UniqueName(base string, taken []string) string {
	if base == "" {
		return ""
	}
	used := make(map[string]bool, len(taken))
	for _, t := range taken {
		used[t] = true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		if c := base + "-" + strconv.Itoa(n); !used[c] {
			return c
		}
	}
}

// sanitise makes one component safe to put in a tmux session name.
//
// "/" is the separator here, so a branch like "claude/tmux-nav" is flattened
// to "claude-tmux-nav" — the same rule gitx.WorktreeDirName applies to the
// worktree directory, and for the same reason: keeping the component count
// fixed is what makes the name parseable again. "." and ":" are tmux's own
// restriction rather than ours.
func sanitise(s string) string {
	return strings.NewReplacer(
		"/", "-",
		`\`, "-",
		".", "_",
		":", "_",
	).Replace(s)
}

// Parts is a session name taken apart. Which fields carry anything depends on
// Kind, and Kind is empty for a name that did not parse.
type Parts struct {
	Kind   string
	Repo   string
	Branch string
	Tool   string
	Slug   string
}

// ParseName splits a session name back into its parts. It reports false for
// anything not under the prefix, for a kind ft does not know, and for a name
// whose component count does not match its kind — a name from before the
// convention changed lands here, and the caller still lists it, it just has
// nothing but the name to show for it.
func ParseName(prefix, name string) (Parts, bool) {
	if prefix == "" || !strings.HasPrefix(name, prefix) {
		return Parts{}, false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "/")
	if len(parts) < 2 {
		return Parts{}, false
	}
	kind := parts[0]
	rest := parts[1:]
	want, known := kindParts[kind]
	if !known || len(rest) != want {
		return Parts{}, false
	}
	for _, p := range rest {
		if p == "" {
			return Parts{}, false
		}
	}
	if kind == KindAgent {
		return Parts{Kind: kind, Repo: rest[0], Branch: rest[1], Tool: rest[2]}, true
	}
	return Parts{Kind: kind, Slug: rest[0]}, true
}

// listFields are the session properties List asks tmux for, in order. They are
// joined with tabs: a tab cannot reach a session name (git refuses control
// characters in a branch name, and the other components are path basenames),
// so no field can be split in two by its own contents.
var listFields = []string{
	"#{session_name}",
	"#{session_attached}",
	"#{session_activity}",
	"#{session_windows}",
	"#{session_alerts}",
	"#{session_path}",
	"#{pane_current_command}",
}

// ListFormat is the -F argument for list-sessions.
var ListFormat = strings.Join(listFields, "\t")

// ParseList turns list-sessions output into the sessions carrying the prefix.
// Anything else on the server — ft's own wrapper session, the anonymous
// sessions behind the splits and popups, whatever else the user is running —
// is dropped here, which is the whole point of the naming convention.
//
// A malformed line is skipped rather than failing the list: one unreadable
// session should not hide the rest.
func ParseList(prefix, out string) []Session {
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != len(listFields) {
			continue
		}
		if prefix == "" || !strings.HasPrefix(f[0], prefix) {
			continue
		}
		s := Session{
			Name:     f[0],
			Attached: atoi(f[1]),
			Windows:  atoi(f[3]),
			// session_alerts is empty when nothing is pending and lists the
			// alerts otherwise ("bell", "activity", ...). Claude Code rings the
			// terminal bell when it wants input, which is what makes this the
			// "waiting on you" marker.
			Alert:   strings.Contains(f[4], "bell"),
			Dir:     f[5],
			Command: f[6],
		}
		if secs := atoi(f[2]); secs > 0 {
			s.Activity = time.Unix(int64(secs), 0)
		}
		p, _ := ParseName(prefix, s.Name)
		s.Kind, s.Repo, s.Branch, s.Tool, s.Slug = p.Kind, p.Repo, p.Branch, p.Tool, p.Slug
		sessions = append(sessions, s)
	}
	return sessions
}

// atoi is Atoi with a zero for anything unreadable: a count or a timestamp
// tmux did not fill in is missing information, not a reason to drop the row.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
