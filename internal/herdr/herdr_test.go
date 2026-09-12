package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stub is a herdr server that answers one canned reply per method, recording
// the params it was sent so a test can assert on the request as well as on how
// the reply was decoded.
type stub struct {
	t       *testing.T
	replies map[string]string // method -> raw JSON written back
	seen    map[string]json.RawMessage
}

// serveStub listens on a socket and answers until the test ends.
//
// /tmp rather than t.TempDir(): on macOS the default temp path routinely
// exceeds the 104-byte sun_path limit, and the kernel's only complaint is
// "invalid argument". internal/ipc's tests keep clear of it the same way.
func serveStub(t *testing.T, replies map[string]string) (*stub, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fthrd")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	s := &stub{t: t, replies: replies, seen: map[string]json.RawMessage{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	return s, sock
}

func (s *stub) handle(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	s.seen[req.Method] = req.Params

	body, ok := s.replies[req.Method]
	if !ok {
		body = `{"error":{"code":"unknown_method","message":"no stub for ` + req.Method + `"}}`
	}
	// Decode and re-encode rather than splicing strings: the canned replies are
	// written across several lines for readability, and the wire format is one
	// reply per line.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		s.t.Errorf("stub reply for %s is not JSON: %v", req.Method, err)
		return
	}
	envelope["id"], _ = json.Marshal(req.ID)
	out, err := json.Marshal(envelope)
	if err != nil {
		s.t.Errorf("stub reply for %s: %v", req.Method, err)
		return
	}
	_, _ = conn.Write(append(out, '\n'))
}

func TestPanesDecodesTheFieldsWeDecideOn(t *testing.T) {
	// Trimmed from a real pane.list reply: an agent pane, a plain shell, and a
	// pane in another workspace.
	_, sock := serveStub(t, map[string]string{
		"pane.list": `{"result":{"type":"pane_list","panes":[
			{"agent":"claude","agent_status":"working","cwd":"/r/filetree","foreground_cwd":"/r/filetree","pane_id":"w1:p3","tab_id":"w1:t3","workspace_id":"w1","revision":126},
			{"agent_status":"unknown","cwd":"/r/filetree","foreground_cwd":"/r/filetree/internal","pane_id":"w1:p4","tab_id":"w1:t4","workspace_id":"w1","revision":7},
			{"agent_status":"unknown","cwd":"/r/other","foreground_cwd":"/r/other","pane_id":"w2:p1","tab_id":"w2:t1","workspace_id":"w2","revision":24}
		]}}`,
	})

	panes, err := New(sock).Panes()
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3", len(panes))
	}
	if panes[0].Agent != "claude" {
		t.Errorf("agent pane: Agent = %q, want claude", panes[0].Agent)
	}
	if panes[1].Agent != "" {
		t.Errorf("shell pane: Agent = %q, want empty", panes[1].Agent)
	}
	// The shell has been cd'd since it started; the foreground directory is the
	// one the matcher must see.
	if got := panes[1].Cwd(); got != "/r/filetree/internal" {
		t.Errorf("Cwd() = %q, want /r/filetree/internal", got)
	}
	if panes[1].TabID != "w1:t4" {
		t.Errorf("TabID = %q, want w1:t4", panes[1].TabID)
	}
}

func TestProcessInfoDecodesBothPidsAndTheParamsAreRight(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"pane.process_info": `{"result":{"type":"pane_process_info","process_info":{
			"foreground_process_group_id":90145,"shell_pid":90145,"pane_id":"w1:p4",
			"foreground_processes":[{"argv0":"zsh","cmdline":"-zsh","cwd":"/r/filetree","name":"zsh","pid":90145}]
		}}}`,
	})

	info, err := New(sock).ProcessInfo("w1:p4")
	if err != nil {
		t.Fatalf("ProcessInfo: %v", err)
	}
	if !info.AtPrompt() {
		t.Error("AtPrompt() = false, want true for a shell owning its terminal")
	}
	if len(info.Foreground) != 1 || info.Foreground[0].Name != "zsh" {
		t.Errorf("Foreground = %+v, want one zsh", info.Foreground)
	}
	var params struct {
		PaneID string `json:"pane_id"`
	}
	if err := json.Unmarshal(s.seen["pane.process_info"], &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.PaneID != "w1:p4" {
		t.Errorf("sent pane_id %q, want w1:p4", params.PaneID)
	}
}

