// Package ipc lets another program move a running ft's cursor: an editor
// binds a key to `ft jump <file>`, and the tree pane you are looking at
// highlights that file.
//
// Every instance listens on its own Unix socket under <config dir>/run, named
// after its pid. The jump command dials all of them, asks each which root it
// is on, picks one, and sends the path. Nothing about an instance is baked
// into the filename because nothing about an instance is stable: ft re-roots
// at runtime (">", the scratch view, worktrees), so the root has to be asked
// for at the moment of the request.
//
// The split mirrors the rest of the tree's external-tool packages: route.go is
// pure and table-tested, and the process and socket work lives alone in
// server.go and client.go.
package ipc

// Ops a request can carry. One op per connection, so the client always knows
// which reply shape to decode.
const (
	OpStatus = "status" // who are you, and what root are you on?
	OpReveal = "reveal" // put your cursor on this path
)

// Request is the single JSON object a client writes, newline-terminated.
type Request struct {
	Op   string `json:"op"`
	Path string `json:"path,omitempty"` // absolute; OpReveal only
}

// StatusReply answers OpStatus. It is served from the atomic snapshot rather
// than from the Bubble Tea model, so it answers even while the program is
// suspended running an interactive command — see Server.
type StatusReply struct {
	PID  int    `json:"pid"`
	Pane string `json:"pane"` // $TMUX_PANE at startup; "" outside tmux
	Root string `json:"root"` // the tree's current root, absolute
}

// RevealReply answers OpReveal. Reason is filled in on failure and is meant to
// be read by a person: it reaches the editor's status line.
type RevealReply struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}
