package config

import (
	"regexp"
	"strings"
)

// Vars are the placeholders available in command templates.
type Vars struct {
	Path    string // absolute path of the selection
	RelPath string // path relative to the closest parent git repo (or Path)
	Dir     string // directory of the selection, or itself if a directory
	Root    string // tree root
	Name    string // base name of the selection
}

// ExpandCommand substitutes known {placeholders} with shell-quoted values.
// Unknown {tokens} are left untouched so things like tmux's `-t "{last}"`
// pass through verbatim. Relative values that start with "-" get a "./"
// prefix so they cannot be parsed as flags by the target program.
func ExpandCommand(tmpl string, v Vars) string {
	repl := strings.NewReplacer(
		"{path}", ShellQuote(v.Path),
		"{relpath}", ShellQuote(noFlag(v.RelPath)),
		"{dir}", ShellQuote(v.Dir),
		"{root}", ShellQuote(v.Root),
		"{name}", ShellQuote(noFlag(v.Name)),
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
