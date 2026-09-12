// Package herdr talks to a running herdr server over its local socket.
//
// herdr organises terminals into workspaces, tabs and panes, owns every PTY in
// a background server, and recognises the coding agent occupying a pane. ft
// uses it the way it uses tmux: to find a shell that already belongs to the
// checkout under the cursor, and to open one when there is none.
//
// The socket rather than the "herdr" CLI, for one decisive reason: focusing a
// *named* pane has no CLI form. "herdr pane focus" takes a direction rather
// than an id, and "herdr api" offers only snapshot and schema, while the socket
// exposes pane.focus taking a pane id. Going straight to the socket also costs
// no process spawn, which matters because one key press makes several calls.
//
// The wire format is newline-delimited JSON: a request carries an id, a method
// and its params; the reply carries the same id and either a result or an
// error. That is the same shape internal/ipc uses for ft's own socket, and this
// package is modelled on it — one connection per call, deadlines on both ends,
// and no state kept between calls.
package herdr

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// Environment variables herdr sets in every pane it manages. Their presence is
// how ft knows it is running inside herdr, exactly as $TMUX_PANE tells it that
// it is inside tmux.
const (
	EnvInside = "HERDR_ENV"          // "1" inside a managed pane
	EnvSocket = "HERDR_SOCKET_PATH"  // the server socket to talk to
	EnvPane   = "HERDR_PANE_ID"      // this pane, e.g. "w1:p4"
	EnvTab    = "HERDR_TAB_ID"       // the tab holding it, e.g. "w1:t4"
	EnvSpace  = "HERDR_WORKSPACE_ID" // the workspace holding that, e.g. "w1"
)

// Timeouts. Dialling a local Unix socket succeeds in microseconds or not at
// all, so a missing server is reported almost immediately rather than hanging a
// key press. The call deadline is longer because pane.split has to spawn a
// shell before it can answer.
const (
	dialTimeout = 200 * time.Millisecond
	callTimeout = 3 * time.Second
)

// ErrNotRunning reports a socket with no herdr server behind it — herdr not
// started, or stopped since the pane ft is sitting in was created. It is the
// ordinary state on a machine that does not use herdr, not a malfunction, so
// callers report it and change nothing.
var ErrNotRunning = errors.New("herdr is not running")

// Error is a refusal from the server. herdr's own code is kept alongside the
// message so a caller can recognise a specific failure without matching on
// prose, the way prutil tells "agent_blocked" from the rest.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// Client is a connection-per-call handle on one herdr server.
type Client struct{ socket string }

// New returns a client for the server listening on socket.
func New(socket string) *Client { return &Client{socket: socket} }

// DefaultSocket is where the server listens: whatever herdr told this pane,
// falling back to the documented default for an ft started outside herdr that
// still wants to reach it.
func DefaultSocket() string {
	if s := os.Getenv(EnvSocket); s != "" {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// Self is where this process is running, when herdr is the one running it.
type Self struct {
	Pane      string
	Tab       string
	Workspace string
}

// Inside reports whether this process is running in a pane herdr manages, and
// where. Everything comes from the environment, so it costs nothing and stays
// right even when the server is unreachable.
//
// The pane id has to be there as well as the marker: a HERDR_ENV inherited by
// something herdr is not actually hosting would otherwise convince ft it sits
// in a pane that does not exist.
func Inside() (Self, bool) {
	s := Self{
		Pane:      os.Getenv(EnvPane),
		Tab:       os.Getenv(EnvTab),
		Workspace: os.Getenv(EnvSpace),
	}
	return s, os.Getenv(EnvInside) != "" && s.Pane != ""
}

// request and response are the envelopes. Params is an interface rather than a
// concrete type because every method sends a different shape; Result is left
// raw so call can decode it into whatever the caller asked for.
type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// UnmarshalJSON reads herdr's error object, whose field names differ from the
// Go ones.
func (e *Error) UnmarshalJSON(b []byte) error {
	var raw struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	e.Code, e.Message = raw.Code, raw.Message
	return nil
}

// seq numbers requests. Each call opens its own connection and reads one reply,
// so the id is never needed to match a response to its request — it is here
// because the protocol requires one, and a distinct value per call keeps the
// server's logs readable.
var seq atomic.Uint64

// call sends one request and decodes the result into out, which may be nil for
// a method whose reply carries nothing worth reading.
func (c *Client) call(method string, params any, out any) error {
	if c.socket == "" {
		return ErrNotRunning
	}
	conn, err := net.DialTimeout("unix", c.socket, dialTimeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))

	// Params is always written, even when empty: several methods take an empty
	// object and the server is stricter about a missing field than a bare "{}".
	if params == nil {
		params = struct{}{}
	}
	b, err := json.Marshal(request{
		ID:     "ft:" + strconv.FormatUint(seq.Add(1), 10),
		Method: method,
		Params: params,
	})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	var resp response
	if err := json.Unmarshal(bytes.TrimSpace(line), &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
