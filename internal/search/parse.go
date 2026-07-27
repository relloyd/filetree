package search

import (
	"encoding/json"
	"strings"
)

// Hit is one matching line, with the path as ripgrep reported it — relative to
// the tree root, since Run searches from there.
type Hit struct {
	Path string
	Line int
	Text string
}

// rgEvent is the subset of ripgrep's --json stream we read. Paths and lines
// that are not valid UTF-8 arrive as a "bytes" field instead of "text", which
// decodes to the empty string here and is skipped.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

// ParseJSONLine extracts a hit from one line of ripgrep's --json stream.
// Everything that is not a usable match — begin, end and summary events,
// non-UTF-8 paths, binary-file notices — yields ok=false.
func ParseJSONLine(b []byte) (Hit, bool) {
	var ev rgEvent
	if err := json.Unmarshal(b, &ev); err != nil || ev.Type != "match" {
		return Hit{}, false
	}
	if ev.Data.Path.Text == "" || ev.Data.LineNumber == 0 {
		return Hit{}, false
	}
	return Hit{
		Path: ev.Data.Path.Text,
		Line: ev.Data.LineNumber,
		Text: strings.TrimRight(ev.Data.Lines.Text, "\r\n"),
	}, true
}
