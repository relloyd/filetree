package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/relloyd/filetree/internal/herdr"
)

// editors has to find helix wherever it is in the pane's process list, and has
// to pass over everything else in the same checkout.
func TestRunningFindsHelixAndNothingElse(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftedit")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	repo := repoAt(t, filepath.Join(base, "proj"))

	panes := []map[string]any{
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo}, // idle shell
		{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": repo}, // helix
		{"pane_id": "w1:p3", "tab_id": "w1:t3", "workspace_id": "w1", "cwd": repo, "agent": "claude"},
		{"pane_id": "w1:p4", "tab_id": "w1:t4", "workspace_id": "w1", "cwd": repo}, // something else
	}
	procs := map[string][]map[string]any{
		"w1:p1": {{"name": "zsh", "argv0": "zsh", "pid": 100}},
		"w1:p2": {{"name": "hx", "argv0": "hx", "pid": 200}},
		"w1:p4": {{"name": "node", "argv0": "node", "pid": 400}},
	}
	sock, _ := serveHerdrProcs(t, panes, map[string]bool{"w1:p1": true}, procs)

	c := herdr.New(sock)
	all, _ := c.Panes()
	scoped := inScope(all, herdr.ScopeFor(repo, repo), "", "")

	got := shellIDs(running(c, scoped, helix.matches))
	if !equal(got, []string{"w1:p2"}) {
		t.Errorf("running(hx) = %v, want just the helix pane", got)
	}
}

// A wrapper listed ahead of the real program must not hide it, and a program
// that renames itself must still be found. Both come from a real herdr reply.
func TestRunningSeesPastAWrapper(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftedit")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	repo := repoAt(t, filepath.Join(base, "proj"))

	panes := []map[string]any{
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo},
	}
	procs := map[string][]map[string]any{
		"w1:p1": {
			{"name": "caffeinate", "argv0": "caffeinate", "pid": 10},
			{"name": "25.07.1", "argv0": "hx", "pid": 20},
		},
	}
	sock, _ := serveHerdrProcs(t, panes, nil, procs)

	c := herdr.New(sock)
	all, _ := c.Panes()
	scoped := inScope(all, herdr.ScopeFor(repo, repo), "", "")
	if got := shellIDs(running(c, scoped, helix.matches)); !equal(got, []string{"w1:p1"}) {
		t.Errorf("running(hx) = %v, want the pane despite the wrapper and the rename", got)
	}
}

func TestQuotedAndCommand(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"ordinary paths stay bare, which helix's own parser prefers", []string{"/a/one.go", "/a/two.go"}, "/a/one.go /a/two.go"},
		{"a grep hit keeps its line", []string{"/a/one.go:42"}, "/a/one.go:42"},
		{"a space forces quoting", []string{"/a/my file.go"}, `'/a/my file.go'`},
		{"nothing at all", nil, ""},
	}
	for _, tc := range tests {
		if got := quoted(tc.paths); got != tc.want {
			t.Errorf("%s: quoted = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := helix.Command([]string{"/a/one.go"}); got != "hx /a/one.go" {
		t.Errorf("helix.Command = %q, want the editor and its file", got)
	}
}

func TestHerdrEditArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"a directory and a file", []string{"herdr-edit", "/a", "/a/x.go"}, true},
		{"several marked files", []string{"herdr-edit", "/a", "/a/x.go", "/a/y.go"}, true},
		{"a directory of that name still opens a tree", []string{"herdr-edit"}, false},
		{"a directory with no files to open", []string{"herdr-edit", "/a"}, false},
		{"something else entirely", []string{"jump", "/a", "/a/x.go"}, false},
	}
	for _, tc := range tests {
		if got := herdrEditArgs(tc.args); got != tc.want {
			t.Errorf("%s: herdrEditArgs(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestRunHerdrEditRejectsWrongArgs(t *testing.T) {
	if err := runHerdrEdit([]string{"/a"}); err == nil {
		t.Error("a directory with no paths was accepted, want a usage error")
	}
}

// The exact sequence openIn sends, pinned, because getting it wrong is silent
// and destructive rather than obviously broken.
//
// Driven against a real helix while this was written, and the failure it guards
// was real: sending the command text with pane.send_input delivers it as a
// bracketed paste, which helix inserts into the buffer. The file ends up
// containing ":open /path/to/other" and marked modified, and nothing opens. The
// command line is only reached by text sent as typing, which is pane.send_text.
//
// So the assertion is not decoration. If someone consolidates these three calls
// into one send_input "because it is atomic", this test is what says no.
func TestOpenInTypesRatherThanPastes(t *testing.T) {
	sock, rec := serveHerdrProcs(t, nil, nil, nil)

	if err := openIn(herdr.New(sock), "w1:p2", []string{"/a/one.go", "/a/two.go"}); err != nil {
		t.Fatalf("openIn: %v", err)
	}

	want := []string{"pane.send_keys", "pane.send_text", "pane.send_keys"}
	if got := rec.methods(); !equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}

	var esc struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(rec.paramsOf("pane.send_keys", 0), &esc); err != nil {
		t.Fatalf("first send_keys: %v", err)
	}
	if len(esc.Keys) != 1 || esc.Keys[0] != "esc" {
		t.Errorf("first keys = %v, want a lone esc to reach normal mode", esc.Keys)
	}

	var cmd struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(rec.paramsOf("pane.send_text", 0), &cmd); err != nil {
		t.Fatalf("send_text: %v", err)
	}
	if cmd.PaneID != "w1:p2" {
		t.Errorf("pane = %q, want w1:p2", cmd.PaneID)
	}
	if cmd.Text != ":open /a/one.go /a/two.go" {
		t.Errorf("text = %q, want the open command and both files", cmd.Text)
	}

	var enter struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(rec.paramsOf("pane.send_keys", 1), &enter); err != nil {
		t.Fatalf("second send_keys: %v", err)
	}
	if len(enter.Keys) != 1 || enter.Keys[0] != "enter" {
		t.Errorf("second keys = %v, want a lone enter to run it", enter.Keys)
	}
}
