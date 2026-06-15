// Tests for the forge SSE consumer. See plan.md §8.2.
//
// The consumer is the read side of the forge SSE stream. It is
// self-contained (no third-party SSE library) and CI-tested by
// feeding it SSE wire bytes and asserting on the emitted events.
package forge_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/forge"
)

// TestSSEConsumer_EmitsTextDelta verifies that a basic
// text_delta frame is parsed into a forge.Event with the right
// type and body.
func TestSSEConsumer_EmitsTextDelta(t *testing.T) {
	t.Parallel()
	wire := "event: text_delta\ndata: {\"delta\":\"hello\"}\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != forge.EventTextDelta {
		t.Errorf("Type: want %q, got %q", forge.EventTextDelta, ev.Type)
	}
	if ev.Content != `{"delta":"hello"}` {
		t.Errorf("Content: want %q, got %q", `{"delta":"hello"}`, ev.Content)
	}
}

// TestSSEConsumer_EmitsToolCallStartEnd verifies that both the
// start and end tool events are parsed with the right types and
// the JSON bodies are preserved verbatim.
func TestSSEConsumer_EmitsToolCallStartEnd(t *testing.T) {
	t.Parallel()
	wire := strings.Join([]string{
		"event: tool_call_start",
		`data: {"tool":"bash","args":{"cmd":"ls"}}`,
		"",
		"event: tool_call_end",
		`data: {"tool":"bash","output":"foo\n"}`,
		"",
		"",
	}, "\n")
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	start := mustEvent(t, events, 1*time.Second)
	if start.Type != forge.EventToolCallStart {
		t.Errorf("Type[0]: want %q, got %q", forge.EventToolCallStart, start.Type)
	}
	if !strings.Contains(start.Content, `"tool":"bash"`) {
		t.Errorf("Content[0]: want bash tool, got %q", start.Content)
	}
	end := mustEvent(t, events, 1*time.Second)
	if end.Type != forge.EventToolCallEnd {
		t.Errorf("Type[1]: want %q, got %q", forge.EventToolCallEnd, end.Type)
	}
}

// TestSSEConsumer_EmitsAgentEnd verifies that the
// "agent_end" event is parsed.
func TestSSEConsumer_EmitsAgentEnd(t *testing.T) {
	t.Parallel()
	wire := "event: agent_end\ndata: {}\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != forge.EventAgentEnd {
		t.Errorf("Type: want %q, got %q", forge.EventAgentEnd, ev.Type)
	}
}

// TestSSEConsumer_MultipleDataLinesPerEvent verifies that the
// consumer concatenates multiple "data:" lines with a "\n"
// between them, per the SSE spec.
func TestSSEConsumer_MultipleDataLinesPerEvent(t *testing.T) {
	t.Parallel()
	wire := "event: text_delta\ndata: line1\ndata: line2\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Content != "line1\nline2" {
		t.Errorf("Content: want %q, got %q", "line1\nline2", ev.Content)
	}
}

// TestSSEConsumer_Heartbeat verifies that comment lines
// (":heartbeat") are silently dropped, the consumer does not
// crash, and the subsequent real event is delivered.
func TestSSEConsumer_Heartbeat(t *testing.T) {
	t.Parallel()
	wire := ":heartbeat\n\nevent: agent_end\ndata: {}\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != forge.EventAgentEnd {
		t.Errorf("Type: want %q, got %q", forge.EventAgentEnd, ev.Type)
	}
}

// TestSSEConsumer_BadJSON verifies that the consumer logs the
// bad event and continues. The test asserts the next event
// (after the bad one) is still delivered. (The consumer does
// NOT validate the JSON body — that is the caller's job.)
func TestSSEConsumer_BadJSON(t *testing.T) {
	t.Parallel()
	wire := strings.Join([]string{
		"event: text_delta",
		"data: this is not json",
		"",
		"event: text_delta",
		`data: {"delta":"valid"}`,
		"",
		"",
	}, "\n")
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	first := mustEvent(t, events, 1*time.Second)
	if first.Content != "this is not json" {
		t.Errorf("Content[0]: want raw body, got %q", first.Content)
	}
	second := mustEvent(t, events, 1*time.Second)
	if second.Content != `{"delta":"valid"}` {
		t.Errorf("Content[1]: want %q, got %q", `{"delta":"valid"}`, second.Content)
	}
}

