// Command tetherd is the Tether base-station daemon.
//
// It wires the full data plane into one process:
//
//	bridge serial ──► radio.Receiver ──► forge.Pipeline ──► forge SSE
//	   (M5 mic)         (reassemble)        (STT → POST)
//
//	forge SSE ──► Pipeline (TTS) ──► FragmentAndSend ──► bridge serial
//	   (agent reply)   (synthesize+encode)    (LoRa TX)      (M5 speaker)
//
//	conv.Store ──► conv.Sync ──► UI_UPDATE ──► bridge serial
//	   (mutations)     (watch)     (LoRa TX)        (M5 conv list)
//
// The daemon owns a single radio.Radio (the RAK4631 bridge transport).
// Both directions time-multiplex over it; on real hardware the radio is
// half-duplex, on the loopback/test transport the directions are
// independent. All external dependencies are interfaces, so the daemon
// is unit-testable end-to-end with in-process mocks (the same mocks the
// e2e simulator uses). main() constructs the daemon with mocks; the
// real serial/STT/TTS wiring lands behind build tags when the hardware
// paths are ready (research.md §3.1, §4.3, §5.5).
//
// This binary is the production-shaped counterpart to the e2e simulator
// (go/internal/e2e): where the simulator hand-wires two loopback pairs
// to model both ends, the daemon models only the base station and lets
// a test (or a real M5) drive the other end of the bridge.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/forge"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// DaemonConfig configures a Daemon. All fields are required unless
// noted; main() fills in the production values, tests fill in mocks.
type DaemonConfig struct {
	// Bridge is the LoRa radio transport (the RAK4631 bridge over
	// USB-serial). The daemon owns it for the lifetime of Run.
	Bridge radio.Radio
	// Store is the conversation store. The daemon writes forge session
	// rows here (via the pipeline) and the conv.Sync watches it.
	Store conv.Store
	// Forge is the forge HTTP + SSE client.
	Forge forge.Client
	// STT is the speech-to-text engine (Parakeet via sherpa-onnx cgo
	// in production; stt.Mock in tests).
	STT stt.Transcriber
	// TTS is the text-to-speech engine (Piper subprocess in
	// production; tts.Mock in tests).
	TTS tts.Synthesizer
	// Codec is the Opus encode/decode wrapper.
	Codec codec.Opus
	// Logger is the structured logger. Defaults to slog.Default().
	Logger *slog.Logger
	// AckTimeout is the per-ACK timeout for downlink Senders
	// (research.md §8.5: 2 s). Defaults to the Sender default.
	AckTimeout time.Duration
	// MaxRetry is the per-envelope retransmit budget (research.md
	// §8.5: 5). Defaults to the Sender default.
	MaxRetry int
	// SenderID is the base-station node id (e.g. 0x0002) for
	// UI_UPDATE envelopes.
	SenderID uint32
	// TargetID is the M5 node id (0xFFFF = broadcast) for UI_UPDATEs.
	TargetID uint32
}

