package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// handleDeadline bounds one connection end to end. It is deliberately far
// longer than any client is willing to wait: the client's own deadline is what
// keeps a keystroke responsive, and this one exists only so a half-open
// connection cannot pin a goroutine forever.
const handleDeadline = 10 * time.Second

// maxSocketPath is the size of sun_path in a Unix socket address: 104 bytes on
// macOS, 108 on Linux. Over it the kernel answers "invalid argument", which
// says nothing at all about what went wrong, so the check is here to turn that
// into a sentence. ~/.filetree/run/<pid>.sock leaves room for a home directory
// of about 75 characters, which is why this has never bitten in practice — but
// a deep enough $HOME would silently cost the user the whole feature.
const maxSocketPath = 104

// Dir is where instances register, alongside state/ and bookmarks/ under the
// config directory. It is 0700 and the sockets in it are 0600: a Unix socket
// honours file permissions on connect, so this is what keeps another account
// from driving your editor's sidebar.
//
// Short by design — macOS caps a Unix socket path at 104 bytes, and a pid
// filename under ~/.filetree/run leaves plenty of room for a long home
// directory.
func Dir(cfgDir string) string { return filepath.Join(cfgDir, "run") }

// Handlers are the operations the owning program supplies. Reveal is called on
// the accept goroutine and may block; the client's deadline covers it.
type Handlers struct {
	Reveal func(path string) RevealReply
}

// Server is one instance's listener.
//
// The root is held in an atomic rather than read back out of the model on
// demand, and that is the point of the whole arrangement: an ft suspended in
// an interactive command (pressing "e" runs hx through tea.ExecProcess) has a
// blocked Update loop, so anything that had to round-trip through it would
// hang. Status has to answer while that is true, because an instance that
// cannot answer cannot be routed *around* either — it would just time out and
// stall the editor's keystroke.
type Server struct {
	ln   net.Listener
	sock string
	pid  int
	pane string
	root atomic.Pointer[string]
	h    Handlers
}

// Serve starts listening for jump requests. pane is $TMUX_PANE, or "" outside
// tmux, and is fixed for the life of the process: a pane id survives
// move-pane and break-pane, so only the *location* of that pane can change,
// and the client resolves that at request time.
func Serve(dir, pane string, h Handlers) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	pid := os.Getpid()
	sock := filepath.Join(dir, strconv.Itoa(pid)+".sock")
	if len(sock) >= maxSocketPath {
		return nil, fmt.Errorf("socket path %q is %d bytes, over the %d-byte limit", sock, len(sock), maxSocketPath)
	}
	ln, err := listen(sock)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sock, 0o600)
	s := &Server{ln: ln, sock: sock, pid: pid, pane: pane, h: h}
	s.SetRoot("")
	go s.accept()
	return s, nil
}

// listen binds the socket, clearing a leftover file first.
//
// The name carries this process's pid, so anything already at that path
// belongs to a dead process — unless it answers, which would mean the pid is
// somehow live and in use, and then failing is right.
func listen(sock string) (net.Listener, error) {
	ln, err := net.Listen("unix", sock)
	if err == nil {
		return ln, nil
	}
	c, derr := net.DialTimeout("unix", sock, dialTimeout)
	if derr == nil {
		c.Close()
		return nil, err
	}
	if os.Remove(sock) != nil {
		return nil, err
	}
	return net.Listen("unix", sock)
}

// SetRoot publishes the tree's current root. Wired to the model's root
// observer, so every re-root — ">", the scratch view, worktrees, Esc home —
// reaches it through the one funnel they all share.
func (s *Server) SetRoot(root string) { s.root.Store(&root) }

// Close stops listening and removes the socket. Go unlinks a socket file it
// created itself; the explicit Remove covers the path where it did not.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.Remove(s.sock)
	return err
}

// Addr is the socket path, for tests and for diagnostics.
func (s *Server) Addr() string { return s.sock }

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return // the listener was closed
		}
		// One goroutine per connection: a reveal waiting on the model must
		// not hold up the status probes of every other instance.
		go s.handle(c)
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(handleDeadline))

	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	if json.Unmarshal(bytes.TrimSpace(line), &req) != nil {
		return
	}
	switch req.Op {
	case OpStatus:
		root := ""
		if p := s.root.Load(); p != nil {
			root = *p
		}
		s.write(c, StatusReply{PID: s.pid, Pane: s.pane, Root: root})
	case OpReveal:
		if s.h.Reveal == nil {
			s.write(c, RevealReply{Reason: "this instance cannot reveal"})
			return
		}
		s.write(c, s.h.Reveal(req.Path))
	}
}

func (s *Server) write(c net.Conn, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = c.Write(append(b, '\n'))
}
