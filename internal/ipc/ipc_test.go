package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortTempDir is t.TempDir() with a path short enough to hold a socket.
//
// The default lands under $TMPDIR, which on macOS is
// /var/folders/<...>/T/<TestName><digits>/001 — routinely over the 104-byte
// sun_path limit once a pid filename is appended, and the kernel's only
// complaint is "invalid argument". /tmp keeps it well inside.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ftipc")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// serveTest starts a server in a temp dir and stops it when the test ends.
func serveTest(t *testing.T, pane string, h Handlers) (*Server, string) {
	t.Helper()
	dir := shortTempDir(t)
	s, err := Serve(dir, pane, h)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestStatusReportsTheCurrentRoot(t *testing.T) {
	s, _ := serveTest(t, "%11", Handlers{})
	s.SetRoot("/a/proj")

	got, err := Status(s.Addr())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", got.PID, os.Getpid())
	}
	if got.Pane != "%11" {
		t.Errorf("Pane = %q, want %q", got.Pane, "%11")
	}
	if got.Root != "/a/proj" {
		t.Errorf("Root = %q, want /a/proj", got.Root)
	}
}

// Re-rooting is the reason status is asked for rather than recorded: a second
// probe has to see the new root.
func TestStatusFollowsARootChange(t *testing.T) {
	s, _ := serveTest(t, "", Handlers{})
	s.SetRoot("/a/proj")
	if got, _ := Status(s.Addr()); got.Root != "/a/proj" {
		t.Fatalf("Root = %q, want /a/proj", got.Root)
	}
	s.SetRoot("/a/proj/sub")
	if got, _ := Status(s.Addr()); got.Root != "/a/proj/sub" {
		t.Errorf("Root = %q, want /a/proj/sub", got.Root)
	}
}

func TestRevealRoundTrip(t *testing.T) {
	var gotPath string
	s, _ := serveTest(t, "", Handlers{
		Reveal: func(path string) RevealReply {
			gotPath = path
			return RevealReply{OK: true}
		},
	})
	s.SetRoot("/a/proj")

	r, err := Reveal(s.Addr(), "/a/proj/x.go")
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if !r.OK {
		t.Errorf("OK = false, reason %q", r.Reason)
	}
	if gotPath != "/a/proj/x.go" {
		t.Errorf("handler saw %q", gotPath)
	}
}

// A refusal has to reach the caller intact: it is what the editor's status
// line ends up showing.
func TestRevealCarriesItsReason(t *testing.T) {
	s, _ := serveTest(t, "", Handlers{
		Reveal: func(string) RevealReply {
			return RevealReply{Reason: "x.go is gitignored — press i to show"}
		},
	})
	s.SetRoot("/a/proj")

	r, err := Reveal(s.Addr(), "/a/proj/x.go")
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if r.OK || r.Reason != "x.go is gitignored — press i to show" {
		t.Errorf("got %+v", r)
	}
}

func TestCollectFindsALiveInstance(t *testing.T) {
	s, dir := serveTest(t, "%11", Handlers{})
	s.SetRoot("/a/proj")

	cands := Collect(dir)
	if len(cands) != 1 {
		t.Fatalf("Collect() = %+v, want one candidate", cands)
	}
	if cands[0].Root != "/a/proj" || cands[0].Pane != "%11" || cands[0].PID != os.Getpid() {
		t.Errorf("candidate = %+v", cands[0])
	}
}

