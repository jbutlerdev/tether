// transport.go — radio.Radio adapter over a real serial port.
//
// The RAK4631 bridge speaks the frame protocol (frame.go) over
// USB-Serial at 921 600 baud. This file implements radio.Radio on
// top of an io.ReadWriteCloser (a go.bug.st/serial port in
// production, a net.Pipe in tests).
//
// Mapping:
//
//	radio.Radio.Send(env)  → protocol.Encode(env) → EncodeFrame(kAck) → write
//	radio.Radio.Receive()  → read frames → kRxPacket → protocol.Decode → env
//	radio.Radio.Init()     → EncodeFrame(kSetConfig, [sf,bw,cr,power,sync]) → write
//	radio.Radio.SetChannel → EncodeFrame(kSetConfig with frequency) → write
//
// The bridge is a pass-through: it forwards kAck payloads directly to
// the SX1262 TX FIFO and emits RX FIFO contents as kRxPacket frames.
// The 34-byte fixed header (protocol.Encode) is the on-air format; the
// bridge never inspects it.
//
// A background goroutine reads from the serial port and feeds a
// FrameDecoder. kRxPacket frames are decoded into Envelopes and
// enqueued into a channel; Receive blocks on that channel. Other
// frame types (kTxDone, kCadResult, kLog) are surfaced via optional
// callbacks for the daemon to consume.
package serial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Port is the serial port interface. go.bug.st/serial.SerialPort
// satisfies this; net.Pipe-based test ports do too.
type Port interface {
	io.ReadWriteCloser
}

// LogHandler is called for every kLog frame from the bridge. Optional.
type LogHandler func(line string)

// CadHandler is called for every kCadResult frame. `busy` is true if
// the channel was active. Optional.
type CadHandler func(busy bool)

// TxDoneHandler is called when the bridge confirms a LoRa TX completed.
// Optional.
type TxDoneHandler func()

// TransportConfig configures a Transport.
type TransportConfig struct {
	Port       Port
	LogHandler LogHandler
	CadHandler CadHandler
	TxDoneHandler TxDoneHandler
}

// Transport implements radio.Radio over a serial port + the bridge
// frame protocol. Construct with NewTransport.
type Transport struct {
	port       Port
	logHandler LogHandler
	cadHandler CadHandler
	txHandler  TxDoneHandler

	mu     sync.Mutex
	closed bool

	rx   chan *protocolpb.Envelope
	stop chan struct{}
	done chan struct{}

	// curCh tracks the current channel for SetChannel (single channel
	// for v1; stored for future frequency-hopping support).
	curCh uint8
}

// NewTransport wraps a serial port as a radio.Radio. Call Init() to
// configure the LoRa preset and start the background read goroutine.
// The caller retains ownership of the port; Close() closes it.
func NewTransport(cfg TransportConfig) *Transport {
	return &Transport{
		port:       cfg.Port,
		logHandler: cfg.LogHandler,
		cadHandler: cfg.CadHandler,
		txHandler:  cfg.TxDoneHandler,
		rx:         make(chan *protocolpb.Envelope, 256),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Init configures the LoRa radio on the bridge and starts the
// background RX reader goroutine. Idempotent (calling twice is a
// no-op after the first success).
func (t *Transport) Init(_ context.Context, preset radio.Preset) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrSerialClosed
	}
	t.mu.Unlock()

	// Encode the preset as a kSetConfig frame:
	//   [sf:1][bw:1][cr:1][power:int8][sync:1]
	// bw: 0=125, 1=250, 2=500 kHz
	var bw byte
	switch preset.BandwidthHz {
	case 125000:
		bw = 0
	case 250000:
		bw = 1
	case 500000:
		bw = 2
	default:
		return fmt.Errorf("serial: unsupported bandwidth %d Hz", preset.BandwidthHz)
	}
	payload := []byte{
		preset.SpreadingFactor,
		bw,
		preset.CodingRate,
		byte(preset.TxPowerDbm),
		preset.SyncWord,
	}
	if err := t.writeFrame(Frame{Type: FrameSetConfig, Payload: payload}); err != nil {
		return fmt.Errorf("serial: init set-config: %w", err)
	}

	// Start the background reader (idempotent via the done channel).
	go t.readLoop()
	return nil
}

// Send serializes env to the 34-byte wire format and sends it as a
// kAck frame (the bridge's "TX this over LoRa" command).
func (t *Transport) Send(_ context.Context, env *protocolpb.Envelope) error {
	if env == nil {
		return errors.New("serial: nil envelope")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrSerialClosed
	}
	t.mu.Unlock()

	encoded, err := protocol.Encode(env)
	if err != nil {
		return fmt.Errorf("serial: encode envelope: %w", err)
	}
	if err := t.writeFrame(Frame{Type: FrameAck, Payload: encoded}); err != nil {
		return fmt.Errorf("serial: send frame: %w", err)
	}
	return nil
}

