package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/relloyd/filetree/internal/agenthook"
	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/platform"
	"github.com/relloyd/filetree/internal/tmux"
)

// agentHookArgs is the same guard the other subcommands use: two arguments, so
// "ft agent-hook" on its own still opens a directory called "agent-hook".
func agentHookArgs(args []string) bool {
	return len(args) == 2 && args[0] == "agent-hook"
}

// maxHookPayload caps what is read from a hook's stdin. Payloads are a few
// hundred bytes; a tool result can be large, and none of it is needed.
const maxHookPayload = 1 << 20

// runAgentHook is "ft agent-hook <event>", run by an agent's own lifecycle
// hooks. It records the agent's state on the tmux session it runs in, so "T"
// and the tree's status bar can show which agents are waiting on you, and asks
// for attention when one is.
//
// It is a subcommand rather than a line of shell in each agent's config for
// the reason "ft herdr-shell" is: one place that knows the option name, the
// value format and the rules, with a table behind it (internal/agenthook).
// Each agent's config then only says which of its events is which.
//
// Outside tmux it does nothing and succeeds: the hooks are installed per user,
// not per terminal, and an agent run straight in a terminal window has no
// session to report on and its own notifications to fall back on.
func runAgentHook(event string) error {
	var payload []byte
	// A hook always pipes its payload in. A terminal on stdin means someone
	// ran this by hand, and reading would wait for input that never comes.
	if !term.IsTerminal(os.Stdin.Fd()) {
		payload, _ = io.ReadAll(io.LimitReader(os.Stdin, maxHookPayload))
	}
	return hookEnv{
		pane:     os.Getenv("TMUX_PANE"),
		now:      time.Now(),
		settings: hookSettings,
		describe: tmux.DescribePane,
		set:      tmux.SetAgent,
		clear:    tmux.ClearAgent,
		bell:     ringBell,
		post:     platform.New().Notify,
	}.run(event, payload)
}

// hookEnv is everything runAgentHook touches outside itself, injected so the
// sequencing — what is set, when the bell rings, when a notification is
// posted — is tested without a tmux server or a desktop.
type hookEnv struct {
	pane     string    // $TMUX_PANE; empty outside tmux
	now      time.Time // when the state changed
	settings func() (prefix string, notify bool)
	describe func(pane string) (tmux.PaneInfo, error)
	set      func(pane, value string) error
	clear    func(pane string) error
	bell     func(tty string) error
	post     func(title, message string) error
}

func (e hookEnv) run(event string, payload []byte) error {
	// Decided before anything else, so a misspelt event in a hook config is
	// reported even when the agent was started outside tmux.
	a, err := agenthook.Decide(event, payload)
	if err != nil {
		return err
	}
	if e.pane == "" {
		return nil
	}
	if a.Clear {
		if err := e.clear(e.pane); err != nil {
			return err
		}
	}
	if a.State != "" {
		if err := e.set(e.pane, tmux.FormatAgent(a.State, e.now)); err != nil {
			return err
		}
	}
	if !a.Alert {
		return nil
	}
	info, err := e.describe(e.pane)
	if err != nil {
		return err
	}
	// The bell goes to the pane whether or not anyone is looking, because
	// tmux is the one that knows what "looking" means: on a window nobody is
	// viewing it becomes the session's "!", cleared when you attach; on one
	// you are viewing it is just a bell. That flag is what tells a finished
	// agent you have read from one you have not.
	if err := e.bell(info.TTY); err != nil {
		return err
	}
	// A banner only for a session nobody has open. One you are looking at
	// has just told you itself, and a notification on top is noise.
	if info.Attached > 0 {
		return nil
	}
	prefix, notify := e.settings()
	if !notify {
		return nil
	}
	return e.post(agenthook.Title(prefix, info.Session), a.Message)
}

// hookSettings reads the two settings the hook needs, falling back to the
// defaults on a config that will not load: a broken config should not stop an
// agent's notifications, and the tree will report it the next time it opens.
// Load, not EnsureAndLoad — a hook is not the place to write a starter file.
func hookSettings() (string, bool) {
	d := config.Default().Sessions
	dir, err := config.Dir()
	if err != nil {
		return d.Prefix, d.Notify
	}
	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		return d.Prefix, d.Notify
	}
	return cfg.Sessions.Prefix, cfg.Sessions.Notify
}

// ringBell writes BEL to a pane's terminal, which reaches tmux exactly as if
// the agent had rung it. O_NOCTTY because the hook may have no controlling
// terminal, and opening one without it would make this tty that terminal.
func ringBell(tty string) error {
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	_, werr := f.Write([]byte{'\a'})
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
