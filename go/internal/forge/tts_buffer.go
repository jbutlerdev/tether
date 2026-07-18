// tts_buffer.go — per-session text buffering, sentence-boundary
// flush, and the TTS → Opus → radio emit path.
//
// Streaming semantics (research.md §5.3):
//
//   - Text deltas are buffered until a sentence boundary
//     (. ! ? or newline), at which point the buffered text is
//     handed to TTS. This avoids a TTS call per token, which
//     would be too slow and would also stutter the audio on
//     the M5.
//
//   - Tool output (bash stdout) is delivered one line at a
//     time. Each line goes through the same buffer+TTS path,
//     so the M5 hears the build output as it streams in.
//
//   - On agent_end, any buffered text is flushed. A TTS_END
//     marker is emitted as the last envelope in the burst so
//     the M5's EPD can clear its "playing" indicator.
//
// A stale-buffer force-flush timer (BufferFlushTimeout) covers
// the case of a long agent reply with no sentence boundary, so
// the SLA on first audio is still met.

package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// HandleSSETextDelta is a single-event entry point used by
// the production SSE consumer (when the consumer is wired
// outside the pipeline). It buffers the delta and emits TTS
// chunks on sentence boundaries. The session id is required.
func (p *Pipeline) HandleSSETextDelta(ctx context.Context, sessionID string, delta string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)
	return p.bufferAndFlush(ctx, convID, delta, false)
}

// HandleSSEToolStdout is the per-line entry point for the
// bash tool's streaming output. Each call adds the line to
// the per-session buffer and flushes on sentence boundary.
func (p *Pipeline) HandleSSEToolStdout(ctx context.Context, sessionID string, line string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)
	return p.bufferAndFlush(ctx, convID, line, false)
}

// HandleSSEAgentEnd flushes any buffered sentence for the
// given session and emits a TTS_END marker.
func (p *Pipeline) HandleSSEAgentEnd(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)
	if err := p.bufferAndFlush(ctx, convID, "", true); err != nil {
		return err
	}
	return p.sendTTSEnd(ctx, convID)
}

// HandleSSEToolCallStart sends a TTS prefix "running tool:
// <name>.".
func (p *Pipeline) HandleSSEToolCallStart(ctx context.Context, sessionID string, tool string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)
	text := fmt.Sprintf("running tool: %s.", tool)
	return p.speakAndSend(ctx, convID, text)
}

// HandleSSEToolCallEnd is a no-op (the "in tool" flag is
// reset by the next text_delta; we don't track it
// explicitly).
func (p *Pipeline) HandleSSEToolCallEnd(_ context.Context, _ string) error {
	return nil
}

// HandleSSEError speaks "agent error: <message>.".
func (p *Pipeline) HandleSSEError(ctx context.Context, sessionID string, message string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)
	text := fmt.Sprintf("agent error: %s.", message)
	return p.speakAndSend(ctx, convID, text)
}

