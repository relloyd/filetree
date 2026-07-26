package search

import "strconv"

// Query describes one content search.
type Query struct {
	Regex      string // ripgrep pattern
	Filter     Filter // file-type filter, shared with the finder's walk
	Hidden     bool   // search hidden files too
	NoIgnore   bool   // search gitignored files too
	MaxPerFile int    // stop after this many matches per file; 0 for no limit
}

// Args builds the ripgrep argument list for a query.
//
// The search path is left implicit: Run sets the working directory to the tree
// root, so ripgrep reports root-relative paths and there is nothing to strip
// or rejoin on our side.
func Args(q Query) []string {
	args := []string{"--json", "--no-heading", "--color=never"}
	if q.Hidden {
		args = append(args, "--hidden")
	}
	if q.NoIgnore {
		args = append(args, "--no-ignore")
	}
	if q.MaxPerFile > 0 {
		args = append(args, "--max-count", strconv.Itoa(q.MaxPerFile))
	}
	for _, g := range q.Filter.Globs() {
		args = append(args, "-g", g)
	}
	// The tree never shows .git either, and --hidden would otherwise drag the
	// whole object store into the search.
	args = append(args, "-g", "!.git/")
	// -e keeps a pattern starting with "-" from being read as a flag.
	return append(args, "-e", q.Regex)
}
