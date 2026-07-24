// Package fsops is the filesystem side of the tree: directory listing and
// change watching.
package fsops

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/relloyd/filetree/internal/tree"
)

// ReadDir lists a directory as tree entries, resolving symlink targets so
// symlinked directories are expandable and broken links are flagged.
func ReadDir(dir string) ([]tree.Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]tree.Entry, 0, len(des))
	for _, de := range des {
		e := tree.Entry{Name: de.Name(), IsDir: de.IsDir()}
		if de.Type()&fs.ModeSymlink != 0 {
			e.IsSymlink = true
			full := filepath.Join(dir, de.Name())
			e.SymlinkTarget, _ = os.Readlink(full)
			if fi, err := os.Stat(full); err != nil {
				e.Broken = true
			} else {
				e.IsDir = fi.IsDir()
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}
