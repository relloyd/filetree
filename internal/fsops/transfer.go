package fsops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Op is a transfer operation kind.
type Op int

const (
	OpCopy Op = iota
	OpMove
)

// Policy says how to handle a destination that already exists.
type Policy int

const (
	// PolicyNone expects no conflicts; one appearing mid-run (e.g. two
	// sources sharing a basename) falls back to keep-both rather than
	// failing or overwriting.
	PolicyNone Policy = iota
	// PolicyOverwrite moves the existing destination to the Trash first,
	// so an overwrite is always recoverable.
	PolicyOverwrite
	// PolicyKeepBoth transfers under a suffixed name (foo-1.txt).
	PolicyKeepBoth
)

// Result summarises a Transfer for the status bar.
type Result struct {
	Done    int      // transferred under their own name
	Renamed int      // transferred under a keep-both name
	Skipped int      // missing sources, or moves already in place
	Errors  []string // per-item failures; the operation continues past them
}

func (r Result) Summary(op Op) string {
	verb := "Copied"
	if op == OpMove {
		verb = "Moved"
	}
	parts := []string{fmt.Sprintf("%s %d", verb, r.Done+r.Renamed)}
	if r.Renamed > 0 {
		parts = append(parts, fmt.Sprintf("kept both for %d", r.Renamed))
	}
	if r.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d", r.Skipped))
	}
	s := strings.Join(parts, ", ")
	if len(r.Errors) > 0 {
		s += " — " + strings.Join(r.Errors, "; ")
	}
	return s
}

// Transfer copies or moves items into targetDir. trash is used for
// recoverable deletes (overwritten destinations, cross-device move sources)
// and is injected so tests can fake it.
func Transfer(op Op, items []string, targetDir string, policy Policy, trash func(string) error) Result {
	var res Result
	for _, src := range items {
		if err := transferOne(op, src, targetDir, policy, trash, &res); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", filepath.Base(src), err))
		}
	}
	return res
}

func transferOne(op Op, src, targetDir string, policy Policy, trash func(string) error, res *Result) error {
	fi, err := os.Lstat(src)
	if err != nil {
		res.Skipped++ // vanished since it was marked
		return nil
	}
	if fi.IsDir() && (targetDir == src || strings.HasPrefix(targetDir+"/", src+"/")) {
		return errors.New("cannot transfer a directory into itself")
	}

	dst := filepath.Join(targetDir, filepath.Base(src))
	renamed := false
	if _, err := os.Lstat(dst); err == nil {
		switch {
		case dst == src && op == OpMove:
			res.Skipped++ // already in place
			return nil
		case dst == src:
			// Copying into its own directory: overwriting would trash the
			// source itself, so always keep both.
			if dst, err = KeepBothName(dst, fi.IsDir()); err != nil {
				return err
			}
			renamed = true
		case policy == PolicyOverwrite:
			if err := trash(dst); err != nil {
				return fmt.Errorf("trash existing: %w", err)
			}
		default: // PolicyKeepBoth, or a conflict appearing under PolicyNone
			if dst, err = KeepBothName(dst, fi.IsDir()); err != nil {
				return err
			}
			renamed = true
		}
	}

	switch op {
	case OpMove:
		if err := os.Rename(src, dst); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return err
			}
			// Cross-device: copy, then trash (not delete) the source.
			if err := copyPath(src, dst); err != nil {
				return err
			}
			if err := trash(src); err != nil {
				return fmt.Errorf("copied, but trashing source failed: %w", err)
			}
		}
	case OpCopy:
		if err := copyPath(src, dst); err != nil {
			return err
		}
	}
	if renamed {
		res.Renamed++
	} else {
		res.Done++
	}
	return nil
}

// KeepBothName finds a free suffixed name: foo.txt -> foo-1.txt, dir -> dir-1.
// Directories and bare-stem dotfiles suffix the whole name (.env -> .env-1).
func KeepBothName(dst string, isDir bool) (string, error) {
	dir, base := filepath.Dir(dst), filepath.Base(dst)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if isDir || stem == "" || stem == "." {
		stem, ext = base, ""
	}
	for i := 1; i <= 99; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Lstat(cand); errors.Is(err, fs.ErrNotExist) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no free name for %s", base)
}

// copyPath recursively copies a file, directory, or symlink, preserving
// permissions and recreating symlinks rather than following them.
func copyPath(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Chmod(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	case fi.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dst, fi.Mode().Perm())
	default:
		return fmt.Errorf("unsupported file type %v", fi.Mode().Type())
	}
}
