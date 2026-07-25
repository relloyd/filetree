package config

import (
	"regexp"
	"strings"
)

// Vars are the placeholders available in command templates.
type Vars struct {
	Path    string   // absolute path of the selection
	RelPath string   // path relative to the closest parent git repo (or Path)
	Dir     string   // directory of the selection, or itself if a directory
	Root    string   // tree root
	Name    string   // base name of the selection
	Marked  []string // marked paths in mark order (oldest first)
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
func ExpandCommand(tmpl string, v Vars) string {
	quoted := make([]string, len(v.Marked))
	for i, p := range v.Marked {
		quoted[i] = ShellQuote(p)
	}
	m1, m2 := "''", "''"
	if n := len(v.Marked); n >= 2 {
		m1, m2 = quoted[n-2], quoted[n-1]
	}
	// {marked1}/{marked2} must precede {marked}: strings.Replacer tries
	// patterns in argument order at each position.
	repl := strings.NewReplacer(
		"{path}", ShellQuote(v.Path),
		"{relpath}", ShellQuote(noFlag(v.RelPath)),
		"{dir}", ShellQuote(v.Dir),
		"{root}", ShellQuote(v.Root),
		"{name}", ShellQuote(noFlag(v.Name)),
		"{marked1}", m1,
		"{marked2}", m2,
		"{marked}", strings.Join(quoted, " "),
	)
	return repl.Replace(tmpl)
}

func noFlag(s string) string {
	if strings.HasPrefix(s, "-") {
		return "./" + s
	}
	return s
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_,./=:@%+-]+$`)

// ShellQuote single-quotes s for POSIX sh unless it is already safe verbatim.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
