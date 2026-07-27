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

// Args builds the ripgrep argument list Run executes.
//
// The search path is left implicit: Run sets the working directory to the tree
// root, so ripgrep reports root-relative paths and there is nothing to strip
// or rejoin on our side.
func Args(q Query) []string {
	// --json is for the parser in parse.go; the rest of the output shape
	// follows from it.
	return append([]string{"--json", "--no-heading", "--color=never"}, matchArgs(q)...)
}

// DisplayArgs builds the same search with human-readable output, for showing
// the user a command they can paste into a shell. It shares matchArgs with
// Args, so the two can never disagree about *which lines match* — they differ
// only in how the matches are printed.
func DisplayArgs(q Query) []string {
	return append([]string{"-n"}, matchArgs(q)...)
}

// FileListArgs builds a command that lists the files the type filter selects,
// without searching inside them — the equivalent of the finder's list when
// only the Type field is filled in.
func FileListArgs(q Query) []string {
	return append([]string{"--files"}, scopeArgs(q)...)
}

// matchArgs is every flag that decides the result set: which files are
// searched, and what counts as a match within them.
func matchArgs(q Query) []string {
	args := scopeArgs(q)
	if q.MaxPerFile > 0 {
		// Enforced by ripgrep itself, per file: it stops reading a file after
		// this many matching lines. Nothing on the Go side counts per file.
		args = append(args, "--max-count", strconv.Itoa(q.MaxPerFile))
	}
	// -e keeps a pattern starting with "-" from being read as a flag.
	return append(args, "-e", q.Regex)
}

// scopeArgs is the set of files in play: the visibility toggles and the type
// filter. It is shared by searching and listing, so a Type filter means the
// same thing whether or not a pattern has been typed yet.
func scopeArgs(q Query) []string {
	var args []string
	if q.Hidden {
		args = append(args, "--hidden")
	}
	if q.NoIgnore {
		args = append(args, "--no-ignore")
	}
	for _, g := range q.Filter.Globs() {
		args = append(args, "-g", g)
	}
	// The tree never shows .git either, and --hidden would otherwise drag the
	// whole object store into the search.
	return append(args, "-g", "!.git/")
}