// bufferAndFlush is the core text-delta handler. It appends
// delta to the per-session buffer and, on a sentence
// boundary (or force=true), flushes the buffered text to
// TTS → Opus → radio. force=true is set by HandleSSEAgentEnd.
func (p *Pipeline) bufferAndFlush(ctx context.Context, convID [16]byte, delta string, force bool) error {
	p.bufMu.Lock()
	buf, ok := p.buffers[convID]
	if !ok {
		buf = &sentenceBuffer{}
		p.buffers[convID] = buf
	}
	buf.append(delta)
	text, shouldFlush := buf.maybeFlush(p.boundChrs, force, p.flushTO)
	// If the buffer is non-empty after a successful flush
	// check, start a force-flush timer (only the first
	// append sets it; subsequent appends within the window
	// leave it running). The timer fires after
	// BufferFlushTimeout and force-flushes whatever is
	// still buffered, so a long agent reply without a
	// sentence boundary is still spoken within the SLA.
	if buf.text.Len() > 0 && buf.flushTimer == nil && p.flushTO > 0 {
		buf.flushTimer = time.AfterFunc(p.flushTO, func() {
			p.bufMu.Lock()
			if buf.text.Len() > 0 {
				out := buf.text.String()
				buf.text.Reset()
				buf.lastFlush = time.Now()
				buf.flushTimer = nil
				p.bufMu.Unlock()
				_ = p.speakAndSend(ctx, convID, out)
				return
			}
			buf.flushTimer = nil
			p.bufMu.Unlock()
		})
	} else if buf.text.Len() == 0 && buf.flushTimer != nil {
		// Buffer was flushed by a sentence boundary; cancel
		// the pending timer.
		buf.flushTimer.Stop()
		buf.flushTimer = nil
	}
	p.bufMu.Unlock()
	if !shouldFlush {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return p.speakAndSend(ctx, convID, text)
}

// speakAndSend is the workhorse: text → TTS → Opus → fragment
// → radio. It is used by every per-event handler that needs
// to emit spoken audio.
func (p *Pipeline) speakAndSend(ctx context.Context, convID [16]byte, text string) error {
	pcm, sr, err := p.cfg.TTS.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("forge: tts: %w", err)
	}
	_ = sr
	// Convert float32 PCM to int16 (Opus is int16 in our
	// abstraction).
	int16pcm := make([]int16, len(pcm))
	for i, s := range pcm {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		int16pcm[i] = int16(s * 32767)
	}
	// Encode each frame and accumulate the Opus bytes.
	frameSize := p.cfg.Codec.FrameSize()
	var opus []byte
	for off := 0; off < len(int16pcm); off += frameSize {
		end := off + frameSize
		if end > len(int16pcm) {
			// Pad with zeros for the trailing partial frame.
			pad := make([]int16, end-len(int16pcm))
			frame := append(int16pcm[off:], pad...)
			b, err := p.cfg.Codec.Encode(frame)
			if err != nil {
				return fmt.Errorf("forge: encode (padded): %w", err)
			}
			opus = append(opus, b...)
			break
		}
		b, err := p.cfg.Codec.Encode(int16pcm[off:end])
		if err != nil {
			return fmt.Errorf("forge: encode: %w", err)
		}
		opus = append(opus, b...)
	}
	// Send as a TTS_DATA envelope. The radio layer is
	// responsible for fragmentation, ACK, and retry.
	return p.sendTTS(ctx, convID, opus)
}

// sendTTS hands one TTS_DATA envelope to the radio. The
// ConversationId is a first-class field on the Envelope so the
// radio layer can route, fragment, and ACK it without parsing
// the payload.
func (p *Pipeline) sendTTS(ctx context.Context, convID [16]byte, payload []byte) error {
	env := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_TTS_DATA,
		ConversationId:  append([]byte(nil), convID[:]...),
		Payload:         payload,
	}
	return p.cfg.Radio.Send(ctx, env)
}

// sendTTSEnd hands one TTS_END envelope to the radio.
func (p *Pipeline) sendTTSEnd(ctx context.Context, convID [16]byte) error {
	env := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_TTS_END,
		ConversationId:  append([]byte(nil), convID[:]...),
	}
	return p.cfg.Radio.Send(ctx, env)
}

// sentenceBuffer accumulates text deltas and decides when
// to flush (sentence boundary or force). Plain string
// accumulation is fine — the per-session buffer is bounded
// in practice by the agent's reply length.
type sentenceBuffer struct {
	text strings.Builder
	// lastFlush is the time of the last flush. Used to
	// force-flush stale buffers.
	lastFlush time.Time
	// flushTimer is the pending force-flush timer, if any.
	// Set on the first append; cleared on a successful
	// flush (sentence boundary or force=true).
	flushTimer *time.Timer
}

func (b *sentenceBuffer) append(s string) {
	b.text.WriteString(s)
	if b.lastFlush.IsZero() {
		b.lastFlush = time.Now()
	}
}

// maybeFlush returns the buffered text (if a sentence
// boundary was seen or force is true) and a bool indicating
// whether the caller should proceed. When true, the buffer
// is reset.
func (b *sentenceBuffer) maybeFlush(boundaries string, force bool, flushTO time.Duration) (string, bool) {
	if force {
		out := b.text.String()
		b.text.Reset()
		b.lastFlush = time.Now()
		return out, true
	}
	// Find the latest sentence boundary.
	idx := -1
	for _, r := range boundaries {
		if i := strings.LastIndexByte(b.text.String(), byte(r)); i > idx {
			idx = i
		}
	}
	if idx < 0 {
		// No boundary yet. Check timeout.
		if !b.lastFlush.IsZero() && time.Since(b.lastFlush) > flushTO && b.text.Len() > 0 {
			out := b.text.String()
			b.text.Reset()
			b.lastFlush = time.Now()
			return out, true
		}
		return "", false
	}
	out := b.text.String()[:idx+1]
	remainder := b.text.String()[idx+1:]
	b.text.Reset()
	b.text.WriteString(remainder)
	b.lastFlush = time.Now()
	return out, true
}
