package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/relloyd/filetree/internal/bookmark"
	"github.com/relloyd/filetree/internal/config"
)

// bookmarkContextRadius is how many lines either side of the bookmarked line
// are kept as its anchor. Enough that a block of boilerplate is distinguishable
// from the next one; small enough that a store of hundreds stays a few hundred
// kilobytes.
const bookmarkContextRadius = 2

// labelMax caps a label, which comes from whatever the editor had selected.
const labelMax = 120

// runBookmark records a bookmark from an editor. It is the whole of the
// capture side: helix binds a key to
//
//	:sh ft bookmark %{buffer_name} %{cursor_line}
//
// which means filetree owns the storage format outright and the editor only
// has to know a command name.
//
// helix expands %{buffer_name} relative to its own working directory when the
// file is under it and absolutely otherwise, and runs the command *with* that
// working directory — so resolving against our own cwd is right either way,
// and there is nothing for the editor to tell us about where it is.
//
// Every failure exits non-zero, which helix reports as "Shell command failed"
// in its status line. That is the only feedback channel there is, so a silent
// success and a loud failure is the right split.
// cfgDir is threaded in rather than resolved here so a test can point the whole
// thing at a temporary directory instead of the caller's real ~/.filetree.
func runBookmark(cfgDir string, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: ft bookmark <file> <line> [label]")
	}
	line, err := strconv.Atoi(strings.TrimSpace(args[1]))
	if err != nil || line < 1 {
		return fmt.Errorf("bad line number %q", args[1])
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)

	// Capture reads the file, so a path that does not exist fails here. That is
	// what rejects an unnamed buffer, which helix expands to the literal
	// "[scratch]", along with any other name that does not name a file.
	ctx, offset, err := bookmark.Capture(abs, line, bookmarkContextRadius)
	if err != nil {
		return fmt.Errorf("cannot bookmark %s:%d: %w", args[0], line, err)
	}

	repo, rel := bookmark.KeyFor(abs)
	cfg, err := config.EnsureAndLoad(cfgDir)
	if err != nil {
		return err
	}

	now := time.Now()
	store := bookmark.Load(bookmark.Dir(cfgDir), repo)
	store.Add(bookmark.Bookmark{
		Path:     rel,
		Line:     line,
		Context:  ctx,
		Offset:   offset,
		Label:    label(args[2:]),
		At:       now,
		LastSeen: now,
	}, cfg.General.BookmarkMax)
	return store.Save(bookmark.Dir(cfgDir), cfg.General.BookmarkMax, now, cfg.RetentionWindow())
}

// label collapses whatever the editor sent into one short line. It is optional
// by design: helix's %{selection} silently aborts the whole command when the
// selection spans lines, so the documented keybinding leaves it out.
func label(args []string) string {
	s := strings.Join(args, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > labelMax {
		s = s[:labelMax]
	}
	return s
}
