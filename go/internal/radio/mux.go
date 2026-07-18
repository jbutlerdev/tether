// mux.go — half-duplex radio demuxer.
//
// A real Tether base station has ONE LoRa radio (the RAK4631 bridge).
// Both directions time-multiplex over it: the uplink Receiver reads M5
// mic DATA, and the downlink Sender reads ACKs for the TTS fragments it
// is transmitting. If both call Radio.Receive on the same radio they
// race on a single RX path — an ACK can be consumed by the uplink
// Receiver (which ignores it) and a DATA chunk by the downlink Sender
// (which ignores it), so neither side makes progress.
//
// Mux solves this with a single reader goroutine that sorts every
// incoming envelope by MsgType into two logical sub-radios:
//
//   - DataRadio(): Receive returns DATA/START/END/TTS_* envelopes (the
//     uplink Receiver reassembles these).
//   - AckRadio():  Receive returns ACK envelopes (the downlink Sender
//     consumes these to advance its bitmap).
//
// Both sub-radios share the underlying radio's Send (TX is not
// contended — the air is serialised by the radio itself) and its
// Init/SetChannel/Close. The Mux's Run is the single reader; call it
// on a dedicated goroutine for the lifetime of the radio.
//
// This is the production-shaped single-radio model: the e2e simulator
// uses two loopback pairs instead because it models both ends, but the
// daemon and a real M5 each have one radio and need a Mux to share it
// between their Sender and Receiver.
package radio

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Mux demuxes a single radio.Radio into a DataRadio and an AckRadio by
// MsgType. Construct with NewMux, call Run on a dedicated goroutine,
// and hand the sub-radios to a Receiver (DataRadio) and a Sender
// (AckRadio).
type Mux struct {
	real    Radio
	dataCh  chan *protocolpb.Envelope
	ackCh   chan *protocolpb.Envelope
	stopMu  sync.Mutex
	stopped bool
	stop    chan struct{}
}

// NewMux wraps r with a demuxer. The data/ack channels are buffered
// (256) so a burst of fragments does not block the reader when one
// consumer is momentarily slow.
func NewMux(r Radio) *Mux {
	return &Mux{
		real:   r,
		dataCh: make(chan *protocolpb.Envelope, 256),
		ackCh:  make(chan *protocolpb.Envelope, 256),
		stop:   make(chan struct{}),
	}
}

// DataRadio returns a radio.Radio whose Receive yields DATA/START/END/
// TTS_* envelopes (everything except ACK). Send/Init/SetChannel/Close
// delegate to the underlying radio.
func (m *Mux) DataRadio() Radio { return &muxSide{mux: m, rx: m.dataCh} }

// AckRadio returns a radio.Radio whose Receive yields ACK envelopes.
// Send/Init/SetChannel/Close delegate to the underlying radio. A
// downlink Sender uses this so its Receive-based ACK loop does not
// race the uplink Receiver for the same RX path.
func (m *Mux) AckRadio() Radio { return &muxSide{mux: m, rx: m.ackCh} }

// Run is the single reader. It reads from the underlying radio and
// routes each envelope to the data or ack channel until ctx is
// canceled or the radio is closed. Must be called on a dedicated
// goroutine.
func (m *Mux) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Use a short timeout so we notice ctx cancellation promptly
		// even when the radio has no traffic.
		rctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		env, err := m.real.Receive(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Timeout or transient error: keep reading.
			continue
		}
		if env == nil {
			continue
		}
		if env.MsgType == protocolpb.MsgType_MSG_TYPE_ACK {
			select {
			case m.ackCh <- env:
			case <-ctx.Done():
				return ctx.Err()
			case <-m.stop:
				return io.EOF
			}
		} else {
			select {
			case m.dataCh <- env:
			case <-ctx.Done():
				return ctx.Err()
			case <-m.stop:
				return io.EOF
			}
		}
	}
}

// Close stops the demuxer and unblocks any pending channel sends. The
// underlying radio is NOT closed (the caller owns it). Idempotent.
func (m *Mux) Close() {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	if m.stopped {
		return
	}
	m.stopped = true
	close(m.stop)
}

// muxSide is a sub-radio backed by one of the Mux's channels.
type muxSide struct {
	mux *Mux
	rx  chan *protocolpb.Envelope
}

func (s *muxSide) Init(ctx context.Context, p Preset) error {
	return s.mux.real.Init(ctx, p)
}

func (s *muxSide) Send(ctx context.Context, env *protocolpb.Envelope) error {
	return s.mux.real.Send(ctx, env)
}

func (s *muxSide) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env := <-s.rx:
		return env, nil
	case <-ctx.Done():
		return nil, io.EOF
	case <-s.mux.stop:
		return nil, io.EOF
	}
}

func (s *muxSide) SetChannel(ctx context.Context, ch Channel) error {
	return s.mux.real.SetChannel(ctx, ch)
}

func (s *muxSide) Close() error { return s.mux.real.Close() }
