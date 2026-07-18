// Package e2e is an end-to-end simulator that wires every Tether
// data-plane component into one in-process round trip and validates
// the full M5 ↔ base-station chain with mocked I/O.
//
// The simulator chains, in order:
//
//   - codec.Mock      (Opus encode/decode, identity)
//   - protocol.Fragment / Reassemble (LoRa chunking)
//   - radio.Sender / Receiver (per-chunk ACK, cumulative bitmap,
//     retransmit budget) over a serial.LoopbackPair with optional
//     packet loss
//   - the forge voice pipeline (incoming audio → STT → forge POST;
//     forge SSE → TTS → Opus → radio)
//   - conv.MemStore + the session↔conv_id mapping
//
// It exists because every component was unit-tested in isolation but
// never chained: this is both the regression net for the whole data
// plane and the skeleton for the eventual `tetherd` daemon (see
// REVIEW.md F1/F17). The simulator uses TWO loopback pairs (one per
// direction) so the uplink (M5 mic → PC STT) and downlink (PC TTS →
// M5 speaker) never share a receive queue; on real hardware the
// single radio time-multiplexes, but the data-plane behaviour under
// test is identical.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/forge"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Default per-ACK timeout and retry budget for the simulated radio.
// The mock loopback has no real air-time, so a small timeout keeps
// the test fast; under injected loss the retry budget is what gets
// exercised.
const (
	defaultAckTimeout = 200 * time.Millisecond
	defaultMaxRetry   = 10
)

// Simulator is the wired-up Tether data plane. Construct with
// NewSimulator and drive with RunUplink / RunDownlink. Close it to
// release the receiver goroutines and the forge mock.
type Simulator struct {
	// Radios: uplink (M5→PC) and downlink (PC→M5), one pair each.
	m5Up, pcUp         radio.Radio
	pcDown, m5Down     radio.Radio
	uplinkLoss, dnLoss serial.PacketLosser

	// Components.
	store    *conv.MemStore
	forge    *forge.MockClient
	sttEng   *stt.Mock
	ttsEng   *tts.Mock
	codec    *codec.Mock
	pipeline *forge.Pipeline

	// Per-direction captured output.
	mu          sync.Mutex
	downlinkPCM []int16 // reassembled+decoded TTS the M5 "speaker" got
	ttsEndSeen  bool
	uplinkMsgs  []string // transcribed texts the PC dispatched to forge

	// msgID is the monotonic LoRa message id shared across both
	// directions so no two bursts collide on (conv_id, msg_id).
	msgID atomic.Uint32

	// ackTimeout / maxRetry are the radio Sender parameters shared
	// by the uplink and downlink senders.
	ackTimeout time.Duration
	maxRetry   int

	// ctx/cancel own the long-lived receiver goroutines.
	ctx    context.Context
	cancel context.CancelFunc
}

// Option configures a Simulator.
type Option func(*simConfig)

type simConfig struct {
	ackTimeout time.Duration
	maxRetry   int
	logger     *slog.Logger
}

// OptionAckTimeout sets the per-ACK timeout for the simulated
// radio Senders. Defaults to 200ms.
func OptionAckTimeout(d time.Duration) Option {
	return func(c *simConfig) { c.ackTimeout = d }
}

// OptionMaxRetry sets the per-envelope retransmit budget.
func OptionMaxRetry(n int) Option {
	return func(c *simConfig) { c.maxRetry = n }
}

// OptionLogger installs a structured logger (default: discard).
func OptionLogger(l *slog.Logger) Option {
	return func(c *simConfig) { c.logger = l }
}

