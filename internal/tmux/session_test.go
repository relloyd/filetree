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
		{"plain", "filetree", "main", "claude", "ft/agent/filetree/main/claude"},
		// A branch is the component most likely to carry a slash, and
		// flattening it is what keeps the component count fixed.
		{"slash in branch", "filetree", "claude/tmux-nav", "claude", "ft/agent/filetree/claude-tmux-nav/claude"},
		{"nested branch", "filetree", "a/b/c", "copilot", "ft/agent/filetree/a-b-c/copilot"},
		// tmux rewrites "." and ":" to "_" itself; doing it here is what keeps
		// the name we ask for and the name it stores the same string.
		{"dot in branch", "filetree", "release/1.2.0", "claude", "ft/agent/filetree/release-1_2_0/claude"},
		{"dot in repo", "next.js", "main", "claude", "ft/agent/next_js/main/claude"},
		{"colon", "repo", "a:b", "shell", "ft/agent/repo/a_b/shell"},
		{"no tool", "filetree", "main", "", "ft/agent/filetree/main"},
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
		p, ok := ParseName(DefaultPrefix, name)
		if !ok {
			t.Fatalf("ParseName(%q) not ok", name)
		}
		if p.Kind != KindAgent {
			t.Errorf("ParseName(%q) kind = %q, want %q", name, p.Kind, KindAgent)
		}
		if p.Repo != sanitise(c[0]) || p.Tool != sanitise(c[2]) {
			t.Errorf("ParseName(%q) = %q/%q, want %q/%q", name, p.Repo, p.Tool, sanitise(c[0]), sanitise(c[2]))
		}
		// The branch cannot round-trip to its original when it had a slash;
		// what matters is that it survives as the flattened form.
		if want := sanitise(c[1]); p.Branch != want {
			t.Errorf("ParseName(%q) branch = %q, want %q", name, p.Branch, want)
		}
	}
}

// PlaceName is the other half of the grammar: a kind and one path, for
// everything that belongs to a place rather than to a repo and branch.
func TestPlaceNameRoundTrip(t *testing.T) {
	for _, kind := range []string{KindTree, KindShell, KindLazygit, KindBlame, KindDiff} {
		name := PlaceName(DefaultPrefix, kind, "/home/u/filetree")
		p, ok := ParseName(DefaultPrefix, name)
		if !ok {
			t.Fatalf("ParseName(%q) not ok", name)
		}
		if p.Kind != kind {
			t.Errorf("ParseName(%q) kind = %q, want %q", name, p.Kind, kind)
		}
		if p.Slug != Slug("/home/u/filetree") {
			t.Errorf("ParseName(%q) slug = %q", name, p.Slug)
		}
	}
	// Nothing to name is no name, rather than a name every caller shares.
	if got := PlaceName(DefaultPrefix, KindShell, ""); got != "" {
		t.Errorf("PlaceName with no path = %q, want empty", got)
	}
	if got := PlaceName(DefaultPrefix, "", "/tmp"); got != "" {
		t.Errorf("PlaceName with no kind = %q, want empty", got)
	}
}

// A slug is one component, so nothing tmux rewrites may survive in it: "."
// and ":" are the two it silently turns into "_", and storekey allows the dot.
func TestSlug(t *testing.T) {
	for _, path := range []string{"/home/u/next.js", "/home/u/a:b", "/home/u/plain"} {
		got := Slug(path)
		if strings.ContainsAny(got, "./:") {
			t.Errorf("Slug(%q) = %q, still carries a character tmux rewrites", path, got)
		}
		if strings.Contains(got, "/") {
			t.Errorf("Slug(%q) = %q, must be one component", path, got)
		}
	}
	// The hash is what keeps two directories of the same name apart.
	if Slug("/a/web") == Slug("/b/web") {
		t.Error("two directories called web should not share a slug")
	}
	if Slug("") != "" {
		t.Errorf("Slug(\"\") = %q, want empty", Slug(""))
	}
}

// The tree session is the one name that cannot simply be reattached when it
// is taken: a second ft on the same directory is a second tree.
func TestUniqueName(t *testing.T) {
	base := "ft/tree/filetree-1a2b3c4d"
	cases := []struct {
		name  string
		taken []string
		want  string
	}{
		{"free", nil, base},
		{"taken once", []string{base}, base + "-2"},
		{"taken twice", []string{base, base + "-2"}, base + "-3"},
		{"gap is filled", []string{base, base + "-3"}, base + "-2"},
		{"others do not count", []string{"ft/tree/other-9f8e7d6c"}, base},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UniqueName(base, tc.taken); got != tc.want {
				t.Errorf("UniqueName(%q, %v) = %q, want %q", base, tc.taken, got, tc.want)
			}
		})
	}
	if got := UniqueName("", []string{"x"}); got != "" {
		t.Errorf("UniqueName with no base = %q, want empty", got)
	}
}

