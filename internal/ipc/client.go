package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Timeouts. The status probe is what a keystroke waits on, once per running
// instance, so it is short: a local Unix socket answers in microseconds or not
// at all. A reveal is allowed much longer because the owning program may be
// waiting on its own event loop to come back from an interactive command.
const (
	dialTimeout   = 200 * time.Millisecond
	statusTimeout = 500 * time.Millisecond
	revealTimeout = 5 * time.Second
)

// ErrDead reports a socket with nothing behind it — the leftover of an
// instance that was killed rather than quit.
var ErrDead = errors.New("no listener")

// Sockets lists the registered sockets, oldest pid first. A missing directory
// is an empty list: it means no ft has ever run, not that anything is wrong.
func Sockets(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".sock" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// Collect asks every registered instance who it is and returns the ones that
// answered.
//
// A socket that refuses the connection is unlinked on the way past. That is
// the whole of the cleanup story: an instance that quits removes its own
// socket, and one that was killed leaves a file that the next jump clears.
// Nothing has to sweep on a timer, and no stale entry can ever be routed to,
// because being routed to requires having just answered.
func Collect(dir string) []Candidate {
	var cands []Candidate
	for _, sock := range Sockets(dir) {
		st, err := Status(sock)
		if errors.Is(err, ErrDead) {
			_ = os.Remove(sock)
			continue
		}
		if err != nil || st.Root == "" {
			continue // alive but not answering usefully; skip, do not unlink
		}
		cands = append(cands, Candidate{PID: st.PID, Root: st.Root, Pane: st.Pane})
	}
	return cands
}

// SocketFor is the socket of a candidate collected from dir.
func SocketFor(dir string, pid int) string {
	return filepath.Join(dir, fmt.Sprintf("%d.sock", pid))
}

// Status asks one instance which root it is on.
func Status(sock string) (StatusReply, error) {
	var r StatusReply
	err := roundTrip(sock, Request{Op: OpStatus}, statusTimeout, &r)
	return r, err
}

// Reveal asks one instance to put its cursor on path.
func Reveal(sock, path string) (RevealReply, error) {
	var r RevealReply
	err := roundTrip(sock, Request{Op: OpReveal, Path: path}, revealTimeout, &r)
	return r, err
}

func roundTrip(sock string, req Request, timeout time.Duration, out any) error {
	c, err := net.DialTimeout("unix", sock, dialTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDead, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))

	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	return json.Unmarshal(bytes.TrimSpace(line), out)
}