func TestFocusPaneSendsThePaneID(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"pane.focus": `{"result":{"type":"pane_info","pane":{"pane_id":"w1:p4"}}}`,
	})
	if err := New(sock).FocusPane("w1:p4"); err != nil {
		t.Fatalf("FocusPane: %v", err)
	}
	if got := string(s.seen["pane.focus"]); !strings.Contains(got, `"pane_id":"w1:p4"`) {
		t.Errorf("params = %s, want a pane_id of w1:p4", got)
	}
}

func TestSplitPaneAsksForTheRightThing(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"pane.split": `{"result":{"type":"pane_info","pane":{"pane_id":"w1:p5","tab_id":"w1:t4"}}}`,
	})

	id, err := New(sock).SplitPane("w1:p1", "/r/filetree/internal")
	if err != nil {
		t.Fatalf("SplitPane: %v", err)
	}
	if id != "w1:p5" {
		t.Errorf("new pane = %q, want w1:p5", id)
	}
	var params struct {
		Direction string `json:"direction"`
		Target    string `json:"target_pane_id"`
		Dir       string `json:"cwd"`
		Focus     bool   `json:"focus"`
	}
	if err := json.Unmarshal(s.seen["pane.split"], &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	want := struct {
		Direction string `json:"direction"`
		Target    string `json:"target_pane_id"`
		Dir       string `json:"cwd"`
		Focus     bool   `json:"focus"`
	}{"right", "w1:p1", "/r/filetree/internal", true}
	if params != want {
		t.Errorf("params = %+v, want %+v", params, want)
	}
}

// A pane opened when ft is not itself inside herdr has no pane to split from,
// so the target must be left out rather than sent empty.
func TestSplitPaneOmitsAnEmptyTarget(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"pane.split": `{"result":{"type":"pane_info","pane":{"pane_id":"w1:p5"}}}`,
	})
	if _, err := New(sock).SplitPane("", "/r/filetree"); err != nil {
		t.Fatalf("SplitPane: %v", err)
	}
	if got := string(s.seen["pane.split"]); strings.Contains(got, "target_pane_id") {
		t.Errorf("params = %s, want no target_pane_id at all", got)
	}
}

