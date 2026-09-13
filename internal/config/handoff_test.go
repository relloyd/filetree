package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubTmux puts a fake tmux on PATH that answers list-panes from a fixture and
// records every other subcommand, so the hand-off scripts can be run for real
// through /bin/sh without a tmux server.
//
// The scripts are the part of the catalogue with actual logic in them — a
// pane-picking pass whose last bug shipped — and they are shell, so the only
// honest way to test them is to run them.
func stubTmux(t *testing.T, panes string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = list-panes ]; then\n" +
		"  printf '%s' " + shq(panes) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf '%s\\n' \"$*\" >> " + shq(log) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runHandoff expands a catalogue command and runs it against the stub.
func runHandoff(t *testing.T, name, panes string, v Vars) string {
	t.Helper()
	c, ok := Default().Commands[name]
	if !ok {
		t.Fatalf("%s is not in the catalogue", name)
	}
	dir, log := stubTmux(t, panes)
	cmd := exec.Command("/bin/sh", "-c", ExpandCommand(c.Run, v))
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMUX=/tmp/fake,1,0", "TMUX_PANE=%0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v\n%s", name, err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		return "" // nothing was called beyond list-panes
	}
	return string(b)
}

var handoffVars = Vars{Path: "/repo/a.go", Paths: []string{"/repo/a.go"}, Dir: "/repo/sub", Root: "/repo"}

// The reported bug: ft opens helix with -d, so that pane never becomes active
// and never becomes "last" either. Targeting the last pane found ft's own,
// whose command is "ft", and split again — one new helix per press.
func TestHandoffReusesAHelixThatWasNeverFocused(t *testing.T) {
	got := runHandoff(t, "tmux-handoff", "0 %0 ft\n0 %1 hx\n", handoffVars)
	if !strings.Contains(got, ":open") {
		t.Errorf("did not open in the existing helix:\n%s", got)
	}
	if strings.Contains(got, "split-window") {
		t.Errorf("split instead of reusing the helix:\n%s", got)
	}
	if !strings.Contains(got, "-t %1") {
		t.Errorf("did not target the helix pane:\n%s", got)
	}
}

func TestHandoffTargeting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		panes  string
		want   string
		reject string
	}{
		{"nothing to hand off to splits", "0 %0 ft\n", "split-window", ":open"},
		{"a waiting shell is typed into", "0 %0 ft\n0 %1 zsh\n", "hx ", "split-window"},
		{"helix beats a shell", "0 %0 ft\n0 %1 zsh\n0 %2 hx\n", "-t %2", "split-window"},
		{"helix beats a last-active shell", "0 %0 ft\n1 %1 zsh\n0 %2 hx\n", "-t %2", "split-window"},
		{"the last-active helix wins", "0 %0 ft\n0 %1 hx\n1 %2 hx\n", "-t %2", "split-window"},
		{"unusable panes are ignored", "0 %0 ft\n0 %1 tail\n0 %2 htop\n", "split-window", ":open"},
		// ft's own pane is never a target, whatever it appears to be running:
		// send-keys aimed at it types the command into the tree as keystrokes.
		{"never targets ft itself", "1 %0 zsh\n", "split-window", "-t %0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runHandoff(t, "tmux-handoff", tc.panes, handoffVars)
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, got)
			}
			if tc.reject != "" && strings.Contains(got, tc.reject) {
				t.Errorf("did not want %q in:\n%s", tc.reject, got)
			}
		})
	}
}

// Outside tmux the hand-off resolves against the most recently used session,
// so a send-keys would type into a pane in a window nobody is looking at. It
// says so rather than acting, and the status bar reports it.
func TestHandoffRefusesOutsideTmux(t *testing.T) {
	dir, _ := stubTmux(t, "0 %0 ft\n")
	cmd := exec.Command("/bin/sh", "-c", ExpandCommand(Default().Commands["tmux-handoff"].Run, handoffVars))
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for i, e := range cmd.Env {
		if strings.HasPrefix(e, "TMUX=") {
			cmd.Env[i] = "TMUX="
		}
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("succeeded outside tmux:\n%s", out)
	}
	if !strings.Contains(string(out), "not running inside tmux") {
		t.Errorf("said nothing useful: %q", out)
	}
}

