package bookmark

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// State is how well a bookmark still describes a place in the file.
type State int

const (
	Exact   State = iota // the anchor block is still where it was
	Moved                // the whole block was found at a new line
	Drifted              // only the line matched, but unambiguously
	Lost                 // the file reads, the anchor does not appear in it
	Missing              // the file does not read at all
)

func (s State) String() string {
	switch s {
	case Exact:
		return "exact"
	case Moved:
		return "moved"
	case Drifted:
		return "drifted"
	case Lost:
		return "lost"
	default:
		return "missing"
	}
}

// Found reports whether the bookmark still points at a real place.
func (s State) Found() bool { return s == Exact || s == Moved || s == Drifted }

// Resolved is a bookmark located against a particular checkout.
type Resolved struct {
	Bookmark
	Abs string // absolute path in the checkout it was resolved against
	// AtLine is the line it is at now, or the stored line when not Found. Named
	// apart from the embedded Bookmark's At (a timestamp) and Line (where it was
	// stored) so that a row's *current* position is never confused with either.
	AtLine int
	Text   string // the line's current text, or the stored anchor when not Found
	State  State
}

// Search limits. The window bounds how far a line is assumed to have drifted —
// far enough for ordinary editing above the bookmark, close enough that an
// unrelated match elsewhere in a long file is not mistaken for it. The size cap
// keeps a bookmark in a generated or vendored megafile from costing a full read
// on every visit.
const (
	SearchWindow = 500
	MaxFileBytes = 4 << 20
)

// Resolve locates b against a checkout root. It never modifies b; the caller
// decides whether to write a corrected line back.
//
// Matching is two-tier, and the order is the point. A lone "}" or "return nil"
// occurs many times in a file, so matching the bookmarked line by itself would
// happily follow the wrong occurrence — exactly where bookmarks are most
// common. The whole block is tried first, and the single line is accepted only
// when it is unique within the window.
func Resolve(root string, b Bookmark) Resolved {
	abs := filepath.Join(root, filepath.FromSlash(b.Path))
	r := Resolved{Bookmark: b, Abs: abs, AtLine: b.Line, Text: b.Text(), State: Missing}

	lines, err := readLines(abs)
	if err != nil {
		return r
	}
	if len(b.Context) == 0 {
		// Nothing to anchor with: report whatever is at the stored line.
		if b.Line >= 1 && b.Line <= len(lines) {
			r.Text, r.State = lines[b.Line-1], Exact
		} else {
			r.State = Lost
		}
		return r
	}

	// start is where the block would begin if nothing had moved.
	start := b.Line - 1 - b.Offset
	if blockAt(lines, start, b.Context) {
		r.AtLine, r.Text, r.State = b.Line, lines[b.Line-1], Exact
		return r
	}
	if at, ok := nearestBlock(lines, b, start); ok {
		r.AtLine, r.Text, r.State = at, lines[at-1], Moved
		return r
	}
	if at, ok := uniqueLine(lines, b); ok {
		r.AtLine, r.Text, r.State = at, lines[at-1], Drifted
		return r
	}
	r.State = Lost
	return r
}

// blockAt reports whether ctx sits at lines[start:], with start 0-based.
func blockAt(lines []string, start int, ctx []string) bool {
	if start < 0 || start+len(ctx) > len(lines) {
		return false
	}
	for i, want := range ctx {
		if lines[start+i] != want {
			return false
		}
	}
	return true
}

// nearestBlock finds the anchor block elsewhere in the file and returns the
// 1-based line the bookmark itself would be on. Nearest to where it used to be
// wins: a block duplicated across a file (boilerplate, a repeated fixture) is
// most likely still the copy closest to the old position.
func nearestBlock(lines []string, b Bookmark, was int) (int, bool) {
	best, found := 0, false
	lo, hi := max(0, was-SearchWindow), min(len(lines), was+SearchWindow)
	for start := lo; start <= hi; start++ {
		if !blockAt(lines, start, b.Context) {
			continue
		}
		if !found || abs(start-was) < abs(best-was) {
			best, found = start, true
		}
	}
	if !found {
		return 0, false
	}
	return best + b.Offset + 1, true
}

// uniqueLine is the fallback for a bookmark whose *surroundings* were edited:
// the line itself survives, so follow it — but only when there is exactly one
// candidate in the window, since a second one means we would be guessing.
func uniqueLine(lines []string, b Bookmark) (int, bool) {
	text := b.Text()
	if strings.TrimSpace(text) == "" {
		return 0, false // blank lines anchor nothing
	}
	was := b.Line - 1
	lo, hi := max(0, was-SearchWindow), min(len(lines), was+SearchWindow)
	at, n := 0, 0
	for i := lo; i < hi; i++ {
		if lines[i] == text {
			at, n = i+1, n+1
			if n > 1 {
				return 0, false
			}
		}
	}
	return at, n == 1
}

// readLines reads a file into lines, refusing anything implausibly large. Long
// individual lines are kept rather than erroring: a minified file should not
// make a bookmark elsewhere in the store unreadable.
func readLines(path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > MaxFileBytes {
		return nil, os.ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, strings.TrimSuffix(sc.Text(), "\r"))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// Capture reads the anchor block around a 1-based line, returning the context
// and which element of it is the line itself. Near the top or bottom of a file
// there is less to take, which is why the offset is reported rather than
// assumed.
func Capture(path string, line, radius int) (ctx []string, offset int, err error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, 0, err
	}
	if line < 1 || line > len(lines) {
		return nil, 0, os.ErrInvalid
	}
	start := max(0, line-1-radius)
	end := min(len(lines), line+radius)
	return append([]string(nil), lines[start:end]...), line - 1 - start, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Canonical resolves symlinks so that one file reached by two different paths
// keys one store. It matters because git answers path questions in resolved
// form — on macOS /var is a symlink to /private/var, so a repo under /var
// reached directly and the same repo as git reports it would otherwise be two
// stores that never see each other's bookmarks.
func Canonical(p string) string {
	// The empty path is the global store's key, not a path. EvalSymlinks("")
	// answers "." — which would file the no-repository bucket under a hash of
	// the current directory and split it in two.
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