// The crash case: a socket file with nothing behind it. Collect must drop it
// and clear it, so the directory does not accumulate junk forever.
func TestCollectUnlinksAStaleSocket(t *testing.T) {
	dir := shortTempDir(t)
	stale := filepath.Join(dir, "999999.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.Close() // Go unlinks on close, so put the file back by hand
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if cands := Collect(dir); len(cands) != 0 {
		t.Errorf("Collect() = %+v, want none", cands)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale socket still present: %v", err)
	}
}

// A live instance alongside a dead one must still be found — the dead one
// cannot be allowed to abort the sweep.
func TestCollectSkipsTheDeadAndKeepsTheLive(t *testing.T) {
	s, dir := serveTest(t, "%11", Handlers{})
	s.SetRoot("/a/proj")
	stale := filepath.Join(dir, "1.sock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cands := Collect(dir)
	if len(cands) != 1 || cands[0].Root != "/a/proj" {
		t.Fatalf("Collect() = %+v", cands)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale socket still present")
	}
}

func TestStatusOnADeadSocketIsErrDead(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "1.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Status(sock); !errors.Is(err, ErrDead) {
		t.Errorf("err = %v, want ErrDead", err)
	}
	if _, err := Status(filepath.Join(dir, "nope.sock")); !errors.Is(err, ErrDead) {
		t.Errorf("missing socket: err = %v, want ErrDead", err)
	}
}

// Close must leave nothing behind, or every clean quit would litter.
func TestCloseRemovesTheSocket(t *testing.T) {
	dir := shortTempDir(t)
	s, err := Serve(dir, "", Handlers{})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	sock := s.Addr()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket missing while serving: %v", err)
	}
	s.Close()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket survived Close: %v", err)
	}
}

// A file left at our own pid's path by a previous run must not stop us
// starting: the pid is unique among live processes, so it is certainly dead.
func TestServeReplacesALeftoverSocketFile(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, filepath.Base(SocketFor(dir, os.Getpid())))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(sock, []byte("junk"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Serve(dir, "", Handlers{})
	if err != nil {
		t.Fatalf("Serve over a leftover file: %v", err)
	}
	defer s.Close()
	s.SetRoot("/a/proj")
	if got, err := Status(s.Addr()); err != nil || got.Root != "/a/proj" {
		t.Errorf("Status = %+v, %v", got, err)
	}
}

// The directory has to be private: a socket honours file permissions on
// connect, and this is what stops another account driving the sidebar.
func TestServeCreatesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "run")
	s, err := Serve(dir, "", Handlers{})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer s.Close()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}
	sfi, err := os.Stat(s.Addr())
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := sfi.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %o, want 600", perm)
	}
}

// Garbage on the wire must not take the listener down with it; the next
// request has to still work.
func TestServerSurvivesAJunkRequest(t *testing.T) {
	s, _ := serveTest(t, "", Handlers{})
	s.SetRoot("/a/proj")

	c, err := net.DialTimeout("unix", s.Addr(), dialTimeout)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Write([]byte("not json at all\n"))
	c.SetDeadline(time.Now().Add(time.Second))
	c.Close()

	if got, err := Status(s.Addr()); err != nil || got.Root != "/a/proj" {
		t.Errorf("Status after junk = %+v, %v", got, err)
	}
}

// Reveal with no handler wired must answer rather than hang, so a jump aimed
// at such an instance fails fast instead of stalling the editor.
func TestRevealWithoutAHandlerRefuses(t *testing.T) {
	s, _ := serveTest(t, "", Handlers{})
	s.SetRoot("/a/proj")
	r, err := Reveal(s.Addr(), "/a/proj/x.go")
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if r.OK || r.Reason == "" {
		t.Errorf("got %+v, want a refusal with a reason", r)
	}
}

func TestSocketsIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"1.sock", "2.sock", "notes.txt", "3.sock.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	got := Sockets(dir)
	if len(got) != 2 {
		t.Fatalf("Sockets() = %v, want two", got)
	}
}

func TestSocketsOnAMissingDirectory(t *testing.T) {
	if got := Sockets(filepath.Join(t.TempDir(), "never")); got != nil {
		t.Errorf("Sockets() = %v, want nil", got)
	}
}

// A path over sun_path must fail with something a person can read, not the
// kernel's "invalid argument".
func TestServeRejectsAnOverlongSocketPath(t *testing.T) {
	base := shortTempDir(t)
	deep := filepath.Join(base, strings.Repeat("d", 90))
	_, err := Serve(deep, "", Handlers{})
	if err == nil {
		t.Fatal("Serve on an overlong path succeeded")
	}
	if !strings.Contains(err.Error(), "over the") {
		t.Errorf("err = %v, want the length explained", err)
	}
}