// stubFT puts a fake ft on PATH that records its arguments one per line, so a
// catalogue command calling a subcommand can be run for real through /bin/sh.
//
// One argument per line is the whole point: it is what distinguishes three
// marked files arriving as three arguments from arriving as one string with
// spaces in it, which is the way this can quietly break.
func stubFT(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " + shq(log) + "; done\n"
	if err := os.WriteFile(filepath.Join(dir, "ft"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// runFTCommand expands a catalogue command and runs it against the stub ft,
// returning the arguments it was handed.
func runFTCommand(t *testing.T, name string, v Vars) []string {
	t.Helper()
	c, ok := Default().Commands[name]
	if !ok {
		t.Fatalf("%s is not in the catalogue", name)
	}
	dir, log := stubFT(t)
	cmd := exec.Command("/bin/sh", "-c", ExpandCommand(c.Run, v))
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v\n%s", name, err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("%s called nothing", name)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// The herdr editor key has to carry a marked set the way "e" and "t" do: several
// files reaching helix as several arguments, not as one string with spaces in
// it. {paths} is what makes that work, and the quoting is what keeps a path with
// a space in it from arriving as two.
func TestHerdrEditCarriesMarkedFiles(t *testing.T) {
	marks := []string{"/repo/one.go", "/repo/sub/two.go", "/repo/my file.go"}
	got := runFTCommand(t, "herdr-edit", Vars{
		Path:   "/repo/one.go",
		Paths:  marks,
		Marked: marks,
		Dir:    "/repo/sub",
		Root:   "/repo",
	})

	want := append([]string{"herdr-edit", "/repo/sub"}, marks...)
	if len(got) != len(want) {
		t.Fatalf("ft got %d arguments %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argument %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// With nothing marked it still gets the selection, so the key works the same
// whether or not marks are set — the rule {paths} applies everywhere.
func TestHerdrEditFallsBackToTheSelection(t *testing.T) {
	got := runFTCommand(t, "herdr-edit", Vars{
		Path: "/repo/one.go",
		Dir:  "/repo",
		Root: "/repo",
	})
	want := []string{"herdr-edit", "/repo", "/repo/one.go"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("ft got %q, want %q", got, want)
	}
}

// A Grep hit carries its line, and a lone target is the only case that can:
// ":42" on the end of a list would attach to the last path alone.
func TestHerdrEditCarriesAGrepLine(t *testing.T) {
	got := runFTCommand(t, "herdr-edit", Vars{
		Path: "/repo/one.go",
		Line: 42,
		Dir:  "/repo",
		Root: "/repo",
	})
	want := []string{"herdr-edit", "/repo", "/repo/one.go:42"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("ft got %q, want %q", got, want)
	}
}

// The marks must be consumed the same way "e" and "t" consume them, so
// clear_marks_after_command behaves consistently across the editor keys.
func TestHerdrEditUsesMarks(t *testing.T) {
	c := Default().Commands["herdr-edit"]
	if !UsesMarks(c.Run) {
		t.Error("UsesMarks = false; the key would not consume the marks it acted on")
	}
}

// The blame key needs both the directory and the file, and they are different
// arguments: the directory decides which workspace answers, the file decides
// what to show. On a finder row they can be in different checkouts entirely.
func TestHerdrBlameCarriesDirAndFile(t *testing.T) {
	got := runFTCommand(t, "herdr-blame", Vars{
		Path: "/repo/internal/my file.go",
		Dir:  "/repo/internal",
		Root: "/repo",
	})
	want := []string{"herdr-blame", "/repo/internal", "/repo/internal/my file.go"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("ft got %q, want %q", got, want)
	}
}

// It acts on the cursor, never on the marked set: one file has one history, and
// {paths} would hand it several.
func TestHerdrBlameIgnoresMarks(t *testing.T) {
	c := Default().Commands["herdr-blame"]
	if UsesMarks(c.Run) {
		t.Error("UsesMarks = true; blame shows one file, so marks would be meaningless")
	}
	got := runFTCommand(t, "herdr-blame", Vars{
		Path:   "/repo/one.go",
		Paths:  []string{"/repo/one.go", "/repo/two.go"},
		Marked: []string{"/repo/one.go", "/repo/two.go"},
		Dir:    "/repo",
		Root:   "/repo",
	})
	want := []string{"herdr-blame", "/repo", "/repo/one.go"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("ft got %q, want %q — the marks should not reach it", got, want)
	}
}