// Daemon is the wired-up Tether base station. Construct with
// NewDaemon and call Run on a dedicated goroutine (or blocking in
// main). Run blocks until ctx is canceled.
type Daemon struct {
	cfg      DaemonConfig
	bridge   radio.Radio
	mux      *radio.Mux
	pipeline *forge.Pipeline
	sync     *conv.Sync
	store    conv.Store
	recv     *radio.Receiver
	logger   *slog.Logger

	// msgID is the monotonic downlink (TTS) message id. Uplink
	// message ids come from the M5; conv.Sync uses its own counter.
	msgID atomic.Uint32

	// ctx/cancel own the long-lived receiver + sync goroutines.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewDaemon wires the receiver, pipeline, and conv.Sync over the
// configured bridge radio. It does NOT start any goroutines — call
// Run for that.
func NewDaemon(cfg DaemonConfig) (*Daemon, error) {
	if cfg.Bridge == nil {
		return nil, errors.New("tetherd: nil bridge radio")
	}
	if cfg.Store == nil {
		return nil, errors.New("tetherd: nil store")
	}
	if cfg.Forge == nil {
		return nil, errors.New("tetherd: nil forge client")
	}
	if cfg.STT == nil || cfg.TTS == nil || cfg.Codec == nil {
		return nil, errors.New("tetherd: nil STT/TTS/codec")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	d := &Daemon{
		cfg:    cfg,
		bridge: cfg.Bridge,
		store:  cfg.Store,
		logger: logger,
		mux:    radio.NewMux(cfg.Bridge),
	}
	d.msgID.Store(1)

	// forge voice pipeline. Its Radio is the downlink adapter that
	// fragments each TTS_DATA envelope and runs a Sender over the
	// bridge's ACK sub-radio (research.md §5.4). Using the Mux's
	// AckRadio means the downlink Sender's Receive-based ACK loop
	// does not race the uplink Receiver for the bridge's single RX
	// path.
	d.pipeline = forge.NewPipeline(forge.PipelineConfig{
		Forge:  cfg.Forge,
		STT:    cfg.STT,
		TTS:    cfg.TTS,
		Codec:  cfg.Codec,
		Store:  cfg.Store,
		Radio:  &downlinkAdapter{d: d},
		Logger: logger,
	})

	// Uplink receiver: reassembles M5 mic fragments → pipeline. It
	// reads from the Mux's DataRadio so it only sees DATA/START/END,
	// not the downlink's ACKs. OnAck relays the cumulative-bitmap ACK
	// back over the bridge so the M5 sender can advance (research.md
	// §8.5/§8.6).
	d.recv = radio.NewReceiver(d.mux.DataRadio(),
		radio.ReceiverOptionOnMessage(d.handleUplink),
		radio.ReceiverOptionOnAck(d.handleUplinkAck),
		// 180 s: the messageTimeout is for abandoning truly stuck
		// messages, not slowly-transmitting ones. With §8.5's 2 s
		// ACK timeout, 5 retries, and up to ~36 chunks per message,
		// a lossy uplink can legitimately take minutes; a 5 s timeout
		// would sweep the reassembly state mid-transmit (silent data
		// loss — the sender gets per-chunk ACKs so it thinks it
		// delivered). 180 s covers the realistic worst case.
		radio.ReceiverOptionMessageTimeout(180*time.Second),
		radio.ReceiverOptionLogger(logger),
	)

	// conv.Sync: watch the store and push UI_UPDATEs to the M5.
	d.sync = conv.NewSync(conv.SyncConfig{
		Store:    cfg.Store,
		Radio:    cfg.Bridge, // PacketSender subset
		SenderID: cfg.SenderID,
		TargetID: cfg.TargetID,
		Logger:   logger,
	})

	return d, nil
}

// Run starts the radio demuxer, uplink receiver, and conv.Sync
// goroutines and blocks until ctx is canceled. It is safe to call
// once; calling twice panics.
func (d *Daemon) Run(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)
	defer d.cancel()
	defer d.mux.Close()

	errCh := make(chan error, 3)
	go func() { errCh <- d.mux.Run(d.ctx) }()
	go func() { errCh <- d.recv.Run(d.ctx) }()
	go func() { errCh <- d.sync.Run(d.ctx) }()

	d.logger.Info("tetherd: running")
	defer d.logger.Info("tetherd: stopped")

	// Block until the context is canceled. All three goroutines exit
	// on ctx.Done(); a cancel is not an error.
	for i := 0; i < 3; i++ {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			return fmt.Errorf("tetherd: %w", err)
		}
	}
	return nil
}

// Pipeline returns the forge voice pipeline. Callers (tests, the CLI)
// use it to create sessions and subscribe to SSE streams.
func (d *Daemon) Pipeline() *forge.Pipeline { return d.pipeline }

