package config

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/relloyd/filetree/internal/tmux"
)

// Vars are the placeholders available in command templates.
type Vars struct {
	Path    string   // absolute path of the selection
	RelPath string   // path relative to the closest parent git repo (or Path)
	Dir     string   // directory of the selection, or itself if a directory
	Root    string   // tree root
	Name    string   // base name of the selection
	Line    int      // matched line of a finder Grep hit; 0 when there is none
	Paths   []string // what the command should act on: the marks, else Path
	Marked  []string // marked paths in mark order (oldest first)

	// The git repository the selection sits in, and the tmux session name
	// built from it. All four are empty outside a repository, which is what
	// lets a command that needs them be refused rather than run against a
	// half-built name.
	//
	// GitRoot is not Root: Root is the tree root, which in the worktrees view
	// is the worktrees directory itself — the wrong place to start an agent.
	GitRoot string // repo (or linked worktree) root containing the selection
	Repo    string // basename of the main repo, shared by all its worktrees
	Branch  string // branch of that checkout, slashes flattened
	Session string // "<prefix>agent/<repo>/<branch>"; the tool is appended by the config

	// Prefix is [sessions] prefix, which every session ft creates carries.
	// Unlike the four fields above it is filled in outside a repository too:
	// a shell or a popup belongs to a directory, and has a name to build
	// there whether or not git knows about it.
	Prefix string
}

// ExpandCommand substitutes known {placeholders} with shell-quoted values.
// Unknown {tokens} are left untouched so things like tmux's `-t "{last}"`
// pass through verbatim. Relative values that start with "-" get a "./"
// prefix so they cannot be parsed as flags by the target program.
//
// Mark placeholders: {marked} is all marked paths space-separated;
// {marked1}/{marked2} are the two most recently marked ({marked2} newest,
// so `diff {marked1} {marked2}` reads old → new). With fewer than two
// marks, {marked1}/{marked2} expand to ”.
//
// {line} is the matched line of a finder Grep hit. It expands to 1 rather
// than 0 when there is no such line, so a template written as
// `hx {path}:{line}` stays valid everywhere else it is used.
//
// {paths} is the multi-file counterpart of {path}: everything the command
// should act on, which is the marked paths when there are any and the
// selection otherwise. It carries the position rather than leaving it to a
// separate {line}, because a list cannot take one suffix — a single target
// becomes `path:42` when there is a line to go to, and a list is plain. Note
// this is the opposite of {line}'s own 0 → 1 fallback: that exists so
// `{path}:{line}` stays valid, and appending `:1` here would be noise.
//
// {session} is the tmux session name for the selection's repo and branch,
// without a tool on the end: a command appends its own, as in
// `-s {session}/claude`. That works despite the quoting because the value is
// quoted and the suffix is not, so the shell joins `'ft/agent/repo/main'/claude`
// back into one word.
//
// {prefix} plus one of the three keys is how the popups name themselves —
// `-s {prefix}shell/{dirkey}` — and it composes for the same reason. A key is
// one path in one component: its basename plus a hash of the whole path, so
// two directories called "web" are two sessions. {dirkey} is the selection's
// directory, {pathkey} the selection itself, {repokey} its checkout — pick
// whichever is the identity worth reattaching to. Only {repokey} needs a git
// repository, and NeedsRepo below is what refuses a command that wants one
// outside of it.
func ExpandCommand(tmpl string, v Vars) string {
	quoted := make([]string, len(v.Marked))
	for i, p := range v.Marked {
		quoted[i] = ShellQuote(p)
	}
	m1, m2 := "''", "''"
	if n := len(v.Marked); n >= 2 {
		m1, m2 = quoted[n-2], quoted[n-1]
	}
	line := v.Line
	if line < 1 {
		line = 1
	}
	// {marked1}/{marked2} must precede {marked}, and {paths} must precede
	// {path}: strings.Replacer tries patterns in argument order at each
	// position, so the shorter prefix would otherwise win and leave a stray "s".
	//
	// {gitroot} and {root} need no such care — a token only ever matches from
	// its own "{", and "{gitroot}" does not contain "{root}" — but the pair is
	// close enough to look like it does, hence this note and the test.
	repl := strings.NewReplacer(
		// The three keys come before {path}, {dir} and {repo} for the reason
		// spelled out below: the shorter token would match first and leave a
		// stray "key" behind.
		"{prefix}", ShellQuote(v.Prefix),
		"{pathkey}", ShellQuote(tmux.Slug(v.Path)),
		"{dirkey}", ShellQuote(tmux.Slug(v.Dir)),
		"{repokey}", ShellQuote(tmux.Slug(v.GitRoot)),
		"{paths}", expandPaths(v),
		"{path}", ShellQuote(v.Path),
		"{relpath}", ShellQuote(noFlag(v.RelPath)),
		"{dir}", ShellQuote(v.Dir),
		"{root}", ShellQuote(v.Root),
		"{name}", ShellQuote(noFlag(v.Name)),
		"{line}", strconv.Itoa(line),
		"{marked1}", m1,
		"{marked2}", m2,
		"{marked}", strings.Join(quoted, " "),
		"{gitroot}", ShellQuote(v.GitRoot),
		"{repo}", ShellQuote(noFlag(v.Repo)),
		"{branch}", ShellQuote(noFlag(v.Branch)),
		"{session}", ShellQuote(v.Session),
	)
	return repl.Replace(tmpl)
}

