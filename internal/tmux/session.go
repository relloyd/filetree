package tmux

import (
	"strconv"
	"strings"
	"time"
)

// DefaultPrefix marks the sessions this feature owns. Everything ft creates
// for itself — the self-relaunch in exec.go, the splits and popups in the
// starter config — stays unnamed, so the prefix is what separates "a session I
// parked an agent in" from "a pane that happens to exist".
const DefaultPrefix = "ft/"

// A named session is <prefix><repo>/<branch>/<tool>: the repo and branch of
// the selection when it was launched, and the tool running in it. Three
// components after the prefix, always, which is what lets ParseName take a
// name back apart for display.
const nameParts = 3

// Session is one tmux session carrying the prefix, as listed by List.
//
// Repo, Branch and Tool are filled in only when the name parses; a session
// someone created by hand under the prefix keeps its Name and leaves them
// empty rather than being hidden.
type Session struct {
	Name   string
	Repo   string
	Branch string
	Tool   string

	Dir      string    // #{session_path}: where the session was created
	Command  string    // #{pane_current_command} of the active pane
	Attached int       // how many clients have it open
	Windows  int       //
	Activity time.Time // #{session_activity}: last time it did anything
	Alert    bool      // a bell is pending — the tool wants attention
}

// Parsed reports whether the name followed the convention.
func (s Session) Parsed() bool { return s.Repo != "" }

// Label is the name with the prefix taken off, which is the same for every row
// and so says nothing. An unparsed name is shown whole.
func (s Session) Label(prefix string) string {
	return strings.TrimPrefix(s.Name, prefix)
}

// SessionName builds the session name for a repo, branch and tool.
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
	parts := []string{sanitise(repo), sanitise(branch)}
	if tool != "" {
		parts = append(parts, sanitise(tool))
	}
	return prefix + strings.Join(parts, "/")
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

// ParseName splits a session name back into its parts. It reports false for
// anything not under the prefix, and for a name under the prefix that does not
// have the three components — the caller still lists those, it just has
// nothing but the name to show for them.
func ParseName(prefix, name string) (repo, branch, tool string, ok bool) {
	if prefix == "" || !strings.HasPrefix(name, prefix) {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(name, prefix), "/")
	if len(parts) != nameParts {
		return "", "", "", false
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", false
		}
	}
	return parts[0], parts[1], parts[2], true
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
		s.Repo, s.Branch, s.Tool, _ = ParseName(prefix, s.Name)
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