// handleUplink is the receiver's OnMessage: hand the reassembled Opus
// to the forge pipeline (STT → forge POST).
func (d *Daemon) handleUplink(msg *radio.IncomingMessage) {
	audio := &forge.IncomingAudio{
		ConversationID: append([]byte(nil), msg.ConversationID...),
		MessageID:      msg.MessageID,
		Payload:        append([]byte(nil), msg.Payload...),
		CompletedAt:    msg.CompletedAt,
	}
	if err := d.pipeline.HandleIncomingAudio(d.ctx, audio); err != nil {
		d.logger.Warn("tetherd: uplink handle", "err", err)
	}
}

// handleUplinkAck is the receiver's OnAck: relay the cumulative-bitmap
// ACK back over the bridge so the M5 sender can advance.
func (d *Daemon) handleUplinkAck(ack *radio.OutgoingAck) {
	ctx, cancel := context.WithTimeout(d.ctx, 500*time.Millisecond)
	defer cancel()
	if err := radio.SendAck(ctx, d.bridge, ack); err != nil {
		d.logger.Warn("tetherd: relay ack", "err", err)
	}
}

// downlinkAdapter implements forge.Radio. The pipeline hands it a
// TTS_DATA (or TTS_END) envelope; the adapter fragments the payload
// and runs a real radio.Sender over the bridge so the M5 receiver
// reassembles it with ACK + retransmit — the same transport the
// uplink uses (research.md §5.4).
type downlinkAdapter struct {
	d *Daemon
}

func (a *downlinkAdapter) Send(ctx context.Context, env *protocolpb.Envelope) error {
	if env.MsgType == protocolpb.MsgType_MSG_TYPE_TTS_END || len(env.Payload) == 0 {
		// TTS_END is a stream marker; it carries no audio. Emit it as
		// a single envelope so the M5 can clear its "playing"
		// indicator. (The current Receiver ignores non-DATA types, so
		// this is a forward-compat seam; the M5 firmware's radio_task
		// handles TTS_END directly.)
		marker := &protocolpb.Envelope{
			ProtocolVersion: 1,
			MsgType:         protocolpb.MsgType_MSG_TYPE_TTS_END,
			ConversationId:  append([]byte(nil), env.ConversationId...),
		}
		return a.d.bridge.Send(ctx, marker)
	}
	convID := make([]byte, len(env.ConversationId))
	copy(convID, env.ConversationId)
	// FragmentAndSend over the Mux's AckRadio so the downlink Sender
	// reads ACKs from the demuxer's ack channel (not the bridge's
	// shared RX path), avoiding the race with the uplink Receiver.
	return radio.FragmentAndSend(ctx, a.d.mux.AckRadio(), env.Payload, a.d.msgID.Add(1),
		convID, protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_TTS,
		a.d.cfg.AckTimeout, a.d.cfg.MaxRetry)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	// v1 wiring: in-process mocks. The real serial transport
	// (go.bug.st/serial → radio.Radio), real STT (sherpa-onnx cgo),
	// and real TTS (Piper subprocess) land behind build tags when the
	// hardware paths are ready; until then the daemon runs against
	// the same mocks the e2e simulator uses, so the binary builds and
	// the wiring is exercised by go test.
	store := conv.NewMemStore()
	fc := forge.NewMockClient()
	defer fc.Close()
	// v1 wiring: an in-process serial loopback stands in for the
	// RAK4631 bridge. The real transport (go.bug.st/serial → radio.Radio)
	// lands behind a build tag when the hardware path is ready; until
	// then the daemon runs against the same loopback the e2e simulator
	// uses, so the binary builds and the wiring is exercised by go test.
	bridge, _ := serial.NewLoopbackPair()
	d, err := NewDaemon(DaemonConfig{
		Bridge:     bridge,
		Store:      store,
		Forge:      fc,
		STT:        stt.NewMock(),
		TTS:        tts.NewMock(),
		Codec:      codec.NewMock(),
		Logger:     logger,
		AckTimeout: 2 * time.Second, // research.md §8.5
		MaxRetry:   5,
		SenderID:   0x0002,
		TargetID:   0xFFFF,
	})
	if err != nil {
		logger.Error("tetherd: init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.Run(ctx); err != nil {
		logger.Error("tetherd: run", "err", err)
		os.Exit(1)
	}
}
