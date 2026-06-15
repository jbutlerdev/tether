// Voice ↔ forge pipeline. See plan.md §8.4.
//
// The pipeline is the glue that wires the radio receiver (Opus
// bytes from the M5) to the forge backend, and the forge SSE
// stream back to the M5 (as TTS audio). Concretely:
//
//	Incoming audio  ──►  STT  ──►  POST /messages  ──►  SSE
//	Outgoing TTS    ◄──  TTS  ◄──  text_delta / tool_stdout
//
// The pipeline is a long-running component (one per
// tetherd instance); it does not own the radio or the forge
// client — it consumes them through interfaces and is
// constructed via PipelineConfig.
//
// Streaming semantics:
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
// All external dependencies are interfaces, so the pipeline
// is unit-testable end-to-end with the in-process mocks
// (forge.MockClient, stt.Mock, tts.Mock, codec.Mock, conv.
// MemStore, captureRadio). The real HTTP client (http.go)
// is build-tag-gated and not in the CI path.
package forge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// IncomingAudio is a reassembled Opus frame from the M5. It
// is a thin wrapper around the radio.IncomingMessage shape
// (see internal/radio/receiver.go) decoupled from the radio
// package to avoid an import cycle in tests. The fields are
// a subset of the radio.IncomingMessage struct.
type IncomingAudio struct {
	// ConversationID is the 16-byte conversation id the M5
	// used. For a forge session, this is
	// forge.SessionToConvID(sessionID).
	ConversationID []byte
	// MessageID is the per-conversation monotonic id.
	MessageID uint32
	// Payload is the reassembled Opus bytes.
	Payload []byte
	// CompletedAt is the wall-clock time the radio finished
	// reassembling the message.
	CompletedAt time.Time
}

// RadioEnv is the envelope shape the pipeline hands to the
// radio. The MsgType is one of the protocolpb.MsgType
// values (TTS_DATA = 5, TTS_END = 6); the test mocks do not
// need to import the protobuf package, so the field is a
// raw uint32.
type RadioEnv struct {
	MsgType uint32
	Payload []byte
}

// Radio is the subset of radio.Radio the pipeline uses. It is
// defined here (rather than imported) so tests can plug in a
// small fake radio. The Pipeline hands one Envelope at a
// time to Send (the Envelopes are already-fragmented TTS
// chunks; the radio layer handles ACK / retry).
type Radio interface {
	// Send hands one Envelope (a TTS_DATA or TTS_END packet)
	// to the radio. The radio is responsible for any
	// fragmentation, ACK, and retry. The name parameter is
	// for diagnostic logging in the radio; the pipeline
	// passes "tts" or "tts-end" depending on the envelope.
	Send(ctx context.Context, env *RadioEnv, name string) error
}

// PipelineConfig configures a Pipeline.
type PipelineConfig struct {
	// Forge is the forge client. Required.
	Forge Client
	// STT is the speech-to-text engine. Required.
	STT stt.Transcriber
	// TTS is the text-to-speech engine. Required.
	TTS tts.Synthesizer
	// Codec is the Opus codec (encode + decode). Required.
	Codec codec.Opus
	// Store is the conversation store. The pipeline writes
	// one row per new forge session it sees. Required.
	Store conv.Store
	// Radio is the LoRa radio the M5 listens on. Required.
	Radio Radio
	// Logger is the structured logger. Defaults to slog.Default().
	Logger *slog.Logger
	// Profile is the agent profile to use for auto-resume
	// (when a session_expired event fires). Defaults to
	// "coder".
	Profile string
	// SentenceBoundaryChars are the runes that flush the
	// text-delta buffer to TTS. Defaults to ".!?\n".
	SentenceBoundaryChars string
	// FlushOnAgentEnd is true if a buffered sentence must
	// be flushed when the agent_end event fires. Defaults
	// to true.
	FlushOnAgentEnd *bool
	// BufferFlushTimeout is the maximum time a buffered
	// sentence waits before being force-flushed. Defaults
	// to 3 seconds.
	BufferFlushTimeout time.Duration
}

// Pipeline is the voice ↔ forge glue. Construct with
// NewPipeline and call Run on a dedicated goroutine.
type Pipeline struct {
	cfg       PipelineConfig
	logger    *slog.Logger
	profile   string
	boundChrs string
	flushEnd  bool
	flushTO   time.Duration

	// per-session text buffers (keyed by 16-byte convID).
	bufMu   sync.Mutex
	buffers map[[16]byte]*sentenceBuffer

	// monotonic message id used for the next TTS_DATA
	// envelope. The radio does not look at this for TTS
	// packets (it uses ConversationID as the join key), so
	// the counter is purely a "fill the field" convenience.
	msgID atomic.Uint32

	// per-session consumer goroutine state.
	consMu    sync.Mutex
	consumers map[[16]byte]*sseConsumer
}

