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

	"github.com/relloyd/filetree/internal/app"
	"github.com/relloyd/filetree/internal/config"
	"github.com/relloyd/filetree/internal/platform"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: ft [dir]\n\nOpens a file tree for dir (default: current directory).\nConfig: ~/.filetree/config.toml   State: ~/.filetree/state/\n")
		flag.PrintDefaults()
	}
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
