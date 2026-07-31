package bookmark

import (
	"path/filepath"
	"strings"

	"github.com/relloyd/filetree/internal/gitx"
)

// KeyFor decides which store a file belongs to and what its path is within it.
//
// The two halves come from different places, and that is the point. The path is
// relative to the **checkout the file is in**, because that is the only thing
// it is actually inside — a linked worktree lives somewhere else entirely on
// disk, so measuring it against the main repository would yield "../.." and
// mean nothing. The store key is the **repository** that checkout belongs to,
// found through git's common dir, so every worktree files into one list.
//
// Together they are what lets a bookmark taken on main appear in a worktree:
// the stored "internal/app/view.go" is joined to whichever checkout root is in
// front of you at the time.
//
// Both are canonicalised, because capture and display must agree on the key
// whichever path each of them arrived by.
//
// A file in no repository keeps its absolute path in the global store, keyed "".
func KeyFor(abs string) (repo, rel string) {
	abs = Canonical(abs)
	checkout := gitx.FindRepoRoot(filepath.Dir(abs))
	if checkout == "" {
		return "", filepath.ToSlash(abs)
	}
	checkout = Canonical(checkout)

	r, err := filepath.Rel(checkout, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		// Outside the checkout it claimed to be in — a symlink pointing out of
		// the tree, most likely. The global store can hold it; a repo's cannot,
		// because a "../" path is not checkout-relative in any useful sense.
		return "", filepath.ToSlash(abs)
	}
	return repoOf(checkout), filepath.ToSlash(r)
}

// repoOf is the store key for a checkout: the repository it belongs to, which
// for a linked worktree is the main one and for anything else is itself.
func repoOf(checkout string) string {
	if main, err := gitx.MainRepoOf(checkout); err == nil && main != "" {
		return Canonical(main)
	}
	return checkout
}

// CheckoutFor is the working tree containing dir — what stored paths are
// resolved against — and RepoFor is the store key for it. The view needs both:
// which list to show, and which copy of the files to read it against.
func CheckoutFor(dir string) string {
	if dir == "" {
		return ""
	}
	return Canonical(gitx.FindRepoRoot(Canonical(dir)))
}

// RepoFor is KeyFor's store key alone, for callers that have a directory
// rather than a file.
func RepoFor(dir string) string {
	checkout := CheckoutFor(dir)
	if checkout == "" {
		return ""
	}
	return repoOf(checkout)
}
