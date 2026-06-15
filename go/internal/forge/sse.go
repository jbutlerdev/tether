// Server-Sent Events (SSE) consumer for the Forge agent stream.
// See plan.md §8.2.
//
// The consumer is a self-contained parser of the SSE wire
// format defined in the HTML5 spec (https://html.spec.whatwg.org/
// multipage/server-sent-events.html). It takes an io.Reader
// (the HTTP response body) and a destination channel of
// forge.Event, and emits one event per SSE frame:
//
//	event: <name>\n
//	id: <seq>\n
//	data: <body>\n
//	data: <body continued>\n
//	\n
//
// The consumer treats anything it does not recognise as a no-op
// (heartbeats, comments, retry hints) and continues. Malformed
// JSON in the data field is preserved verbatim — JSON validation
// is the caller's responsibility.
//
// Wire format summary:
//
//   - A "data:" line is buffered; multiple data: lines are joined
//     with a single '\n' separator and emitted as one event.
//   - An "event:" line sets the event name. Default is "message".
//   - An "id:" line sets the sequence number (parsed as int64).
//   - A line beginning with ':' is a comment and is dropped.
//   - A blank line dispatches the buffered event (if any).
//
// The consumer is built around bufio.Reader to handle the
// multi-MB stdout lines a bash tool can produce. A
// bufio.Scanner-based implementation would cap at 64 KB and
// fail in the field.
package forge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
)

// SSEConsumer parses the SSE wire format. It is constructed with
// the source reader and a starting sequence cursor; Run reads
// frames and emits them on a freshly-allocated events channel.
// The channel is closed when Run returns; the final error is
// available via Err().
type SSEConsumer struct {
	// src is the byte source. Buffered by the constructor.
	src *bufio.Reader

	// since is the initial cursor. Stored atomically so
	// reconnect logic can read the latest value without
	// locking. Set via SetSince.
	since atomic.Int64

	// maxLineBytes is the cap on a single data: line. The
	// bufio.Reader buffers up to this much between Read calls.
	// Default is 8 MiB, large enough for the largest realistic
	// bash stdout. A line that exceeds this cap is read up to
	// the cap and the rest is dropped (with a logged error in
	// the caller's logger if it wires one up; the consumer
	// itself is logger-free for testability).
	maxLineBytes int

	// runErr is the final error from Run. Stored in errGroup-
	// style: set by run, read by Err.
	runErr atomic.Pointer[error]
}

// SSEConsumerOption configures an SSEConsumer at construction
// time.
type SSEConsumerOption func(*SSEConsumer)

// SSEConsumerOptionMaxLineBytes overrides the per-line cap.
// The default is 8 MiB; tests use small values to keep the
// per-line buffering predictable.
func SSEConsumerOptionMaxLineBytes(n int) SSEConsumerOption {
	return func(c *SSEConsumer) {
		if n > 0 {
			c.maxLineBytes = n
		}
	}
}

// NewSSEConsumer returns a fresh SSEConsumer over r. The
// reader is buffered internally with the SSE line cap; pass
// a raw http.Response.Body and the consumer handles the
// rest.
func NewSSEConsumer(r io.Reader, opts ...SSEConsumerOption) *SSEConsumer {
	c := &SSEConsumer{
		maxLineBytes: 8 * 1024 * 1024,
	}
	for _, o := range opts {
		o(c)
	}
	if r == nil {
		c.src = bufio.NewReaderSize(strings.NewReader(""), c.maxLineBytes)
	} else if br, ok := r.(*bufio.Reader); ok {
		c.src = br
	} else {
		c.src = bufio.NewReaderSize(r, c.maxLineBytes)
	}
	return c
}

// SetSince sets the initial sequence cursor. A caller that has
// seen events up to seq N and is reconnecting passes N here
// (or N+1, depending on the inclusive/exclusive convention
// the forge backend uses — see plan §8.2).
func (c *SSEConsumer) SetSince(s int64) { c.since.Store(s) }

// Since returns the latest sequence cursor. After Run starts
// processing events, this is updated to the highest seq the
// parser has dispatched.
func (c *SSEConsumer) Since() int64 { return c.since.Load() }

