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

// maxLine bounds one JSON event. A match in a minified bundle or a sourcemap
// can be enormous — a 2.5 MB line becomes an 11 MB JSON event once escaped —
// and ripgrep offers no way to cap it: --max-columns is ignored in --json mode.
// Past this the event is discarded and counted, never buffered.
const maxLine = 1 << 20

// Result is one batch of hits, or the terminal message of a search.
type Result struct {
	Hits []Hit
	// Skipped counts matches dropped for sitting on a line longer than
	// maxLine. They are reported rather than swallowed: the search was not
	// exhaustive and the caller should be able to say so.
	Skipped int
	Done    bool
	Err     error
}

// ErrNotInstalled is returned when ripgrep is not on PATH. Content search is
// the one feature that needs it; everything else in ft degrades without it.
var ErrNotInstalled = errors.New("content search needs ripgrep (rg) on PATH")

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

	// A child context killed on the way out, so returning for any reason at
	// all takes ripgrep with it. Without this, an early return leaves rg
	// writing into a pipe nobody drains and cmd.Wait never comes back.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
	skipped := 0
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, over, err := readBoundedLine(r, maxLine)
		if over {
			// Every event big enough to trip the limit is a match: begin, end
			// and summary carry only a path and stats and cannot approach a
			// megabyte. Counting them all beats sniffing ripgrep's key order.
			skipped++
		} else if h, ok := ParseJSONLine(line); ok {
			buf = append(buf, h)
			if len(buf) >= hitChunk {
				if !send(ctx, out, Result{Hits: buf, Skipped: skipped}) {
					return
				}
				buf, skipped = make([]Hit, 0, hitChunk), 0
			}
		}
		if err != nil {
			break // io.EOF, or ripgrep died mid-stream
		}
	}
	// The loop above always reads to EOF, so ripgrep has finished writing and
	// Wait cannot block on an undrained pipe.
	send(ctx, out, Result{Hits: buf, Skipped: skipped, Done: true, Err: waitErr(ctx, cmd, &stderr)})
}

// readBoundedLine returns the next newline-terminated line, or reports that it
// was discarded for exceeding limit. Either way the reader is left at the start
// of the next line, so one monstrous line costs a single result rather than the
// whole search.
//
// bufio.Scanner cannot do this: it aborts permanently on an over-long token
// instead of skipping it, which used to strand ripgrep writing into a pipe that
// was no longer being read.
func readBoundedLine(r *bufio.Reader, limit int) (line []byte, over bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		// chunk aliases the reader's buffer, so it has to be copied before the
		// next read — append does that.
		switch {
		case over:
			// Already past the limit: keep draining to the newline, keep the
			// head we have for nothing but the record.
		case len(line)+len(chunk) > limit:
			over = true
			line = append(line, chunk[:limit-len(line)]...)
		default:
			line = append(line, chunk...)
		}
		if err == bufio.ErrBufferFull {
			continue // no newline in this bufferful; the line goes on
		}
		return line, over, err
	}
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