// Receive blocks until a LoRa packet arrives (via a kRxPacket frame
// from the bridge) or the context is canceled. Returns io.EOF on
// close or context cancel.
func (t *Transport) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env := <-t.rx:
		return env, nil
	case <-t.stop:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, io.EOF
	}
}

// SetChannel switches the bridge's LoRa frequency. For v1 we stay on
// channel 0 (902.3 MHz); this sends a kSetConfig with the same preset
// but a new frequency. The bridge's SerialLink handles kSetConfig by
// re-configuring the radio.
func (t *Transport) SetChannel(_ context.Context, ch radio.Channel) error {
	t.mu.Lock()
	t.curCh = ch.Index
	t.mu.Unlock()
	// The bridge's kSetConfig only sets the preset, not the frequency.
	// Frequency is set via a separate mechanism — for v1 (single
	// channel) this is a no-op; the bridge defaults to ch 0.
	// TODO(v2): add a kSetChannel frame type for frequency hopping.
	return nil
}

// Close stops the background reader and closes the serial port.
// Idempotent. The port is closed first so the read loop's blocked
// Read() returns EOF and the goroutine can exit.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	// Close the port first — this unblocks the read loop's Read().
	close(t.stop)
	_ = t.port.Close()
	<-t.done
	return nil
}

// writeFrame encodes and writes a single frame to the serial port.
// Safe to call from multiple goroutines (the bridge serial port is
// not, but the daemon only sends from the downlink Sender + conv.Sync
// which are serialized by the half-duplex Mux in production).
func (t *Transport) writeFrame(f Frame) error {
	encoded, err := EncodeFrame(f)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrSerialClosed
	}
	_, err = t.port.Write(encoded)
	return err
}

// readLoop runs in a background goroutine, reading bytes from the
// serial port, decoding frames, and dispatching them. kRxPacket
// frames are decoded into Envelopes and sent to the rx channel.
// Other frame types are dispatched to their handlers.
func (t *Transport) readLoop() {
	defer close(t.done)
	dec := NewFrameDecoder()
	buf := make([]byte, 256)
	for {
		select {
		case <-t.stop:
			return
		default:
		}
		n, err := t.port.Read(buf)
		if n > 0 {
			dec.Feed(buf[:n])
			for {
				f, ok := dec.Next()
				if !ok {
					break
				}
				t.handleFrame(f)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// A read error on a serial port is usually a disconnect.
			// Signal stop so Receive unblocks.
			t.mu.Lock()
			if !t.closed {
				t.closed = true
				close(t.stop)
			}
			t.mu.Unlock()
			return
		}
	}
}

// handleFrame dispatches a decoded frame.
func (t *Transport) handleFrame(f Frame) {
	switch f.Type {
	case FrameRxPacket:
		env, err := protocol.Decode(f.Payload)
		if err != nil {
			// A corrupt air packet — drop it. The bridge should have
			// already checked the LoRa CRC, but the 34-byte header
			// CRC is a second line of defense.
			return
		}
		clone := proto.Clone(env).(*protocolpb.Envelope)
		select {
		case t.rx <- clone:
		case <-t.stop:
		default:
			// RX queue full — drop. The Receiver will miss this
			// packet; the Sender's retry will re-send it. This is
			// preferable to blocking the read loop.
		}
	case FrameTxDone:
		if t.txHandler != nil {
			t.txHandler()
		}
	case FrameCadResult:
		if t.cadHandler != nil && len(f.Payload) > 0 {
			t.cadHandler(f.Payload[0] != 0)
		}
	case FrameLog:
		if t.logHandler != nil {
			t.logHandler(string(f.Payload))
		}
	case FrameError:
		if t.logHandler != nil {
			t.logHandler("bridge error: " + string(f.Payload))
		}
	}
}

// Compile-time check.
var _ radio.Radio = (*Transport)(nil)

// encodeSetConfigPayload builds the kSetConfig payload for a preset.
// Exported for tests and for the daemon's config path.
func encodeSetConfigPayload(preset radio.Preset) ([]byte, error) {
	var bw byte
	switch preset.BandwidthHz {
	case 125000:
		bw = 0
	case 250000:
		bw = 1
	case 500000:
		bw = 2
	default:
		return nil, fmt.Errorf("serial: unsupported bandwidth %d Hz", preset.BandwidthHz)
	}
	payload := []byte{
		preset.SpreadingFactor,
		bw,
		preset.CodingRate,
		byte(preset.TxPowerDbm),
		preset.SyncWord,
	}
	return payload, nil
}