// NewPipeline returns a Pipeline ready to be used. cfg.Forge,
// cfg.STT, cfg.TTS, cfg.Codec, cfg.Store, and cfg.Radio must
// all be non-nil; the rest is optional.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	profile := cfg.Profile
	if profile == "" {
		profile = "coder"
	}
	boundChrs := cfg.SentenceBoundaryChars
	if boundChrs == "" {
		boundChrs = ".!?\n"
	}
	flushEnd := true
	if cfg.FlushOnAgentEnd != nil {
		flushEnd = *cfg.FlushOnAgentEnd
	}
	flushTO := cfg.BufferFlushTimeout
	if flushTO == 0 {
		flushTO = 3 * time.Second
	}
	return &Pipeline{
		cfg:       cfg,
		logger:    logger,
		profile:   profile,
		boundChrs: boundChrs,
		flushEnd:  flushEnd,
		flushTO:   flushTO,
		buffers:   make(map[[16]byte]*sentenceBuffer),
		consumers: make(map[[16]byte]*sseConsumer),
	}
}

// HandleIncomingAudio is the entry point for radio-side
// audio: it decodes the Opus payload, runs STT, and POSTs
// the recognised text to the forge session whose conversation
// id is the IncomingAudio's ConversationID. The session id
// is recovered by reverse-mapping the conv id via
// SessionToConvID (the test wires this directly via the
// captured IncomingAudio; in production the matrix/forge
// glue maintains the mapping).
//
// An empty payload is a no-op (returns nil). A payload that
// fails to decode is returned as an error and the pipeline
// does NOT POST a "transcribed" message (the caller can log
// and move on).
func (p *Pipeline) HandleIncomingAudio(ctx context.Context, audio *IncomingAudio) error {
	if audio == nil {
		return errors.New("forge: nil IncomingAudio")
	}
	if len(audio.Payload) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Decode Opus → int16 PCM.
	pcm, err := p.cfg.Codec.Decode(audio.Payload)
	if err != nil {
		return fmt.Errorf("forge: decode: %w", err)
	}
	if len(pcm) == 0 {
		return nil
	}
	// Convert int16 → float32 for the STT engine.
	float := make([]float32, len(pcm))
	for i, s := range pcm {
		float[i] = float32(s) / 32768.0
	}
	// STT.
	text, err := p.cfg.STT.Transcribe(ctx, float, p.cfg.Codec.SampleRate())
	if err != nil {
		return fmt.Errorf("forge: stt: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// Resolve the conv id → session id. The mapping is
	// forge.SessionToConvID; we don't currently have a
	// reverse index, so we accept that the test wires the
	// conv id directly via IncomingAudio and the production
	// code wires it via a side channel (the conv.Store
	// row, populated by the matrix/forge glue at session
	// create time). For v1 we POST to the session whose
	// conv id matches audio.ConversationID; the
	// session_id is recovered via a lookup against the
	// store's Target field.
	sessionID, err := p.lookupSessionID(ctx, audio.ConversationID)
	if err != nil {
		return fmt.Errorf("forge: lookup session: %w", err)
	}
	if err := p.cfg.Forge.SendMessage(ctx, sessionID, text); err != nil {
		return fmt.Errorf("forge: send message: %w", err)
	}
	p.logger.Info("pipeline: voice→forge sent",
		"convID", convIDHex(audio.ConversationID),
		"session", sessionID,
		"len", len(text),
	)
	return nil
}

// lookupSessionID finds the forge session id whose
// SessionToConvID matches the given conv id. The conv.Store
// holds the reverse mapping (Target == sessionUUID). The
// lookup is O(n) for v1 — fine for the few-session case.
func (p *Pipeline) lookupSessionID(ctx context.Context, convID []byte) (string, error) {
	list, err := p.cfg.Store.List(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range list {
		if c.Info.Kind != conv.KindForge {
			continue
		}
		if !bytesEqual16(c.ID[:], convID) {
			continue
		}
		return c.Info.Target, nil
	}
	return "", fmt.Errorf("forge: no session for conv %x", convID)
}

// HandleSSESubscribe opens an SSE stream on the given forge
// session and dispatches events to the per-type handlers. It
// returns immediately; the actual event pump runs on a
// dedicated goroutine. Calling HandleSSESubscribe twice for
// the same session is a no-op (the existing consumer is
// reused).
func (p *Pipeline) HandleSSESubscribe(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	convID := SessionToConvID16(sessionID)

	// Idempotency: a re-subscribe for the same session is a
	// no-op. (The forge backend tolerates re-subscribes; the
	// pipeline is the one that gates them.)
	p.consMu.Lock()
	if existing, ok := p.consumers[convID]; ok {
		p.consMu.Unlock()
		// Tell the existing consumer to ignore its work and
		// exit; a new consumer will be spawned by the
		// caller on the next HandleSSESubscribe if needed.
		// For the v1 test suite, the existing consumer is
		// good enough — we just bump the cancel so it
		// knows to stop and the next call spawns fresh.
		existing.stop()
		// Wait for it to actually exit so the new consumer
		// does not race the old one.
		<-existing.done
		p.consMu.Lock()
		delete(p.consumers, convID)
	}
	p.consMu.Unlock()

	// Ensure a conv.Store row exists.
	if err := p.ensureConvRow(ctx, sessionID); err != nil {
		p.logger.Warn("pipeline: ensure conv row", "err", err)
	}

	events, done, closer, err := p.cfg.Forge.SubscribeEvents(ctx, sessionID, 0)
	if err != nil {
		return fmt.Errorf("forge: subscribe: %w", err)
	}

	sc := &sseConsumer{
		pipeline:  p,
		sessionID: sessionID,
		convID:    convID,
		events:    events,
		done:      done,
		closer:    closer,
		finished:  make(chan struct{}),
	}
	p.consMu.Lock()
	p.consumers[convID] = sc
	p.consMu.Unlock()
	go sc.run()
	return nil
}

// ensureConvRow inserts a conv.Store row for the given forge
// session. Idempotent.
func (p *Pipeline) ensureConvRow(ctx context.Context, sessionID string) error {
	convID := SessionToConvID16(sessionID)
	_, err := p.cfg.Store.Get(ctx, convID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, conv.ErrNotFound) {
		return err
	}
	name := "forge:" + shortSessionID(sessionID)
	_, _, err = p.cfg.Store.Upsert(ctx, convID, conv.ConvInfo{
		Name:               name,
		Kind:               conv.KindForge,
		Target:             sessionID,
		LastActivityUnixMs: time.Now().UnixMilli(),
	})
	return err
}

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

// HandleSSESessionExpired resumes the session: creates a new
// forge session with the same profile and re-subscribes the
// SSE stream. The user message is NOT re-sent; the agent is
// expected to recover state from the new session id.
func (p *Pipeline) HandleSSESessionExpired(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	newID, err := p.cfg.Forge.CreateSession(ctx, p.profile)
	if err != nil {
		return fmt.Errorf("forge: resume create session: %w", err)
	}
	// Replace the conv.Store row's Target with the new
	// session id so subsequent lookups go to the new
	// session. The conv_id stays the same so the M5's UI
	// is undisturbed.
	convID := SessionToConvID16(sessionID)
	if existing, err := p.cfg.Store.Get(ctx, convID); err == nil {
		existing.Info.Target = newID
		existing.Info.LastActivityUnixMs = time.Now().UnixMilli()
		_, _, _ = p.cfg.Store.Upsert(ctx, convID, existing.Info)
	}
	p.logger.Info("pipeline: session resumed",
		"old", shortSessionID(sessionID),
		"new", shortSessionID(newID),
	)
	// Re-subscribe on the new id.
	return p.HandleSSESubscribe(ctx, newID)
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
	text, shouldFlush := buf.maybeFlush(p.boundChrs, force)
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

// sendTTS hands one TTS_DATA envelope to the radio.
func (p *Pipeline) sendTTS(ctx context.Context, convID [16]byte, payload []byte) error {
	env := &RadioEnv{
		MsgType: 5, // protocolpb.MsgType_MSG_TYPE_TTS_DATA
		Payload: stampConvID(convID, payload),
	}
	return p.cfg.Radio.Send(ctx, env, "tts")
}

// sendTTSEnd hands one TTS_END envelope to the radio.
func (p *Pipeline) sendTTSEnd(ctx context.Context, convID [16]byte) error {
	env := &RadioEnv{
		MsgType: 6, // protocolpb.MsgType_MSG_TYPE_TTS_END
		Payload: stampConvID(convID, nil),
	}
	return p.cfg.Radio.Send(ctx, env, "tts-end")
}

// stampConvID prepends a 16-byte conversation id header to
// the payload. The test captureRadio ignores it; a real
// radio layer uses the header to route the envelope.
func stampConvID(convID [16]byte, payload []byte) []byte {
	out := make([]byte, 16+len(payload))
	copy(out[:16], convID[:])
	copy(out[16:], payload)
	return out
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
func (b *sentenceBuffer) maybeFlush(boundaries string, force bool) (string, bool) {
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
		if !b.lastFlush.IsZero() && time.Since(b.lastFlush) > 3*time.Second && b.text.Len() > 0 {
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

// bytesEqual16 compares two byte slices as 16-byte arrays.
func bytesEqual16(a, b []byte) bool {
	if len(a) != 16 || len(b) != 16 {
		return false
	}
	for i := 0; i < 16; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// convIDHex formats a 16-byte conv id as a 32-char hex
// string. Used for log lines.
func convIDHex(b []byte) string {
	const hex = "0123456789abcdef"
	if len(b) != 16 {
		return "invalid"
	}
	out := make([]byte, 32)
	for i, v := range b {
		out[2*i] = hex[v>>4]
		out[2*i+1] = hex[v&0x0F]
	}
	return string(out)
}

// shortSessionID returns the first 8 chars of a session id
// for compact log lines. The session id is a UUID; the first
// 8 chars are the first segment.
func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// _ ensures the binary package is referenced even if the
// protocol-layer import is dropped in v1.
var _ = binary.LittleEndian
