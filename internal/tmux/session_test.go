package tmux

import (
	"strings"
	"testing"
	"time"
)

func TestSessionName(t *testing.T) {
	cases := []struct {
		name               string
		repo, branch, tool string
		want               string
	}{
		{"plain", "filetree", "main", "claude", "ft/filetree/main/claude"},
		// A branch is the component most likely to carry a slash, and
		// flattening it is what keeps the name at three parts.
		{"slash in branch", "filetree", "claude/tmux-nav", "claude", "ft/filetree/claude-tmux-nav/claude"},
		{"nested branch", "filetree", "a/b/c", "copilot", "ft/filetree/a-b-c/copilot"},
		// tmux rewrites "." and ":" to "_" itself; doing it here is what keeps
		// the name we ask for and the name it stores the same string.
		{"dot in branch", "filetree", "release/1.2.0", "claude", "ft/filetree/release-1_2_0/claude"},
		{"dot in repo", "next.js", "main", "claude", "ft/next_js/main/claude"},
		{"colon", "repo", "a:b", "shell", "ft/repo/a_b/shell"},
		{"no tool", "filetree", "main", "", "ft/filetree/main"},
		{"no repo", "", "main", "claude", ""},
		{"no branch", "filetree", "", "claude", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionName(DefaultPrefix, tc.repo, tc.branch, tc.tool); got != tc.want {
				t.Errorf("SessionName(%q, %q, %q) = %q, want %q", tc.repo, tc.branch, tc.tool, got, tc.want)
			}
		})
	}
}

// A name built by SessionName must come back out of ParseName unchanged,
// because the picker shows the parts rather than the raw string.
func TestSessionNameRoundTrip(t *testing.T) {
	cases := [][3]string{
		{"filetree", "main", "claude"},
		{"filetree", "claude/tmux-nav", "copilot"},
		{"next.js", "release/1.2.0", "shell"},
	}
	for _, c := range cases {
		name := SessionName(DefaultPrefix, c[0], c[1], c[2])
		repo, branch, tool, ok := ParseName(DefaultPrefix, name)
		if !ok {
			t.Fatalf("ParseName(%q) not ok", name)
		}
		if repo != sanitise(c[0]) || tool != sanitise(c[2]) {
			t.Errorf("ParseName(%q) = %q/%q, want %q/%q", name, repo, tool, sanitise(c[0]), sanitise(c[2]))
		}
		// The branch cannot round-trip to its original when it had a slash;
		// what matters is that it survives as the flattened form.
		if want := sanitise(c[1]); branch != want {
			t.Errorf("ParseName(%q) branch = %q, want %q", name, branch, want)
		}
	}
}

func TestParseName(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		in     string
		ok     bool
	}{
		{"conventional", DefaultPrefix, "ft/filetree/main/claude", true},
		{"not our prefix", DefaultPrefix, "work/filetree/main/claude", false},
		{"bare session", DefaultPrefix, "0", false},
		{"too few parts", DefaultPrefix, "ft/filetree/main", false},
		{"too many parts", DefaultPrefix, "ft/filetree/main/claude/extra", false},
		{"empty component", DefaultPrefix, "ft/filetree//claude", false},
		// An empty prefix would match every session on the server, including
		// ft's own; it is refused here as well as at config load.
		{"empty prefix", "", "ft/filetree/main/claude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := ParseName(tc.prefix, tc.in); ok != tc.ok {
				t.Errorf("ParseName(%q, %q) ok = %v, want %v", tc.prefix, tc.in, ok, tc.ok)
			}
		})
	}
}

// row builds one line of list-sessions output in ListFormat's field order.
func row(fields ...string) string { return strings.Join(fields, "\t") }

func TestParseList(t *testing.T) {
	out := strings.Join([]string{
		row("ft/filetree/main/claude", "1", "1700000000", "2", "", "/home/u/filetree", "claude"),
		row("ft/filetree/feat-x/copilot", "0", "1700000060", "1", "bell", "/home/u/wt/feat-x", "bash"),
		// Not ours: ft's own wrapper session and a hand-made one.
		row("0", "1", "1700000000", "1", "", "/home/u", "ft"),
		row("work", "0", "1700000000", "1", "", "/home/u", "bash"),
		// Under the prefix but not conventional — listed, but unparsed.
		row("ft/loose", "0", "1700000000", "1", "", "/tmp", "bash"),
		// Malformed: too few fields, skipped rather than fatal.
		"ft/broken\t0",
		"",
	}, "\n")

	got := ParseList(DefaultPrefix, out)
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3: %+v", len(got), got)
	}

	first := got[0]
	if first.Name != "ft/filetree/main/claude" {
		t.Errorf("name = %q", first.Name)
	}
	if first.Repo != "filetree" || first.Branch != "main" || first.Tool != "claude" {
		t.Errorf("parts = %q/%q/%q", first.Repo, first.Branch, first.Tool)
	}
	if !first.Parsed() {
		t.Error("conventional name should report Parsed")
	}
	if first.Attached != 1 || first.Windows != 2 {
		t.Errorf("attached/windows = %d/%d, want 1/2", first.Attached, first.Windows)
	}
	if first.Alert {
		t.Error("no alerts field should not set Alert")
	}
	if !first.Activity.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("activity = %v", first.Activity)
	}
	if first.Dir != "/home/u/filetree" || first.Command != "claude" {
		t.Errorf("dir/command = %q/%q", first.Dir, first.Command)
	}
	if first.Label(DefaultPrefix) != "filetree/main/claude" {
		t.Errorf("label = %q", first.Label(DefaultPrefix))
	}

	if !got[1].Alert {
		t.Error("a bell in session_alerts should set Alert")
	}
	if got[2].Name != "ft/loose" || got[2].Parsed() {
		t.Errorf("unconventional name should be listed unparsed, got %+v", got[2])
	}
}

// The format string and the parser are two halves of one agreement; a field
// added to one and not the other silently shifts every column after it.
func TestListFormatMatchesParser(t *testing.T) {
	if n := len(strings.Split(ListFormat, "\t")); n != len(listFields) {
		t.Fatalf("ListFormat has %d fields, listFields has %d", n, len(listFields))
	}
	line := row("ft/r/b/t", "0", "1", "1", "", "/tmp", "sh")
	if got := ParseList(DefaultPrefix, line); len(got) != 1 {
		t.Fatalf("a line with len(listFields) fields must parse, got %d rows", len(got))
	}
}

func TestTargetIsExact(t *testing.T) {
	// Without the "=", tmux treats a -t name as a prefix and would let
	// "ft/r/main/claude" match "ft/r/main/claude-2".
	if got := target("ft/r/main/claude"); got != "=ft/r/main/claude" {
		t.Errorf("target() = %q", got)
	}
}
