// Package forge implements the voice ↔ forge agent pipeline.
//
// The pipeline is the glue that wires the radio receiver (Opus
// bytes from the M5) to the forge backend, and the forge SSE
// stream back to the M5 (as TTS audio). Concretely:
//
//	Incoming audio  ──►  STT  ──►  POST /messages  ──►  SSE
//	Outgoing TTS    ◄──  TTS  ◄──  text_delta / tool_stdout
//
// The pipeline is a long-running component (one per tetherd
// instance); it does not own the radio or the forge client — it
// consumes them through interfaces and is constructed via
// PipelineConfig.
//
// The pipeline's responsibilities are split across four files:
//
//   - pipeline.go      — the Pipeline struct, config, and the
//     incoming-audio → STT → forge POST path.
//   - subscribe.go     — SSE subscription lifecycle (subscribe,
//     stop-and-replace, session resume).
//   - tts_buffer.go    — per-session text buffering, sentence-
//     boundary flush, and TTS → Opus → radio.
//   - sse_consumer.go  — the per-session SSE event pump that
//     dispatches each event type to its handler.
//
// All external dependencies are interfaces, so the pipeline is
// unit-testable end-to-end with the in-process mocks (forge.
// MockClient, stt.Mock, tts.Mock, codec.Mock, conv.MemStore,
// captureRadio). The real HTTP client (http.go) is build-tag-gated
// and not in the CI path.
package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
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

// Radio is the subset of radio.Radio the pipeline uses. The
// Pipeline hands one protocolpb.Envelope at a time to Send (a
// TTS_DATA or TTS_END packet with the ConversationId set); the
// radio layer is responsible for fragmentation, ACK, and retry.
// Defining the interface here keeps the forge package decoupled
// from internal/radio (which imports the protocol package that
// tests want to stub).
type Radio interface {
	Send(ctx context.Context, env *protocolpb.Envelope) error
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
		"convID", conv.ConvIDToHex(audio.ConversationID),
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
		if !bytes.Equal(c.ID[:], convID) {
			continue
		}
		return c.Info.Target, nil
	}
	return "", fmt.Errorf("forge: no session for conv %x", convID)
}