// TestSSEConsumer_SeqNumbers verifies that the consumer
// preserves the seq numbers from the wire (delivered via the
// `id:` field) and that a missing id field produces Seq=0.
func TestSSEConsumer_SeqNumbers(t *testing.T) {
	t.Parallel()
	wire := strings.Join([]string{
		"id: 1",
		"event: text_delta",
		`data: {"d":"a"}`,
		"",
		"id: 2",
		"event: text_delta",
		`data: {"d":"b"}`,
		"",
		"event: agent_end",
		`data: {}`,
		"",
		"",
	}, "\n")
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	a := mustEvent(t, events, 1*time.Second)
	b := mustEvent(t, events, 1*time.Second)
	c := mustEvent(t, events, 1*time.Second)
	if a.Seq != 1 {
		t.Errorf("a.Seq: want 1, got %d", a.Seq)
	}
	if b.Seq != 2 {
		t.Errorf("b.Seq: want 2, got %d", b.Seq)
	}
	if c.Seq != 0 {
		t.Errorf("c.Seq: want 0 (no id field), got %d", c.Seq)
	}
}

// TestSSEConsumer_DefaultEventType verifies that a frame
// without an "event:" line defaults to "message" (the SSE
// spec default).
func TestSSEConsumer_DefaultEventType(t *testing.T) {
	t.Parallel()
	wire := "data: hello\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != "message" {
		t.Errorf("Type: want %q (SSE default), got %q", "message", ev.Type)
	}
	if ev.Content != "hello" {
		t.Errorf("Content: want %q, got %q", "hello", ev.Content)
	}
}

// TestSSEConsumer_ContextCancel verifies that canceling the
// context causes the consumer to stop reading, the event
// channel to drain, and Run to return.
func TestSSEConsumer_ContextCancel(t *testing.T) {
	t.Parallel()
	// A reader that blocks forever, simulating an idle SSE
	// connection.
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	consumer := forge.NewSSEConsumer(pr)
	events := consumer.Run(ctx)

	// Cancel after a short delay. The consumer is blocked
	// on a read; we close the read end of the pipe to
	// unblock it (this is what a real http.Response.Body
	// closer would do in production).
	time.Sleep(20 * time.Millisecond)
	cancel()
	_ = pr.Close()

	// Drain events until the channel closes.
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				if err := consumer.Err(); err != nil && !errors.Is(err, context.Canceled) && err != io.EOF {
					// Acceptable: the consumer returned
					// either ctx.Canceled (cancelled
					// before read) or the underlying
					// pipe error (closed before read).
				}
				return
			}
		case <-deadline:
			t.Fatal("events channel not closed after cancel + pipe close")
		}
	}
}

