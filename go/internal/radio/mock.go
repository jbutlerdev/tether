// Mock implementation of the Radio interface. See plan §1.5.
//
// The Mock is an in-process stand-in: a buffered Go channel stores
// envelopes, with an optional artificial delay to simulate air-time.
// It is fully thread-safe and intended to be exercised under
// `go test -race`.
package radio

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Mock is a test double for Radio.
type Mock struct {
	queue chan *protocolpb.Envelope

	// mu protects closed, lastSendCh, and the lazy init of `queue`
	// (so that NewMock can be called from a test setup without
	// a real radio).
	mu        sync.Mutex
	closed    bool
	lastSendC atomic.Uint64 // Channel.Index of the most recent Send
	curCh     atomic.Uint64 // Channel.Index of the most recent SetChannel

	// txAirtimeMs is an optional artificial delay applied to Send,
	// to simulate the time-on-air of a real LoRa transmission.
	txAirtime time.Duration
}

// MockOption configures a Mock at construction time.
type MockOption func(*Mock)

// MockOptionMaxQueueSize sets the Send-side buffer length.
func MockOptionMaxQueueSize(n int) MockOption {
	return func(m *Mock) { m.queue = make(chan *protocolpb.Envelope, n) }
}

// MockOptionTxAirtime sets a per-Send artificial delay.
func MockOptionTxAirtime(d time.Duration) MockOption {
	return func(m *Mock) { m.txAirtime = d }
}

// NewMock returns a Mock with default settings. The default queue size
// is 16; override with MockOptionMaxQueueSize.
func NewMock(opts ...MockOption) *Mock {
	m := &Mock{
		queue: make(chan *protocolpb.Envelope, 16),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Init is a no-op for the mock; the preset is recorded for inspection
// in future phases but not validated here.
func (m *Mock) Init(_ context.Context, _ Preset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("radio: init on closed mock")
	}
	return nil
}

// Send enqueues env for later Receive. Returns ErrQueueFull immediately
// if the queue is full (non-blocking).
func (m *Mock) Send(ctx context.Context, env *protocolpb.Envelope) error {
	if env == nil {
		return errors.New("radio: nil envelope")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("radio: send on closed mock")
	}
	if m.txAirtime > 0 {
		d := m.txAirtime
		m.mu.Unlock()
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return errors.New("radio: send on closed mock")
		}
	}
	// Record the channel at send time. Use a clone so a later mutation
	// of the caller's env does not retroactively change history.
	clone := proto.Clone(env).(*protocolpb.Envelope)
	m.lastSendC.Store(uint64(m.curCh.Load()))
	// Non-blocking send. The select-default is safe even after Close
	// because the channel is not actually closed — instead, Close
	// sets `closed=true` and receivers fall through to the `done`
	// channel. See Close.
	select {
	case m.queue <- clone:
		m.mu.Unlock()
		return nil
	default:
		m.mu.Unlock()
		return ErrQueueFull
	}
}

// Receive blocks until an envelope is available, ctx is canceled, or
// the mock is closed (whichever comes first). Returns io.EOF on
// cancel/close. The queue is closed by Close, so a Receive on a
// closed-and-drained mock returns io.EOF via the `if !ok` branch.
func (m *Mock) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env, ok := <-m.queue:
		if !ok {
			return nil, io.EOF
		}
		return env, nil
	case <-ctx.Done():
		return nil, io.EOF
	}
}

// SetChannel records the channel for the next Send. The index is also
// stored in an atomic so LastSendChannel can read it without holding
// the mutex.
func (m *Mock) SetChannel(_ context.Context, ch Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("radio: setchannel on closed mock")
	}
	m.curCh.Store(uint64(ch.Index))
	return nil
}

// Close releases resources. Subsequent Send/Receive calls return
// errors / io.EOF. Calling Close twice is a no-op. We hold the mutex
// while closing the queue so concurrent Send calls (which also hold
// the mutex) cannot race with the close.
func (m *Mock) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	// Close the queue. This is safe because Send holds m.mu around
	// its non-blocking send, so no goroutine is sending to a closed
	// channel. Receivers see the closed channel and fall into the
	// `if !ok` branch, returning io.EOF.
	close(m.queue)
	return nil
}

// CurrentChannel returns the most recently SetChannel index, as a
// Channel value (Hz left zero). Useful in tests.
func (m *Mock) CurrentChannel() Channel {
	return Channel{Index: uint8(m.curCh.Load())}
}

// LastSendChannel returns the channel in effect at the time of the most
// recent successful Send. Useful in tests for verifying SetChannel
// takes effect on the next TX.
func (m *Mock) LastSendChannel() Channel {
	return Channel{Index: uint8(m.lastSendC.Load())}
}
