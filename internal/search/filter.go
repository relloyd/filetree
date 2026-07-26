// Package search owns the finder's file-type filter and the ripgrep
// integration behind the "/" finder's content search. Pattern compilation,
// argument building, and output parsing are pure so they can be table-tested;
// the single process spawn lives in exec.go, the way internal/gitx owns every
// git invocation.
package search

import (
	"fmt"
	"path"
	"strings"
)

// metachars are the glob characters that mark a token as already being a
// pattern rather than a bare extension or filename.
const metachars = "*?[]{}"

// pattern is one compiled filter term.
type pattern struct {
	glob string // canonical glob, exactly as handed to `rg -g`
	base bool   // match against the basename rather than the whole rel path
}

// Filter selects paths by glob. The zero Filter matches everything.
//
// A filter is written as a comma-separated list of terms; a term prefixed with
// "!" excludes. A path is kept when it matches at least one include (or there
// are no includes) and matches no exclude.
//
// Terms are canonicalised so the walk and ripgrep agree on what a term means:
//
//	hcl              -> *.hcl and hcl   (extension, or a file with that name)
//	.hcl             -> *.hcl
//	terragrunt.hcl   -> terragrunt.hcl  (matched against the basename)
//	*.tf             -> *.tf            (matched against the basename)
//	infra/**/*.hcl   -> matched against the whole root-relative path
//
// A term containing "/" is matched against the root-relative path, where "**"
// spans zero or more path segments and "*" never crosses a separator. Every
// other term is matched against the basename.
type Filter struct {
	include []pattern
	exclude []pattern
}

// CompileFilter parses a filter expression. An empty expression yields the
// zero Filter, which matches everything.
func CompileFilter(s string) (Filter, error) {
	var f Filter
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		neg := strings.HasPrefix(tok, "!")
		tok = strings.TrimPrefix(tok, "!")
		if tok == "" {
			continue
		}
		pats, err := canonicalise(tok)
		if err != nil {
			return Filter{}, err
		}
		if neg {
			f.exclude = append(f.exclude, pats...)
		} else {
			f.include = append(f.include, pats...)
		}
	}
	return f, nil
}

// canonicalise turns one filter term into the globs it stands for. A bare word
// yields two: the extension reading (*.go) and the exact-name reading (go), so
// "hcl" finds *.hcl and "Makefile" finds Makefile without the user having to
// know which is meant.
func canonicalise(tok string) ([]pattern, error) {
	switch {
	case strings.Contains(tok, "/"):
		if err := validate(tok); err != nil {
			return nil, err
		}
		return []pattern{{glob: tok}}, nil

	case strings.ContainsAny(tok, metachars):
		if err := validate(tok); err != nil {
			return nil, err
		}
		return []pattern{{glob: tok, base: true}}, nil

	case strings.HasPrefix(tok, ".") && !strings.Contains(tok[1:], "."):
		return []pattern{{glob: "*" + tok, base: true}}, nil

	case strings.Contains(tok, "."):
		return []pattern{{glob: tok, base: true}}, nil

	default:
		return []pattern{
			{glob: "*." + tok, base: true},
			{glob: tok, base: true},
		}, nil
	}
}

// validate rejects malformed globs up front so a typo surfaces as an error
// instead of silently matching nothing.
func validate(glob string) error {
	for _, seg := range strings.Split(glob, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("bad pattern %q: %w", glob, err)
		}
	}
	return nil
}

// Empty reports whether the filter selects everything.
func (f Filter) Empty() bool { return len(f.include) == 0 && len(f.exclude) == 0 }

// Match reports whether a root-relative slash path passes the filter.
func (f Filter) Match(rel string) bool {
	for _, p := range f.exclude {
		if p.match(rel) {
			return false
		}
	}
	if len(f.include) == 0 {
		return true
	}
	for _, p := range f.include {
		if p.match(rel) {
			return true
		}
	}
	return false
}

// Globs returns the filter as ripgrep -g arguments, exclusions negated. The
// walk and ripgrep therefore apply the same patterns.
func (f Filter) Globs() []string {
	out := make([]string, 0, len(f.include)+len(f.exclude))
	for _, p := range f.include {
		out = append(out, p.glob)
	}
	for _, p := range f.exclude {
		out = append(out, "!"+p.glob)
	}
	return out
}

func (p pattern) match(rel string) bool {
	if p.base {
		ok, err := path.Match(p.glob, path.Base(rel))
		return err == nil && ok
	}
	return matchSegments(strings.Split(p.glob, "/"), strings.Split(rel, "/"))
}

// matchSegments matches a slash-split glob against a slash-split path. "**"
// spans zero or more segments; every other segment goes through path.Match, so
// "*" never crosses a separator.
func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