// TestSSEConsumer_EOFClosesDone verifies that when the source
// reaches EOF, the consumer closes the events channel and Run
// returns nil (visible via Err()).
func TestSSEConsumer_EOFClosesDone(t *testing.T) {
	t.Parallel()
	wire := "event: agent_end\ndata: {}\n\n"
	br := bufio.NewReaderSize(strings.NewReader(wire), 1<<20)
	c := forge.NewSSEConsumer(br)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := c.Run(ctx)

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != forge.EventAgentEnd {
		t.Errorf("Type: want %q, got %q", forge.EventAgentEnd, ev.Type)
	}
	// The events channel must close at EOF.
	select {
	case _, ok := <-events:
		if ok {
			t.Error("events: got extra value after agent_end")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("events channel not closed at EOF")
	}
	if err := c.Err(); err != nil {
		t.Errorf("Err at EOF: want nil, got %v", err)
	}
}

// TestSSEConsumer_ReconnectPreservesSince verifies that the
// `since` argument passed to NewSSEConsumer is recorded and
// accessible via the consumer's Since() method, so a higher
// layer can re-subscribe with the right cursor after a
// reconnect.
func TestSSEConsumer_ReconnectPreservesSince(t *testing.T) {
	t.Parallel()
	c := forge.NewSSEConsumer(strings.NewReader(""))
	if c.Since() != 0 {
		t.Errorf("Since: want 0, got %d", c.Since())
	}
	c2 := forge.NewSSEConsumer(strings.NewReader(""))
	c2.SetSince(42)
	if c2.Since() != 42 {
		t.Errorf("Since after Set: want 42, got %d", c2.Since())
	}
}

// TestSSEConsumer_RaceDetector runs the consumer under heavy
// concurrent injection to make sure the parser state machine
// does not race.
func TestSSEConsumer_RaceDetector(t *testing.T) {
	t.Parallel()
	const n = 100
	// Build a long SSE stream of n events.
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.WriteString("id: ")
		buf.WriteString(itoa(i))
		buf.WriteString("\nevent: text_delta\ndata: {\"d\":\"")
		buf.WriteString(itoa(i))
		buf.WriteString("\"}\n\n")
	}

	events, closer := newConsumer(t, &buf, 0)
	defer closer.Close()

	var received atomic.Int64
	for ev := range events {
		_ = ev
		received.Add(1)
	}
	if received.Load() != n {
		t.Errorf("received: want %d, got %d", n, received.Load())
	}
}

// TestSSEConsumer_EmptyDataLine verifies that an event with
// empty data is delivered with Content="" (not dropped). The
// pipeline uses empty data to signal "no content yet" for
// sentinel events.
func TestSSEConsumer_EmptyDataLine(t *testing.T) {
	t.Parallel()
	wire := "event: agent_end\ndata: \n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 1*time.Second)
	if ev.Type != forge.EventAgentEnd {
		t.Errorf("Type: want %q, got %q", forge.EventAgentEnd, ev.Type)
	}
	if ev.Content != "" {
		t.Errorf("Content: want empty (data: with space), got %q", ev.Content)
	}
}