// herdr has been seen to send an identifier either as a bare string or as the
// object it names, so both have to decode.
func TestCreateTabAcceptsEitherSpellingOfRootPane(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"as an object", `{"result":{"type":"tab_created","tab":{"tab_id":"w1:t9"},"root_pane":{"pane_id":"w1:p9"}}}`},
		{"as a string", `{"result":{"type":"tab_created","tab":"w1:t9","root_pane":"w1:p9"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, sock := serveStub(t, map[string]string{"tab.create": tc.reply})
			id, err := New(sock).CreateTab("w1", "/r/filetree", "")
			if err != nil {
				t.Fatalf("CreateTab: %v", err)
			}
			if id != "w1:p9" {
				t.Errorf("root pane = %q, want w1:p9", id)
			}
		})
	}
}

// An error reply must arrive as an error carrying herdr's own code, not as a
// zero value that reads like success.
func TestServerErrorSurfacesWithItsCode(t *testing.T) {
	_, sock := serveStub(t, map[string]string{
		"pane.focus": `{"error":{"code":"pane_not_found","message":"no such pane: w9:p9"}}`,
	})
	err := New(sock).FocusPane("w9:p9")
	if err == nil {
		t.Fatal("FocusPane succeeded, want an error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *herdr.Error", err)
	}
	if apiErr.Code != "pane_not_found" {
		t.Errorf("code = %q, want pane_not_found", apiErr.Code)
	}
	if !strings.Contains(apiErr.Error(), "no such pane") {
		t.Errorf("message = %q, want herdr's own text", apiErr.Error())
	}
}

// A machine without herdr is the ordinary case, not a malfunction, and has to
// be tellable from every other failure so the caller can say so plainly.
func TestNoServerIsErrNotRunning(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fthrd")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if _, err := New(filepath.Join(dir, "absent.sock")).Panes(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
	if _, err := New("").Panes(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("empty socket: err = %v, want ErrNotRunning", err)
	}
}

func TestInsideReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvInside, "1")
	t.Setenv(EnvPane, "w1:p4")
	t.Setenv(EnvTab, "w1:t4")
	t.Setenv(EnvSpace, "w1")
	self, ok := Inside()
	want := Self{Pane: "w1:p4", Tab: "w1:t4", Workspace: "w1"}
	if !ok || self != want {
		t.Errorf("Inside() = %+v, %v; want %+v, true", self, ok, want)
	}

	// Outside herdr nothing is set, and a pane id alone must not be enough:
	// a stale variable inherited from somewhere else should not convince ft it
	// is in a pane that no longer exists.
	t.Setenv(EnvInside, "")
	if _, ok := Inside(); ok {
		t.Error("Inside() = true with HERDR_ENV unset, want false")
	}
}

func TestDefaultSocketPrefersWhatHerdrTold(t *testing.T) {
	t.Setenv(EnvSocket, "/custom/herdr.sock")
	if got := DefaultSocket(); got != "/custom/herdr.sock" {
		t.Errorf("DefaultSocket() = %q, want the environment's value", got)
	}
	t.Setenv(EnvSocket, "")
	if got := DefaultSocket(); !strings.HasSuffix(got, filepath.Join(".config", "herdr", "herdr.sock")) {
		t.Errorf("DefaultSocket() = %q, want the documented default", got)
	}
}

func TestCreateWorkspaceNamesAndFocusesIt(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"workspace.create": `{"result":{"type":"workspace_created","workspace":{"workspace_id":"w3"},"tab":{"tab_id":"w3:t1"},"root_pane":{"pane_id":"w3:p1"}}}`,
	})

	id, tab, err := New(sock).CreateWorkspace("/r/filetree", "filetree")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if id != "w3:p1" {
		t.Errorf("root pane = %q, want w3:p1", id)
	}
	if tab != "w3:t1" {
		t.Errorf("tab = %q, want w3:t1 so it can be named", tab)
	}
	var params struct {
		Dir   string `json:"cwd"`
		Label string `json:"label"`
		Focus bool   `json:"focus"`
	}
	if err := json.Unmarshal(s.seen["workspace.create"], &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.Dir != "/r/filetree" || params.Label != "filetree" || !params.Focus {
		t.Errorf("params = %+v, want the directory, its name, and focus", params)
	}
}

// A tab has to land in the workspace that holds the code, not wherever herdr
// happens to be looking.
func TestCreateTabTargetsAWorkspace(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"tab.create": `{"result":{"type":"tab_created","tab":"w2:t5","root_pane":"w2:p5"}}`,
	})
	if _, err := New(sock).CreateTab("w2", "/r/filetree", "hx"); err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	if got := string(s.seen["tab.create"]); !strings.Contains(got, `"workspace_id":"w2"`) {
		t.Errorf("params = %s, want a workspace_id of w2", got)
	}

	// With no workspace named, the field is left out rather than sent empty.
	s2, sock2 := serveStub(t, map[string]string{
		"tab.create": `{"result":{"type":"tab_created","tab":"w1:t5","root_pane":"w1:p5"}}`,
	})
	if _, err := New(sock2).CreateTab("", "/r/filetree", ""); err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	if got := string(s2.seen["tab.create"]); strings.Contains(got, "workspace_id") {
		t.Errorf("params = %s, want no workspace_id at all", got)
	}
}

// The pane a program actually occupies is not always the first one listed, nor
// the one whose Name says so. Both halves of this come from a real herdr reply:
// a Claude pane running under caffeinate lists the wrapper first, and Claude
// renames itself to its version number once started.
func TestRunsLooksPastWrappersAndRenames(t *testing.T) {
	claudeUnderCaffeinate := ProcessInfo{
		ShellPID:       38269,
		ForegroundPGID: 84503,
		Foreground: []Process{
			{Name: "caffeinate", Argv0: "caffeinate", PID: 38842},
			{Name: "2.1.268", Argv0: "claude", PID: 84503},
		},
	}
	if !claudeUnderCaffeinate.Runs("claude") {
		t.Error(`Runs("claude") = false; the wrapper listed first must not hide it`)
	}
	if claudeUnderCaffeinate.Runs("hx") {
		t.Error(`Runs("hx") = true for a pane running Claude`)
	}

	helix := ProcessInfo{
		ShellPID:       100,
		ForegroundPGID: 200,
		Foreground:     []Process{{Name: "hx", Argv0: "hx", PID: 200}},
	}
	if !helix.Runs("hx") {
		t.Error(`Runs("hx") = false for a pane running helix`)
	}
	if helix.AtPrompt() {
		t.Error("AtPrompt() = true for a pane with an editor on top of the shell")
	}

	idle := ProcessInfo{ShellPID: 100, ForegroundPGID: 100, Foreground: []Process{{Name: "zsh", Argv0: "zsh", PID: 100}}}
	if idle.Runs("hx") {
		t.Error(`Runs("hx") = true for an idle shell`)
	}
	if (ProcessInfo{}).Runs("") {
		t.Error(`Runs("") = true; an empty name must match nothing`)
	}
}

// Renaming a tab must never trample a label that means something.
func TestTabUnnamed(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  bool
	}{
		{"herdr's positional default", "1", true},
		{"a later one", "12", true},
		{"no label at all", "", true},
		{"a name herdr derived from what is running", "ft", false},
		{"one the user chose", "notes", false},
		{"a number with something on it", "2-api", false},
	}
	for _, tc := range tests {
		if got := (Tab{Label: tc.label}).Unnamed(); got != tc.want {
			t.Errorf("%s: Tab{%q}.Unnamed() = %v, want %v", tc.name, tc.label, got, tc.want)
		}
	}
}

// The editor hand-off leans on these reaching the pane intact.
//
// Which method carries the text matters more than it looks: send_input arrives
// as a bracketed paste and send_text as typing, and helix does entirely
// different things with the two. See herdr.SendText.
func TestSendKeysTextAndInput(t *testing.T) {
	s, sock := serveStub(t, map[string]string{
		"pane.send_keys":  `{"result":{"type":"ok"}}`,
		"pane.send_text":  `{"result":{"type":"ok"}}`,
		"pane.send_input": `{"result":{"type":"ok"}}`,
	})
	c := New(sock)
	if err := c.SendKeys("w1:p4", []string{"esc"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if got := string(s.seen["pane.send_keys"]); !strings.Contains(got, `"keys":["esc"]`) {
		t.Errorf("send_keys params = %s, want a lone esc", got)
	}
	if err := c.SendText("w1:p4", ":open a.go"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	var sent struct {
		PaneID string `json:"pane_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(s.seen["pane.send_text"], &sent); err != nil {
		t.Fatalf("params: %v", err)
	}
	if sent.PaneID != "w1:p4" || sent.Text != ":open a.go" {
		t.Errorf("send_text params = %+v, want the pane and the command line", sent)
	}

	if err := c.SendInput("w1:p4", "hx a.go", []string{"enter"}); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	var params struct {
		PaneID string   `json:"pane_id"`
		Text   string   `json:"text"`
		Keys   []string `json:"keys"`
	}
	if err := json.Unmarshal(s.seen["pane.send_input"], &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.PaneID != "w1:p4" || params.Text != "hx a.go" || len(params.Keys) != 1 || params.Keys[0] != "enter" {
		t.Errorf("send_input params = %+v, want the text and one enter", params)
	}
}