// NewSimulator wires every component and starts the two long-lived
// receiver goroutines (PC uplink receiver, M5 downlink receiver).
func NewSimulator(opts ...Option) (*Simulator, error) {
	cfg := simConfig{
		ackTimeout: defaultAckTimeout,
		maxRetry:   defaultMaxRetry,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(&cfg)
	}

	m5Up, pcUp := serial.NewLoopbackPair()
	pcDown, m5Down := serial.NewLoopbackPair()

	s := &Simulator{
		m5Up:       m5Up,
		pcUp:       pcUp,
		pcDown:     pcDown,
		m5Down:     m5Down,
		store:      conv.NewMemStore(),
		forge:      forge.NewMockClient(),
		sttEng:     stt.NewMock(),
		ttsEng:     tts.NewMock(),
		codec:      codec.NewMock(),
		ackTimeout: cfg.ackTimeout,
		maxRetry:   cfg.maxRetry,
	}
	s.msgID.Store(1)
	// The loopback sides implement serial.PacketLosser; capture them
	// so tests can inject loss per direction.
	if pl, ok := m5Up.(serial.PacketLosser); ok {
		s.uplinkLoss = pl
	}
	if pl, ok := pcDown.(serial.PacketLosser); ok {
		s.dnLoss = pl
	}

	s.ackTimeout = cfg.ackTimeout
	s.maxRetry = cfg.maxRetry

	s.ctx, s.cancel = context.WithCancel(context.Background())

	// forge voice pipeline. Its Radio is the downlink adapter that
	// fragments each TTS_DATA envelope and runs a Sender over pcDown.
	s.pipeline = forge.NewPipeline(forge.PipelineConfig{
		Forge:  s.forge,
		STT:    s.sttEng,
		TTS:    s.ttsEng,
		Codec:  s.codec,
		Store:  s.store,
		Radio:  &ttsAdapter{sim: s},
		Logger: cfg.logger,
	})

	// PC uplink receiver: reassembles mic fragments → pipeline.
	pcUpRecv := radio.NewReceiver(s.pcUp,
		radio.ReceiverOptionOnMessage(s.handleUplinkMessage),
		radio.ReceiverOptionOnAck(s.handleUplinkAck),
		radio.ReceiverOptionMessageTimeout(5*time.Second),
		radio.ReceiverOptionLogger(cfg.logger),
	)
	go func() { _ = pcUpRecv.Run(s.ctx) }()

	// M5 downlink receiver: reassembles TTS fragments → speaker.
	m5DownRecv := radio.NewReceiver(s.m5Down,
		radio.ReceiverOptionOnMessage(s.handleDownlinkMessage),
		radio.ReceiverOptionOnAck(s.handleDownlinkAck),
		radio.ReceiverOptionMessageTimeout(5*time.Second),
		radio.ReceiverOptionLogger(cfg.logger),
	)
	go func() { _ = m5DownRecv.Run(s.ctx) }()

	return s, nil
}

// Close stops the receiver goroutines and closes the forge mock.
// Idempotent.
func (s *Simulator) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.forge.Close()
}

// SetUplinkLoss injects a drop probability in [0,1] on the M5→PC
// direction. 0 disables loss.
func (s *Simulator) SetUplinkLoss(p float64) {
	if s.uplinkLoss != nil {
		s.uplinkLoss.SetPacketLoss(p)
	}
}

// SetDownlinkLoss injects a drop probability in [0,1] on the PC→M5
// direction.
func (s *Simulator) SetDownlinkLoss(p float64) {
	if s.dnLoss != nil {
		s.dnLoss.SetPacketLoss(p)
	}
}

// ForgeSendMessageCalls returns a snapshot of the forge client's
// recorded POST /messages calls (one per uplink transcript). Used
// by tests to assert the uplink delivered the transcript to the
// right session.
func (s *Simulator) ForgeSendMessageCalls() []forge.SendMessageCall {
	return s.forge.SendMessageCalls()
}

// NewConversation creates a forge session, subscribes the pipeline's
// SSE consumer to it, and returns the stable conversation id the M5
// uses on the wire. The conv.Store row is populated by the pipeline's
// ensureConvRow, so subsequent RunUplink calls can resolve conv→session.
func (s *Simulator) NewConversation(ctx context.Context) (sessionID string, convID [16]byte, err error) {
	sessionID, err = s.forge.CreateSession(ctx, "coder")
	if err != nil {
		return "", [16]byte{}, fmt.Errorf("e2e: create session: %w", err)
	}
	if err := s.pipeline.HandleSSESubscribe(ctx, sessionID); err != nil {
		return "", [16]byte{}, fmt.Errorf("e2e: subscribe: %w", err)
	}
	convID = forge.SessionToConvID16(sessionID)
	return sessionID, convID, nil
}

