// sse_consumer.go — the per-session SSE event pump.
//
// One sseConsumer runs per active forge session. It owns the
// forge.SubscribeEvents return values (the events channel, the
// done signal, and the closer) and dispatches each event to the
// per-type handler in tts_buffer.go / subscribe.go. The dispatch
// decodes the event's JSON content into a small per-type struct
// and forwards the typed fields; unknown event types are dropped
// silently.
//
// The consumer exits when the events channel closes or when
// stop() is called (which closes the closer, ending the
// subscription). subscribe.go's stop-and-replace logic awaits
// the consumer's `finished` channel before spawning a new one.

package forge

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// sseConsumer is the per-session event pump. It owns the
// forge.SubscribeEvents return values and dispatches each
// event to the right per-type handler.
type sseConsumer struct {
	pipeline  *Pipeline
	sessionID string
	convID    [16]byte
	events    <-chan Event
	done      <-chan struct{}
	closer    io.Closer
	finished  chan struct{}
	stopOnce  sync.Once
	cancel    context.CancelFunc
}

// run pumps events until the subscription ends. The
// pipeline's Stop method is not exposed; the consumer
// exits when the events channel closes or when stop() is
// called (which closes the closer, which closes the
// subscription).
func (c *sseConsumer) run() {
	defer close(c.finished)
	defer func() { _ = c.closer.Close() }()
	for ev := range c.events {
		c.dispatch(ev)
	}
}

// stop signals the consumer to exit. Idempotent.
func (c *sseConsumer) stop() {
	c.stopOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		_ = c.closer.Close()
	})
}

// done is an alias for the finished channel; tests can
// select on either.
func (c *sseConsumer) done_() <-chan struct{} { return c.finished }

// dispatch routes one event to the right handler. The
// session-scoped context is recreated per call so a single
// bad event cannot poison the pump.
func (c *sseConsumer) dispatch(ev Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch ev.Type {
	case EventTextDelta:
		var d struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(ev.Content), &d); err != nil {
			c.pipeline.logger.Warn("pipeline: text_delta json", "err", err)
			return
		}
		if err := c.pipeline.HandleSSETextDelta(ctx, c.sessionID, d.Delta); err != nil {
			c.pipeline.logger.Warn("pipeline: text_delta", "err", err)
		}
	case EventToolCallStart:
		var d struct {
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal([]byte(ev.Content), &d)
		if err := c.pipeline.HandleSSEToolCallStart(ctx, c.sessionID, d.Tool); err != nil {
			c.pipeline.logger.Warn("pipeline: tool_start", "err", err)
		}
	case EventToolCallEnd:
		if err := c.pipeline.HandleSSEToolCallEnd(ctx, c.sessionID); err != nil {
			c.pipeline.logger.Warn("pipeline: tool_end", "err", err)
		}
	case EventToolStdout:
		var d struct {
			Line string `json:"line"`
		}
		_ = json.Unmarshal([]byte(ev.Content), &d)
		if err := c.pipeline.HandleSSEToolStdout(ctx, c.sessionID, d.Line); err != nil {
			c.pipeline.logger.Warn("pipeline: tool_stdout", "err", err)
		}
	case EventAgentEnd:
		if err := c.pipeline.HandleSSEAgentEnd(ctx, c.sessionID); err != nil {
			c.pipeline.logger.Warn("pipeline: agent_end", "err", err)
		}
	case EventError:
		var d struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(ev.Content), &d)
		if err := c.pipeline.HandleSSEError(ctx, c.sessionID, d.Message); err != nil {
			c.pipeline.logger.Warn("pipeline: error", "err", err)
		}
	case EventSessionExpired:
		if err := c.pipeline.HandleSSESessionExpired(ctx, c.sessionID); err != nil {
			c.pipeline.logger.Warn("pipeline: session_expired", "err", err)
		}
	default:
		// Unknown event types are dropped silently.
	}
}