// expandPaths renders {paths}. An empty Paths falls back to Path, so a
// template using {paths} still does something sensible from a call site that
// only knows about a single file.
func expandPaths(v Vars) string {
	paths := v.Paths
	if len(paths) == 0 {
		if v.Path == "" {
			return ""
		}
		paths = []string{v.Path}
	}
	// Only a lone target can carry a position: ":42" appended to a list would
	// attach to the last path alone, which is not what it would look like.
	if len(paths) == 1 && v.Line > 0 {
		return ShellQuote(paths[0] + ":" + strconv.Itoa(v.Line))
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ShellQuote(p)
	}
	return strings.Join(out, " ")
}

// markTokens are the placeholders that consume the marked set. Kept here, next
// to the Replacer that defines them, so a caller asking "did this command use
// the marks?" cannot drift out of step with what actually gets substituted.
var markTokens = []string{"{paths}", "{marked}", "{marked1}", "{marked2}"}

// repoTokens are the placeholders that only mean something inside a git
// repository. Kept beside the Replacer for the same reason as markTokens.
//
// {prefix}, {dirkey} and {pathkey} are deliberately not here: a shell popup
// names itself after a directory and must keep working outside a repository,
// which is half of where it is used.
var repoTokens = []string{"{session}", "{repo}", "{branch}", "{gitroot}", "{repokey}"}

// NeedsRepo reports whether a command template depends on the selection being
// in a git repository. A template that does is refused outside one rather than
// run with the tokens empty — "tmux new-session -s ”" would otherwise create
// an unnamed session that the picker could never find again.
func NeedsRepo(tmpl string) bool {
	for _, tok := range repoTokens {
		if strings.Contains(tmpl, tok) {
			return true
		}
	}
	return false
}

// UsesMarks reports whether a command template acts on the marked paths. It is
// what decides whether running the command should consume them.
func UsesMarks(tmpl string) bool {
	for _, tok := range markTokens {
		if strings.Contains(tmpl, tok) {
			return true
		}
	}
	return false
}

func noFlag(s string) string {
	if strings.HasPrefix(s, "-") {
		return "./" + s
	}
	return s
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_,./=:@%+-]+$`)

// ShellQuote single-quotes s for POSIX sh unless it is already safe verbatim.
//
// A leading "=" is quoted even though "=" is otherwise safe: zsh expands a
// word starting with "=" to the path of the command named after it (equals
// expansion), so a bare =foo becomes something else entirely or fails outright.
// That is not hypothetical — tmux runs popup commands through default-shell,
// which is zsh on a stock macOS, and it silently broke the "=" exact-match
// target the session picker attaches with. Every other shell leaves =foo alone,
// which is exactly why it survived testing on Linux.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) && !strings.HasPrefix(s, "=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