// RunUplink simulates the M5 capturing pcm (int16 mono @8kHz),
// encoding it to Opus, fragmenting it over LoRa with per-chunk ACK
// and retransmit, reassembling on the PC, running STT, and POSTing
// the transcript to the forge session bound to convID. It blocks
// until the forge client has recorded the SendMessage call.
func (s *Simulator) RunUplink(ctx context.Context, convID [16]byte, pcm []int16) error {
	opus, err := s.encodePCM(pcm)
	if err != nil {
		return fmt.Errorf("e2e: encode: %w", err)
	}
	envs, err := protocol.Fragment(opus, s.msgID.Add(1), convID[:],
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		return fmt.Errorf("e2e: fragment: %w", err)
	}
	if len(envs) == 0 {
		return errors.New("e2e: uplink produced no fragments")
	}
	sender := radio.NewSender(s.m5Up, envs,
		radio.SenderOptionTimeout(s.ackTimeout),
		radio.SenderOptionMaxRetry(s.maxRetry),
	)
	acked, failed, _, err := sender.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("e2e: uplink send: %w", err)
	}
	if failed != nil {
		return fmt.Errorf("e2e: uplink fragment exhausted retries: acked %d/%d", acked, len(envs))
	}
	// The receiver reassembled the message and dispatched it to the
	// pipeline; wait for the forge SendMessage to land.
	if err := s.waitForForgeMessage(ctx, convID, 3*time.Second); err != nil {
		return err
	}
	return nil
}

// RunDownlink simulates a forge agent reply: it injects a
// text_delta + agent_end into the forge SSE stream for sessionID,
// lets the pipeline buffer/flush/synthesize/encode/fragment, and
// waits for the M5 to reassemble and decode the TTS audio. It
// returns the int16 PCM the M5 "speaker" would play.
func (s *Simulator) RunDownlink(ctx context.Context, sessionID, text string) ([]int16, error) {
	// Reset downlink capture for this call.
	s.mu.Lock()
	s.downlinkPCM = nil
	s.ttsEndSeen = false
	s.mu.Unlock()

	if ok := s.forge.InjectEvent(sessionID, forge.Event{
		Type: forge.EventTextDelta, Content: `{"delta":"` + text + `"}`, Seq: 1, At: time.Now(),
	}); !ok {
		return nil, errors.New("e2e: inject text_delta failed (no consumer)")
	}
	if ok := s.forge.InjectEvent(sessionID, forge.Event{
		Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now(),
	}); !ok {
		return nil, errors.New("e2e: inject agent_end failed (no consumer)")
	}

	// Wait until the M5 has decoded the TTS payload (and ideally saw
	// the TTS_END marker). The PCM capture is what we return.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.mu.Lock()
		done := s.ttsEndSeen && len(s.downlinkPCM) > 0
		pcm := s.downlinkPCM
		s.mu.Unlock()
		if done {
			return pcm, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	// TTS_END may race with the last DATA fragment; accept non-empty
	// PCM even if the marker hasn't landed.
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.downlinkPCM) > 0 {
		return s.downlinkPCM, nil
	}
	return nil, errors.New("e2e: downlink timed out waiting for M5 PCM")
}

// handleUplinkMessage is the PC receiver's OnMessage: hand the
// reassembled Opus to the forge pipeline.
func (s *Simulator) handleUplinkMessage(msg *radio.IncomingMessage) {
	audio := &forge.IncomingAudio{
		ConversationID: append([]byte(nil), msg.ConversationID...),
		MessageID:      msg.MessageID,
		Payload:        append([]byte(nil), msg.Payload...),
		CompletedAt:    msg.CompletedAt,
	}
	// The pipeline resolves conv_id → session_id via the store.
	if err := s.pipeline.HandleIncomingAudio(s.ctx, audio); err != nil {
		// Best-effort; a failure here surfaces as a timeout in
		// RunUplink's waitForForgeMessage.
		return
	}
	s.mu.Lock()
	s.uplinkMsgs = append(s.uplinkMsgs, "ok")
	s.mu.Unlock()
}

