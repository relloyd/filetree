package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// rgBinary is the only external program this package runs; every invocation
// goes through Run, the way internal/gitx owns every git invocation.
const rgBinary = "rg"

// hitChunk is how many hits are batched before being handed to the caller.
// Results should appear while a large search is still running.
const hitChunk = 64

// maxLine bounds one JSON event. Matches in minified or generated files can be
// enormous; past this the line is dropped rather than buffered.
const maxLine = 1 << 20

// ErrNotInstalled is returned when ripgrep is not on PATH. Content search is
// the one feature that needs it; everything else in ft degrades without it.
var ErrNotInstalled = errors.New("content search needs ripgrep (rg) on PATH")

// Result is one batch of hits, or the terminal message of a search.
type Result struct {
	Hits []Hit
	Done bool
	Err  error
}

// Available reports whether ripgrep can be run at all.
func Available() bool {
	_, err := exec.LookPath(rgBinary)
	return err == nil
}

// Run searches root for q, streaming batches of hits to out and closing it
// when the search ends. Cancelling ctx kills ripgrep; a cancelled search
// reports no error, since abandoning a search is not a failure.
func Run(ctx context.Context, root string, q Query, out chan<- Result) {
	defer close(out)

	// fail ends the search with an error, except when the search was
	// cancelled: abandoning one is not a failure, and the caller has already
	// moved on to whatever replaced it.
	fail := func(err error) {
		if ctx.Err() != nil {
			err = nil
		}
		send(ctx, out, Result{Done: true, Err: err})
	}

	if !Available() {
		fail(ErrNotInstalled)
		return
	}

	cmd := exec.CommandContext(ctx, rgBinary, Args(q)...)
	cmd.Dir = root // so ripgrep reports root-relative paths
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail(err)
		return
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail(err)
		return
	}

	buf := make([]Hit, 0, hitChunk)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		h, ok := ParseJSONLine(sc.Bytes())
		if !ok {
			continue
		}
		buf = append(buf, h)
		if len(buf) >= hitChunk {
			if !send(ctx, out, Result{Hits: buf}) {
				break
			}
			buf = make([]Hit, 0, hitChunk)
		}
	}
	// The only way out of the loop above is a cancelled context, and
	// CommandContext kills ripgrep on cancel, so Wait cannot block here on a
	// pipe nobody is draining.
	send(ctx, out, Result{Hits: buf, Done: true, Err: waitErr(ctx, cmd, &stderr)})
}

// waitErr reduces ripgrep's exit status to an error worth showing. Exit 1
// means "no matches", which is an empty result rather than a failure, and a
// cancelled search has no error at all.
func waitErr(ctx context.Context, cmd *exec.Cmd, stderr *bytes.Buffer) error {
	err := cmd.Wait()
	if ctx.Err() != nil || err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return nil
	}
	if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
		return errors.New(msg)
	}
	return err
}

// send delivers a result unless the search has been cancelled, reporting
// whether the caller is still listening.
func send(ctx context.Context, out chan<- Result, r Result) bool {
	select {
	case out <- r:
		return true
	case <-ctx.Done():
		return false
	}
}