// Two keys can want the same binary for different jobs, and the only thing
// telling their panes apart is what each was started with.
//
// The fixtures are real: captured from a herdr server running both forms of
// lazygit. Note the transient git subprocess alongside each one, which is why
// the match scans for the binary rather than trusting a position in the list.
func TestRunsWithAndWithout(t *testing.T) {
	plain := ProcessInfo{
		ShellPID: 10, ForegroundPGID: 20,
		Foreground: []Process{
			{Argv0: "git", Argv: []string{"git", "--version"}},
			{Argv0: "lazygit", Argv: []string{"lazygit"}},
		},
	}
	alpha := ProcessInfo{
		ShellPID: 10, ForegroundPGID: 20,
		Foreground: []Process{
			{Argv0: "git", Argv: []string{"git", "-C", "/repo", "rev-parse"}},
			{Argv0: "lazygit", Argv: []string{"lazygit", "-f", "/repo/alpha.txt"}},
		},
	}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"the plain form is the one with no file", plain.RunsWithout("lazygit", "-f"), true},
		{"and is not any file's history", plain.RunsWith("lazygit", "-f", "/repo/alpha.txt"), false},
		{"a file's history is found by its own path", alpha.RunsWith("lazygit", "-f", "/repo/alpha.txt"), true},
		{"never by another file's", alpha.RunsWith("lazygit", "-f", "/repo/beta.txt"), false},
		{"and never answers for the whole repository", alpha.RunsWithout("lazygit", "-f"), false},
		{"both are still lazygit to a plain name match", alpha.Runs("lazygit") && plain.Runs("lazygit"), true},
		{"a binary that is not there matches nothing", alpha.RunsWith("hx", "-f"), false},
		{"nor does an empty name", alpha.RunsWith("", "-f"), false},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
