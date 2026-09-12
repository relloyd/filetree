// ft is a terminal file tree explorer: a JetBrains-style project pane for a
// terminal split, with per-root memory, git awareness, and configurable
// commands. See ~/.filetree/config.toml.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/term"

	"github.com/relloyd/filetree/internal/app"
	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/ipc"
	"github.com/relloyd/filetree/internal/platform"
	"github.com/relloyd/filetree/internal/tmux"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: ft [-no-tmux] [dir]\n       ft bookmark <file> <line> [label]\n       ft jump <file>\n       ft herdr-shell <dir>\n       ft herdr-edit <dir> <path>...\n       ft herdr-lazygit <dir>\n\nOpens a file tree for dir (default: current directory).\nThe other forms act on this one and exit. The first two are meant to be bound\nin an editor, e.g. helix:\n  :sh ft bookmark %%{buffer_name} %%{cursor_line}   record a place for the \"B\" view\n  :sh ft jump %%{buffer_name}                      move a running tree's cursor\nConfig: ~/.filetree/config.toml   State: ~/.filetree/state/\n")
		flag.PrintDefaults()
	}
	noTmux := flag.Bool("no-tmux", false, "do not relaunch inside a new tmux session")
	flag.Parse()

	// "ft bookmark <file> <line>" is unambiguous because the tree form takes
	// exactly one argument, so a directory called "bookmark" still opens.
	if flag.NArg() >= 3 && flag.Arg(0) == "bookmark" {
		dir, err := config.Dir()
		fatalIf(err)
		fatalIf(runBookmark(dir, flag.Args()[1:]))
		return
	}
	// Same guard, same reason: two arguments, so "ft jump" on its own still
	// opens a directory called "jump".
	if flag.NArg() == 2 && flag.Arg(0) == "jump" {
		dir, err := config.Dir()
		fatalIf(err)
		fatalIf(runJump(dir, flag.Args()[1:]))
		return
	}
	// And again for the herdr shell command, which the catalogue runs against
	// the selection rather than an editor running it against a buffer.
	if herdrShellArgs(flag.Args()) {
		fatalIf(runHerdrShell(flag.Args()[1:]))
		return
	}
	if herdrEditArgs(flag.Args()) {
		fatalIf(runHerdrEdit(flag.Args()[1:]))
		return
	}
	if herdrLazygitArgs(flag.Args()) {
		fatalIf(runHerdrLazygit(flag.Args()[1:]))
		return
	}

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	abs, err := filepath.Abs(root)
	fatalIf(err)
	fi, err := os.Stat(abs)
	fatalIf(err)
	if !fi.IsDir() {
		fatalIf(fmt.Errorf("%s is not a directory", abs))
	}

	cfgDir, err := config.Dir()
	fatalIf(err)
	cfg, err := config.EnsureAndLoad(cfgDir)
	fatalIf(err)

	// After the checks above on purpose: a bad directory or a malformed config
	// must report in this terminal. Inside a freshly created session the error
	// would vanish with the session it kills.
	mode := cfg.General.Tmux
	if *noTmux {
		mode = tmux.ModeNever
	}
	if tmux.ShouldWrap(tmux.Env{
		TMUX:    os.Getenv("TMUX"),
		STY:     os.Getenv("STY"),
		ZELLIJ:  os.Getenv("ZELLIJ"),
		Mode:    mode,
		HasTmux: tmux.Available(),
		TTY:     term.IsTerminal(os.Stdout.Fd()),
	}) {
		// Only returns if the exec failed; carry on without tmux.
		tmux.Wrap(abs, treeSessionName(cfg.Sessions.Prefix, abs))
	}

	m, err := app.New(cfg, cfgDir, abs, platform.New())
	fatalIf(err)

	p := tea.NewProgram(m)
	// After the tmux wrap on purpose. Wrap is a syscall.Exec, so a socket
	// opened before it would belong to a process that no longer exists — and
	// only the process on the far side has the $TMUX_PANE that says where this
	// tree is on screen.
	//
	// A listener that will not start costs the jump feature and nothing else,
	// which is how every other optional integration here behaves: ft still
	// opens without tmux, without git, without ripgrep.
	var stopIPC func()
	if srv, serr := ipc.Serve(ipc.Dir(cfgDir), os.Getenv("TMUX_PANE"), ipc.Handlers{
		Reveal: revealVia(p),
	}); serr == nil {
		m.SetRootObserver(srv.SetRoot)
		stopIPC = func() { _ = srv.Close() }
	}

	_, err = p.Run()
	// Not deferred: fatalIf exits, and os.Exit does not run deferred calls, so
	// a failing run would leave the socket behind on every crash.
	if stopIPC != nil {
		stopIPC()
	}
	fatalIf(err)
}

// treeSessionName names the session ft is about to put itself in: the tree
// kind, keyed by the root it was opened on, so "tmux ls" says which tree is
// which and the picker can list the others.
//
// The name has to be free before it is handed to Wrap, because a duplicate is
// fatal there — the exec has replaced this process by the time tmux refuses.
// So a second tree on the same root becomes "…-2". A list that fails is not a
// reason to start without a name: the only failure that means "the name might
// be taken" is a working tmux, and a broken one would fail the new-session
// too.
func treeSessionName(prefix, root string) string {
	base := tmux.PlaceName(prefix, tmux.KindTree, root)
	if base == "" {
		return ""
	}
	sessions, err := tmux.List(prefix)
	if err != nil {
		return base
	}
	taken := make([]string, 0, len(sessions))
	for _, s := range sessions {
		taken = append(taken, s.Name)
	}
	return tmux.UniqueName(base, taken)
}

// revealTimeout is how long a jump waits for the model to answer. Long enough
// to cover a busy Update loop, short enough that an ft suspended in an
// interactive command (pressing "e" runs hx through tea.ExecProcess, which
// blocks the loop until it exits) does not hold the editor's keystroke.
const revealTimeout = 2 * time.Second

// revealVia turns a socket request into a message for the model and waits for
// the verdict. Program.Send is the goroutine-safe door in; the reply channel
// is the way back out.
func revealVia(p *tea.Program) func(string) ipc.RevealReply {
	return func(path string) ipc.RevealReply {
		// Buffered, so the delivery below cannot wedge the Update loop after
		// this function has given up and stopped receiving.
		reply := make(chan app.RevealResult, 1)
		// In a goroutine because Send itself blocks: it writes to the
		// program's message channel, and nothing is draining that while the
		// Update loop is suspended running an interactive command. Waiting on
		// the reply alone would not have covered that — the deadline has to
		// cover getting the message *in* as well as getting an answer back.
		go p.Send(app.RevealMsg{Path: path, Reply: reply})
		select {
		case r := <-reply:
			return ipc.RevealReply{OK: r.OK, Reason: r.Reason}
		case <-time.After(revealTimeout):
			// The send is still queued and will land when the loop comes back,
			// so the jump really does happen — there is just nobody left to
			// confirm it. Reporting failure would be a lie the editor shows
			// the user.
			return ipc.RevealReply{OK: true}
		}
	}
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ft:", err)
		os.Exit(1)
	}
}