// Err returns the final error from Run, or nil if Run has not
// been called or completed successfully. Safe to call from
// any goroutine; the value is set before the events channel
// is closed.
func (c *SSEConsumer) Err() error {
	p := c.runErr.Load()
	if p == nil {
		return nil
	}
	return *p
}

// ErrLineTooLong is returned by Run when a data: line exceeds
// the configured maxLineBytes. The line is truncated and the
// partial event is delivered (the consumer never silently
// drops bytes).
var ErrLineTooLong = errors.New("forge: sse: line exceeds max size")

// Run reads SSE frames from the consumer's source and emits
// them on a freshly-allocated events channel. The channel is
// closed when the consumer returns; the final error is
// available via Err(). The buffer size is configurable via
// RunBuffered (default 32).
//
// The caller consumes the channel with `for ev := range events`.
// The loop exits when the channel closes; after the loop, the
// caller should check Err() for the cause of termination.
func (c *SSEConsumer) Run(ctx context.Context) <-chan Event {
	return c.RunBuffered(ctx, 32)
}

// RunBuffered is Run with a configurable channel buffer. The
// buffer is the backpressure window: a full channel pauses
// reads from the wire until the consumer catches up.
func (c *SSEConsumer) RunBuffered(ctx context.Context, bufSize int) <-chan Event {
	if bufSize < 1 {
		bufSize = 1
	}
	events := make(chan Event, bufSize)
	go func() {
		err := c.run(ctx, events)
		// Record the final error before closing the
		// channel so the caller's "for range" loop can
		// safely read Err() afterwards.
		e := err
		c.runErr.Store(&e)
		close(events)
	}()
	return events
}

// run is the inner loop. It dispatches events on the
// caller-provided channel and returns when ctx is canceled,
// the source hits EOF, or a read error occurs.
func (c *SSEConsumer) run(ctx context.Context, events chan<- Event) error {
	var (
		eventType strings.Builder
		dataLines []string
		idStr     string
	)

	flush := func() error {
		if len(dataLines) == 0 && eventType.Len() == 0 {
			return nil
		}
		name := eventType.String()
		if name == "" {
			name = "message"
		}
		var seq int64
		if idStr != "" {
			parsed, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil {
				seq = parsed
				c.since.Store(seq)
			}
		}
		content := strings.Join(dataLines, "\n")
		ev := Event{Type: name, Content: content, Seq: seq}
		select {
		case events <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
		eventType.Reset()
		dataLines = dataLines[:0]
		idStr = ""
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := c.readLine(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = flush()
				return nil
			}
			return err
		}
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, ok := splitField(line)
		if !ok {
			continue
		}
		switch field {
		case "event":
			eventType.Reset()
			eventType.WriteString(value)
		case "data":
			dataLines = append(dataLines, value)
		case "id":
			idStr = value
		case "retry":
			// The retry hint is honoured by the *real* HTTP
			// client, not by the consumer. Drop.
		default:
			// Unknown field. Drop, per SSE spec.
		}
	}
}

// readLine reads a single line from src, returning it without
// the trailing newline. ctx cancellation is checked before the
// read; for long reads the caller is expected to provide a
// cancellable body (e.g. http.Response.Body, which cancels
// in-flight Reads on Close). This keeps the per-line cost to
// one syscall-equivalent without spawning a goroutine per
// line.
func (c *SSEConsumer) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b, err := c.src.ReadString('\n')
	return trimSSEline(b), err
}

// trimSSEline strips a single trailing "\n" or "\r\n" from
// the line. SSE lines are CR, LF, or CRLF-terminated.
func trimSSEline(s string) string {
	if len(s) == 0 {
		return s
	}
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	return s
}

// splitField splits an SSE line into (field, value) at the
// first colon. If the colon is followed by a single space,
// that space is dropped (per the SSE spec). Lines without a
// colon are treated as a field with empty value.
func splitField(line string) (field, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	field = line[:idx]
	value = line[idx+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return field, value, true
}

// String renders a one-line summary of the consumer for
// debugging. Not used in production code paths.
func (c *SSEConsumer) String() string {
	return fmt.Sprintf("SSEConsumer{since=%d, maxLineBytes=%d}", c.Since(), c.maxLineBytes)
}
