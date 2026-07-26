// ft is a terminal file tree explorer: a JetBrains-style project pane for a
// terminal split, with per-root memory, git awareness, and configurable
// commands. See ~/.filetree/config.toml.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/term"

	"github.com/relloyd/filetree/internal/app"
	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/platform"
	"github.com/relloyd/filetree/internal/tmux"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: ft [-no-tmux] [dir]\n\nOpens a file tree for dir (default: current directory).\nConfig: ~/.filetree/config.toml   State: ~/.filetree/state/\n")
		flag.PrintDefaults()
	}
	noTmux := flag.Bool("no-tmux", false, "do not relaunch inside a new tmux session")
	flag.Parse()

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
		tmux.Wrap(abs)
	}

	m, err := app.New(cfg, cfgDir, abs, platform.New())
	fatalIf(err)

	_, err = tea.NewProgram(m).Run()
	fatalIf(err)
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ft:", err)
		os.Exit(1)
	}
}
