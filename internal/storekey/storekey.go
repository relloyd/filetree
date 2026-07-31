// Package storekey derives the file name a per-directory store is saved under.
// Shared so that state files and bookmark files are named by one rule: a
// human-findable basename, plus a hash of the full path to keep two projects
// with the same basename apart.
package storekey

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// Name is "<sanitised-basename>-<8 hex of sha256(key)><suffix>". An empty key
// has no directory to name itself after, so it gets a fixed name instead of a
// hash of nothing — that is the bucket for files belonging to no project.
func Name(key, suffix string) string {
	if key == "" {
		return "global" + suffix
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s-%x%s", sanitise(filepath.Base(key)), sum[:4], suffix)
}

func sanitise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