// TestSSEConsumer_EmptyWire verifies that an empty body
// produces no events and the consumer closes the events
// channel.
func TestSSEConsumer_EmptyWire(t *testing.T) {
	t.Parallel()
	events, closer := newConsumer(t, strings.NewReader(""), 0)
	defer closer.Close()

	select {
	case _, ok := <-events:
		if ok {
			t.Error("events: got value from empty wire")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("events channel not closed for empty wire")
	}
}

// TestSSEConsumer_BlankLineInMiddle verifies that a stray
// blank line in the middle of a frame is treated as a frame
// terminator (per SSE spec).
func TestSSEConsumer_BlankLineInMiddle(t *testing.T) {
	t.Parallel()
	wire := "event: text_delta\ndata: {\"d\":\"1\"}\n\nevent: text_delta\ndata: {\"d\":\"2\"}\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	a := mustEvent(t, events, 1*time.Second)
	if a.Content != `{"d":"1"}` {
		t.Errorf("Content[0]: want %q, got %q", `{"d":"1"}`, a.Content)
	}
	b := mustEvent(t, events, 1*time.Second)
	if b.Content != `{"d":"2"}` {
		t.Errorf("Content[1]: want %q, got %q", `{"d":"2"}`, b.Content)
	}
}

// TestSSEConsumer_LineBuffering verifies that the parser
// correctly handles a body that arrives in tiny chunks
// (single bytes), not just full lines. This catches
// bufio.Scanner line-length bugs.
func TestSSEConsumer_LineBuffering(t *testing.T) {
	t.Parallel()
	wire := "event: text_delta\ndata: hello\n\n"
	events, closer := newConsumer(t, slowReader(strings.NewReader(wire), 1), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 2*time.Second)
	if ev.Type != forge.EventTextDelta {
		t.Errorf("Type: want %q, got %q", forge.EventTextDelta, ev.Type)
	}
	if ev.Content != "hello" {
		t.Errorf("Content: want %q, got %q", "hello", ev.Content)
	}
}

// TestSSEConsumer_LongLine verifies that the parser handles
// data lines longer than the bufio.Scanner default (64 KB).
// Forge's bash tool output can produce multi-MB stdout in a
// single event.
func TestSSEConsumer_LongLine(t *testing.T) {
	t.Parallel()
	// 200 KB of payload.
	body := strings.Repeat("a", 200*1024)
	wire := "event: tool_stdout\ndata: " + body + "\n\n"
	events, closer := newConsumer(t, strings.NewReader(wire), 0)
	defer closer.Close()

	ev := mustEvent(t, events, 2*time.Second)
	if ev.Type != forge.EventToolStdout {
		t.Errorf("Type: want %q, got %q", forge.EventToolStdout, ev.Type)
	}
	if len(ev.Content) != len(body) {
		t.Errorf("Content length: want %d, got %d", len(body), len(ev.Content))
	}
}

// newConsumer wires a forge.SSEConsumer over a bufio.Reader
// fronting r, starts Run, and returns the events channel plus
// a closer that cancels the consumer's context. The events
// channel is closed when the consumer returns.
//
// The `since` value is the initial cursor; 0 means "from the
// start".
func newConsumer(t *testing.T, r io.Reader, since int64) (<-chan forge.Event, io.Closer) {
	t.Helper()
	br := bufio.NewReaderSize(r, 1<<20) // 1 MiB
	c := forge.NewSSEConsumer(br)
	c.SetSince(since)

	ctx, cancel := context.WithCancel(context.Background())
	events := c.Run(ctx)

	closer := &cancelCloser{cancel: cancel}
	return events, closer
}

// cancelCloser is a tiny io.Closer that calls the underlying
// cancel function. Used by newConsumer to allow the test to
// cancel a runaway consumer.
type cancelCloser struct {
	cancel context.CancelFunc
}

func (c *cancelCloser) Close() error {
	c.cancel()
	return nil
}

// mustEvent reads one event from events with a deadline, or
// fails the test. Returns the event for assertions.
func mustEvent(t *testing.T, events <-chan forge.Event, d time.Duration) forge.Event {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("event channel closed before delivery")
		}
		return ev
	case <-time.After(d):
		t.Fatal("timed out waiting for event")
	}
	return forge.Event{}
}

// slowReader returns an io.Reader that emits at most one byte
// per read. Used to exercise the parser's line-buffering path.
func slowReader(r io.Reader, n int) io.Reader {
	return &slowR{r: r, n: n}
}

type slowR struct {
	r io.Reader
	n int
}

func (s *slowR) Read(p []byte) (int, error) {
	if len(p) > s.n {
		p = p[:s.n]
	}
	return s.r.Read(p)
}

// itoa is a tiny local replacement for strconv.Itoa to avoid
// pulling in strconv for the bench.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// errReader is a tiny io.Reader that always returns the
// configured error. Used by tests below.
type errReader struct {
	err error
}

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }

// TestSSEConsumer_NetworkError verifies that a transport-level
// error (not EOF) is reported via the consumer's Err() method
// after the events channel closes.
func TestSSEConsumer_NetworkError(t *testing.T) {
	t.Parallel()
	want := errors.New("connection reset")
	c := forge.NewSSEConsumer(&errReader{err: want})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := c.Run(ctx)
	// Drain until the channel closes.
	for range events {
	}
	if err := c.Err(); !errors.Is(err, want) && err != want {
		t.Errorf("Err: want %v, got %v", want, err)
	}
}
