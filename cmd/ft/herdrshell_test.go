package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/relloyd/filetree/internal/herdr"
)

// serveHerdr answers pane.list with the given panes and pane.process_info from
// a pane-id to shell-state map, so a test can say "this pane is busy" without
// starting a process.
//
// /tmp rather than t.TempDir(): on macOS the default temp path routinely
// overruns the 104-byte socket limit, and the kernel only says "invalid
// argument". internal/ipc keeps clear of it the same way.
func serveHerdr(t *testing.T, panes []map[string]any, atPrompt map[string]bool) string {
	sock, _ := serveHerdrProcs(t, panes, atPrompt, nil)
	return sock
}

// calls is the ordered log of what a test's client asked the stub to do. Order
// matters for the editor hand-off, where the same three requests in the wrong
// sequence type the command into the file instead of into helix.
type calls struct {
	mu  sync.Mutex
	log []call
}

type call struct {
	method string
	params json.RawMessage
}

func (c *calls) add(method string, params json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log = append(c.log, call{method: method, params: params})
}

// methods is the sequence of methods called, for asserting on shape.
func (c *calls) methods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.log))
	for _, e := range c.log {
		out = append(out, e.method)
	}
	return out
}

// paramsOf returns the params of the nth call to method.
func (c *calls) paramsOf(method string, n int) json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := 0
	for _, e := range c.log {
		if e.method != method {
			continue
		}
		if seen == n {
			return e.params
		}
		seen++
	}
	return nil
}

// serveHerdrProcs is the same, with the foreground process list of each pane
// spelled out, for the tests that turn on what is running rather than on
// whether anything is.
func serveHerdrProcs(t *testing.T, panes []map[string]any, atPrompt map[string]bool, procs map[string][]map[string]any) (string, *calls) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ftcmd")
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

	rec := &calls{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
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
				if json.Unmarshal(line, &req) != nil {
					return
				}
				rec.add(req.Method, req.Params)
				var result any
				switch req.Method {
				case "pane.list":
					result = map[string]any{"type": "pane_list", "panes": panes}
				case "pane.process_info":
					// An idle shell owns its terminal, so both ids match; a busy
					// one has handed the foreground group to something else.
					var target struct {
						PaneID string `json:"pane_id"`
					}
					_ = json.Unmarshal(req.Params, &target)
					info := map[string]any{"pane_id": target.PaneID, "shell_pid": 100}
					if atPrompt[target.PaneID] {
						info["foreground_process_group_id"] = 100
					} else {
						info["foreground_process_group_id"] = 200
					}
					if fg, ok := procs[target.PaneID]; ok {
						info["foreground_processes"] = fg
					}
					result = map[string]any{"type": "pane_process_info", "process_info": info}
				default:
					result = map[string]any{"type": "ok"}
				}
				out, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
				_, _ = conn.Write(append(out, '\n'))
			}()
		}
	}()
	return sock, rec
}

// repoAt makes a directory look like a git checkout to gitx.FindRepoRoot, which
// walks up looking for .git and does not care what it contains.
func repoAt(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return root
}

func TestScopedPanesAndIdleShells(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ftrepo")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	repo := repoAt(t, filepath.Join(base, "proj"))
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	loose := filepath.Join(base, "loose") // a directory in no repo at all
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	panes := []map[string]any{
		{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": repo},                        // plain shell, at the root
		{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": repo, "foreground_cwd": sub}, // same shell, cd'd deeper
		{"pane_id": "w1:p3", "tab_id": "w1:t3", "workspace_id": "w1", "cwd": repo, "agent": "claude"},     // an agent, never a shell
		{"pane_id": "w1:p4", "tab_id": "w1:t4", "workspace_id": "w1", "cwd": repo},                        // busy: something is running
		{"pane_id": "w2:p1", "tab_id": "w2:t1", "workspace_id": "w2", "cwd": loose},                       // not in this checkout
		{"pane_id": "w1:p6", "tab_id": "w1:t6", "workspace_id": "w1"},                                     // herdr knows no directory
		{"pane_id": "w1:p9", "tab_id": "w1:t9", "workspace_id": "w1", "cwd": repo},                        // ft's own pane
	}
	sock := serveHerdr(t, panes, map[string]bool{
		"w1:p1": true, "w1:p2": true, "w1:p4": false, "w2:p1": true, "w1:p9": true,
	})

	c := herdr.New(sock)
	all, err := c.Panes()
	if err != nil {
		t.Fatalf("Panes: %v", err)
	}

	// In the checkout: every pane of it except our own, agents and busy shells
	// included, because this list also answers "where does this code live".
	scoped := inScope(all, herdr.ScopeFor(sub, repo), "w1:p9", "")
	if got := ids(scoped); !equal(got, []string{"w1:p1", "w1:p2", "w1:p3", "w1:p4"}) {
		t.Errorf("inScope = %v, want the four panes of the checkout", got)
	}

	// Narrowed to the ones that could take a command: the agent and the busy
	// shell drop out.
	shells := atPrompt(c, scoped)
	if got := shellIDs(shells); !equal(got, []string{"w1:p1", "w1:p2"}) {
		t.Errorf("atPrompt = %v, want the two idle shells", got)
	}
	for _, sh := range shells {
		if sh.Checkout != repo {
			t.Errorf("%s: Checkout = %q, want %q", sh.PaneID, sh.Checkout, repo)
		}
	}
	// The deeper one is nearer the selection, so it is the one chosen.
	if w, ok := herdr.Pick(shells, sub, herdr.ScopeFor(sub, repo), ""); !ok || w.PaneID != "w1:p2" {
		t.Errorf("Pick = %+v, %v; want w1:p2", w, ok)
	}

	// The loose directory has its own scope, and the checkout's panes are no
	// part of it even though they sit alongside on disk.
	looseScoped := inScope(all, herdr.ScopeFor(loose, ""), "w1:p9", "")
	if got := ids(looseScoped); !equal(got, []string{"w2:p1"}) {
		t.Errorf("inScope(loose) = %v, want just the loose shell", got)
	}
}

func ids(ps []scopedPane) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.pane.ID)
	}
	sort.Strings(out)
	return out
}

