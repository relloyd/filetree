package app

import "strings"

// findQuery is the Find field split into one fuzzy include and zero or more
// substring excludes. Tokens are whitespace-separated; a token that starts
// with "!" is an exclude, and the rest are joined as a single include so
// "foo bar" keeps matching as one subsequence (including the space).
//
// Excludes are case-insensitive substrings, not fuzzy. Inverting subsequence
// matching would drop almost every path for a term like "nonprod".
type findQuery struct {
	include string   // joined leftover tokens; empty means no fuzzy include
	exclude []string // lowercased substrings; empty means no excludes
}

func parseFindQuery(s string) findQuery {
	var q findQuery
	var inc []string
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "!") {
			if rest := strings.ToLower(tok[1:]); rest != "" {
				q.exclude = append(q.exclude, rest)
			}
			continue
		}
		inc = append(inc, tok)
	}
	q.include = strings.Join(inc, " ")
	return q
}

// excluded reports whether s contains any exclude as a case-insensitive
// substring.
func (q findQuery) excluded(s string) bool {
	if len(q.exclude) == 0 {
		return false
	}
	low := strings.ToLower(s)
	for _, ex := range q.exclude {
		if strings.Contains(low, ex) {
			return true
		}
	}
	return false
}

// blank reports whether the query does nothing: no include and no exclude.
func (q findQuery) blank() bool {
	return q.include == "" && len(q.exclude) == 0
}