func TestParseName(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		in     string
		kind   string
		ok     bool
	}{
		{"agent", DefaultPrefix, "ft/agent/filetree/main/claude", KindAgent, true},
		{"tree", DefaultPrefix, "ft/tree/filetree-1a2b3c4d", KindTree, true},
		{"shell", DefaultPrefix, "ft/shell/filetree-1a2b3c4d", KindShell, true},
		{"diff", DefaultPrefix, "ft/diff/main_go-9f8e7d6c", KindDiff, true},
		{"not our prefix", DefaultPrefix, "work/agent/filetree/main/claude", "", false},
		{"bare session", DefaultPrefix, "0", "", false},
		{"kind alone", DefaultPrefix, "ft/tree", "", false},
		{"unknown kind", DefaultPrefix, "ft/pager/main_go-9f8e7d6c", "", false},
		// The shape from before the kind moved to the front. It still lists,
		// it just has nothing but its name to show for itself.
		{"pre-migration agent", DefaultPrefix, "ft/filetree/main/claude", "", false},
		{"agent too few parts", DefaultPrefix, "ft/agent/filetree/main", "", false},
		{"agent too many parts", DefaultPrefix, "ft/agent/filetree/main/claude/extra", "", false},
		{"place too many parts", DefaultPrefix, "ft/shell/filetree/extra", "", false},
		{"empty component", DefaultPrefix, "ft/agent/filetree//claude", "", false},
		// An empty prefix would match every session on the server, including
		// ones ft did not create; it is refused here as well as at config load.
		{"empty prefix", "", "ft/agent/filetree/main/claude", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := ParseName(tc.prefix, tc.in)
			if ok != tc.ok {
				t.Fatalf("ParseName(%q, %q) ok = %v, want %v", tc.prefix, tc.in, ok, tc.ok)
			}
			if p.Kind != tc.kind {
				t.Errorf("ParseName(%q, %q) kind = %q, want %q", tc.prefix, tc.in, p.Kind, tc.kind)
			}
		})
	}
}

// row builds one line of list-sessions output in ListFormat's field order.
func row(fields ...string) string { return strings.Join(fields, "\t") }

func TestParseList(t *testing.T) {
	out := strings.Join([]string{
		row("ft/agent/filetree/main/claude", "1", "1700000000", "2", "", "/home/u/filetree", "claude"),
		row("ft/agent/filetree/feat-x/copilot", "0", "1700000060", "1", "bell", "/home/u/wt/feat-x", "bash"),
		// A place session: one component after the kind, no repo or branch.
		row("ft/shell/filetree-1a2b3c4d", "0", "1700000030", "1", "", "/home/u/filetree", "zsh"),
		// Not ours: a session someone else created.
		row("0", "1", "1700000000", "1", "", "/home/u", "vim"),
		row("work", "0", "1700000000", "1", "", "/home/u", "bash"),
		// Under the prefix but not conventional — listed, but unparsed.
		row("ft/loose", "0", "1700000000", "1", "", "/tmp", "bash"),
		// Malformed: too few fields, skipped rather than fatal.
		"ft/broken\t0",
		"",
	}, "\n")

	got := ParseList(DefaultPrefix, out)
	if len(got) != 4 {
		t.Fatalf("got %d sessions, want 4: %+v", len(got), got)
	}

	first := got[0]
	if first.Name != "ft/agent/filetree/main/claude" {
		t.Errorf("name = %q", first.Name)
	}
	if first.Kind != KindAgent || first.Repo != "filetree" || first.Branch != "main" || first.Tool != "claude" {
		t.Errorf("parts = %q %q/%q/%q", first.Kind, first.Repo, first.Branch, first.Tool)
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
	// The kind leads the label, which is what the picker's query narrows on.
	if first.Label(DefaultPrefix) != "agent/filetree/main/claude" {
		t.Errorf("label = %q", first.Label(DefaultPrefix))
	}

	if !got[1].Alert {
		t.Error("a bell in session_alerts should set Alert")
	}
	if s := got[2]; s.Kind != KindShell || s.Slug != "filetree-1a2b3c4d" || s.Repo != "" {
		t.Errorf("place session parsed as %+v", s)
	}
	if got[3].Name != "ft/loose" || got[3].Parsed() {
		t.Errorf("unconventional name should be listed unparsed, got %+v", got[3])
	}
}

// The format string and the parser are two halves of one agreement; a field
// added to one and not the other silently shifts every column after it.
func TestListFormatMatchesParser(t *testing.T) {
	if n := len(strings.Split(ListFormat, "\t")); n != len(listFields) {
		t.Fatalf("ListFormat has %d fields, listFields has %d", n, len(listFields))
	}
	line := row("ft/agent/r/b/t", "0", "1", "1", "", "/tmp", "sh")
	if got := ParseList(DefaultPrefix, line); len(got) != 1 {
		t.Fatalf("a line with len(listFields) fields must parse, got %d rows", len(got))
	}
}

func TestTargetIsExact(t *testing.T) {
	// Without the "=", tmux treats a -t name as a prefix and would let
	// "ft/agent/r/main/claude" match "ft/agent/r/main/claude-2".
	if got := target("ft/agent/r/main/claude"); got != "=ft/agent/r/main/claude" {
		t.Errorf("target() = %q", got)
	}
}

// The "=" target must reach tmux quoted. tmux runs popup commands through
// default-shell, and on a stock macOS that is zsh, which expands a word
// beginning with "=" to the path of the command it names — silently turning
// the exact-match target into something else and failing the attach. The bug
// this guards against was invisible on Linux, where no common shell does it.
func TestAttachPopupQuotesTheTarget(t *testing.T) {
	quote := func(s string) string { return "'" + s + "'" }
	for _, got := range []string{
		AttachPopup("ft/agent/repo/main/claude", quote),
		AttachCommand("/tmp/tmux-501/default", "ft/agent/repo/main/claude", quote),
	} {
		if !strings.Contains(got, "'=ft/agent/repo/main/claude'") {
			t.Errorf("target reaches the shell unquoted: %s", got)
		}
	}
}