func shellIDs(ss []herdr.Shell) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.PaneID)
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Where a new shell goes is the part that decides whether a workspace list stays
// meaningful, so each of the three placements is pinned down here.
func TestWorkspaceFor(t *testing.T) {
	pane := func(id, ws string) scopedPane {
		return scopedPane{pane: herdr.Pane{ID: id, WorkspaceID: ws}}
	}
	tests := []struct {
		name          string
		scoped        []scopedPane
		selfWorkspace string
		want          string
		wantFound     bool
	}{
		{
			name:      "herdr has never seen this place",
			scoped:    nil,
			want:      "",
			wantFound: false,
		},
		{
			name:          "the code lives where the tree is, so the tree's workspace wins",
			scoped:        []scopedPane{pane("w2:p1", "w2"), pane("w1:p5", "w1")},
			selfWorkspace: "w1",
			want:          "w1",
			wantFound:     true,
		},
		{
			name:          "the code lives elsewhere, so that is where the shell goes",
			scoped:        []scopedPane{pane("w2:p1", "w2")},
			selfWorkspace: "w1",
			want:          "w2",
			wantFound:     true,
		},
		{
			name:      "spread across two workspaces, it resolves the same way every press",
			scoped:    []scopedPane{pane("w3:p1", "w3"), pane("w2:p1", "w2")},
			want:      "w2",
			wantFound: true,
		},
		{
			name:      "a pane herdr gave no workspace is no answer",
			scoped:    []scopedPane{pane("w2:p1", "")},
			want:      "",
			wantFound: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := workspaceFor(tc.scoped, tc.selfWorkspace)
			if got != tc.want || found != tc.wantFound {
				t.Errorf("workspaceFor = %q, %v; want %q, %v", got, found, tc.want, tc.wantFound)
			}
		})
	}
}

func TestHerdrShellArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"the subcommand form", []string{"herdr-shell", "/a"}, true},
		{"a directory of that name still opens a tree", []string{"herdr-shell"}, false},
		{"too many arguments", []string{"herdr-shell", "/a", "/a"}, false},
		{"something else entirely", []string{"jump", "/a"}, false},
		{"nothing", nil, false},
	}
	for _, tc := range tests {
		if got := herdrShellArgs(tc.args); got != tc.want {
			t.Errorf("%s: herdrShellArgs(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestAbbrev(t *testing.T) {
	tests := []struct {
		name, gitroot, dir, want string
	}{
		{"inside the checkout", "/r/proj", "/r/proj/internal/app", "internal/app"},
		{"the checkout root reads as its own name", "/r/proj", "/r/proj", "proj"},
		{"outside it, the full path is all we can say", "/r/proj", "/elsewhere", "../../elsewhere"},
	}
	for _, tc := range tests {
		if got := abbrev(tc.gitroot, tc.dir); got != tc.want {
			t.Errorf("%s: abbrev(%q, %q) = %q, want %q", tc.name, tc.gitroot, tc.dir, got, tc.want)
		}
	}
}

func TestRunHerdrShellRejectsWrongArgs(t *testing.T) {
	if err := runHerdrShell([]string{"/a", "/b"}); err == nil {
		t.Error("two arguments were accepted, want a usage error")
	}
}

// A dotfiles repository at ~ would otherwise make every loose directory on the
// machine one enormous checkout, so that asking for a shell in ~/Downloads
// handed you the one sitting in ~/pentaho. Both resolve to $HOME, and both
// would then "belong to the same checkout".
func TestCheckoutForIgnoresAHomeSpanningRepo(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "fthome")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	repoAt(t, home) // ~ is itself a checkout, as a dotfiles repo makes it

	downloads := filepath.Join(home, "Downloads")
	project := repoAt(t, filepath.Join(home, "proj"))
	nested := filepath.Join(project, "internal")
	for _, d := range []string{downloads, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	tests := []struct {
		name, dir, want string
	}{
		{"a loose directory under a dotfiles home has no checkout", downloads, ""},
		{"nor does the home directory itself", home, ""},
		{"a real project inside it still has one", nested, project},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkoutFor(tc.dir, home); got != tc.want {
				t.Errorf("checkoutFor(%q) = %q, want %q", tc.dir, got, tc.want)
			}
		})
	}

	// Without the home rule, these two would share a scope; with it they are
	// separate loose directories and neither answers for the other.
	dl := herdr.ScopeFor(downloads, checkoutFor(downloads, home))
	if dl.Repo {
		t.Errorf("Downloads scope = %+v, want a loose one", dl)
	}
	if dl.Covers(checkoutFor(filepath.Join(home, "music"), home), filepath.Join(home, "music")) {
		t.Error("a sibling loose directory is covered by the Downloads scope, want not")
	}
	if !dl.Covers(checkoutFor(home, home), home) {
		t.Error("the home directory above it is not covered, want covered so one shell serves both")
	}
}
