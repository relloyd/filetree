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

// grep-here types a shell command, so it must only ever reach a shell. Aimed
// at the previously-active pane it fed "rg -n ... -e " to a helix sitting
// there as editor keystrokes, and left the buffer modified.
func TestGrepHereNeverTypesIntoAnEditor(t *testing.T) {
	got := runHandoff(t, "grep-here", "0 %0 ft\n1 %1 hx\n", handoffVars)
	if strings.Contains(got, "send-keys") {
		t.Errorf("typed into a non-shell pane:\n%s", got)
	}
	if !strings.Contains(got, "split-window") {
		t.Errorf("want a fresh shell split instead:\n%s", got)
	}
}

func TestGrepHereUsesAShellWhenThereIsOne(t *testing.T) {
	got := runHandoff(t, "grep-here", "0 %0 ft\n0 %1 hx\n0 %2 bash\n", handoffVars)
	if !strings.Contains(got, "-t %2") {
		t.Errorf("did not target the shell:\n%s", got)
	}
	if !strings.Contains(got, "rg -n") {
		t.Errorf("did not prime the rg:\n%s", got)
	}
	if strings.Contains(got, "split-window") {
		t.Errorf("split despite a shell being available:\n%s", got)
	}
}

// Outside tmux these resolve against the most recently used session, so a
// send-keys would type into a pane in a window nobody is looking at. They say
// so rather than acting, and the status bar reports it.
func TestHandoffsRefuseOutsideTmux(t *testing.T) {
	for _, name := range []string{"tmux-handoff", "grep-here"} {
		t.Run(name, func(t *testing.T) {
			dir, _ := stubTmux(t, "0 %0 ft\n")
			cmd := exec.Command("/bin/sh", "-c", ExpandCommand(Default().Commands[name].Run, handoffVars))
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
		})
	}
}