// handleUplinkAck is the PC receiver's OnAck: turn the cumulative
// bitmap into a wire ACK envelope and send it back to the M5 sender.
// This is the path that exercises the 32-bit cumulative bitmap
// end-to-end (REVIEW.md F20).
func (s *Simulator) handleUplinkAck(ack *radio.OutgoingAck) {
	if err := s.sendAck(s.pcUp, ack); err != nil {
		_ = err
	}
}

// handleDownlinkMessage is the M5 receiver's OnMessage: decode the
// reassembled Opus payload to PCM and hand it to the "speaker".
func (s *Simulator) handleDownlinkMessage(msg *radio.IncomingMessage) {
	pcm, err := s.codec.Decode(msg.Payload)
	if err != nil || len(pcm) == 0 {
		return
	}
	s.mu.Lock()
	s.downlinkPCM = append(s.downlinkPCM, pcm...)
	s.mu.Unlock()
}

// handleDownlinkAck is the M5 receiver's OnAck: send the bitmap ACK
// back to the PC downlink sender.
func (s *Simulator) handleDownlinkAck(ack *radio.OutgoingAck) {
	if err := s.sendAck(s.m5Down, ack); err != nil {
		_ = err
	}
}

// markTTSEnd records that the pipeline emitted a TTS_END marker.
func (s *Simulator) markTTSEnd() {
	s.mu.Lock()
	s.ttsEndSeen = true
	s.mu.Unlock()
}

// sendAck serialises a Receiver OutgoingAck (cumulative bitmap) into
// a wire ACK envelope and transmits it over r, so the peer Sender can
// validate conv_id + msg_id (REVIEW.md F3) and advance its bitmap.
func (s *Simulator) sendAck(r radio.Radio, ack *radio.OutgoingAck) error {
	ctx, cancel := context.WithTimeout(s.ctx, 500*time.Millisecond)
	defer cancel()
	return radio.SendAck(ctx, r, ack)
}

// encodePCM frames pcm into codec.FrameSize chunks, Opus-encodes
// each (codec.Mock is an identity int16→LE-bytes), and concatenates.
// The final partial frame is zero-padded.
func (s *Simulator) encodePCM(pcm []int16) ([]byte, error) {
	frame := s.codec.FrameSize()
	if frame <= 0 {
		return nil, errors.New("e2e: codec frame size <= 0")
	}
	var out []byte
	for off := 0; off < len(pcm); off += frame {
		end := off + frame
		if end > len(pcm) {
			pad := make([]int16, end-len(pcm))
			f := append(append([]int16(nil), pcm[off:]...), pad...)
			b, err := s.codec.Encode(f)
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
			break
		}
		b, err := s.codec.Encode(pcm[off:end])
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// waitForForgeMessage polls the forge mock until it has a SendMessage
// call whose session maps to convID, or until timeout.
func (s *Simulator) waitForForgeMessage(ctx context.Context, convID [16]byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		list, err := s.store.List(ctx)
		if err == nil {
			for _, c := range list {
				if c.Info.Kind != conv.KindForge || !bytes.Equal(c.ID[:], convID[:]) {
					continue
				}
				for _, call := range s.forge.SendMessageCalls() {
					if call.SessionID == c.Info.Target && call.Text != "" {
						return nil
					}
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("e2e: timed out waiting for forge SendMessage on conv %x", convID)
}

// ttsAdapter implements forge.Radio. The pipeline hands it a
// TTS_DATA (or TTS_END) envelope; the adapter fragments the payload
// and runs a real radio.Sender over the downlink pair so the M5
// receiver reassembles it with ACK + retransmit — the same transport
// the uplink uses.
type ttsAdapter struct {
	sim *Simulator
}

func (a *ttsAdapter) Send(ctx context.Context, env *protocolpb.Envelope) error {
	if env.MsgType == protocolpb.MsgType_MSG_TYPE_TTS_END || len(env.Payload) == 0 {
		a.sim.markTTSEnd()
		return nil
	}
	convID := make([]byte, len(env.ConversationId))
	copy(convID, env.ConversationId)
	return radio.FragmentAndSend(ctx, a.sim.pcDown, env.Payload, a.sim.msgID.Add(1),
		convID, protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_TTS,
		a.sim.ackTimeout, a.sim.maxRetry)
}

// ackTimeout and maxRetry are configured per Simulator and read by
// the uplink/downlink Senders and the ttsAdapter.
