// Test helper: a pair of in-process Radios connected by a wire.
//
// This is test infrastructure for the Sender/Receiver state machines.
// The production-grade in-process loopback (with packet loss, reorder,
// latency) lives in go/internal/serial/loopback.go (plan §2.6). This
// helper is intentionally minimal: a goroutine pumps envelopes from
// one side's Send to the other side's Receive, and back, with no
// reordering. Packet loss is supported via a per-call probabilistic
// drop controlled by SetPacketLoss.
package radio_test

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// loopbackPair is a pair of Radios connected by a wire. aSide.Send
// arrives on bSide.Receive, and vice versa.
type loopbackPair struct {
	aSide *loopbackSide
	bSide *loopbackSide

	// lossPct is the percentage of packets dropped on transmit
	// (0..1000, where 1000 = 100%). Atomic so the test can change it
	// dynamically.
	lossPct atomic.Int64

	// rng is a per-pair random source for loss decisions.
	rngMu sync.Mutex
	rng   *rand.Rand

	// done is closed by Close to signal in-flight Send goroutines
	// to bail out (so they don't race with close(rxQueue)).
	done chan struct{}
}

// newLoopback constructs a connected pair of Radios for use in
// tests. The pair is auto-closed at test cleanup.
func newLoopback(t *testing.T) *loopbackPair {
	t.Helper()
	p := &loopbackPair{
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
		done: make(chan struct{}),
	}
	p.aSide = newLoopbackSide("a", p)
	p.bSide = newLoopbackSide("b", p)
	t.Cleanup(func() { p.Close() })
	return p
}

// SetPacketLoss sets the loss probability. pct in [0.0, 1.0].
func (p *loopbackPair) SetPacketLoss(pct float64) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	p.lossPct.Store(int64(pct * 1000))
}

// shouldDrop returns true if this envelope should be dropped.
func (p *loopbackPair) shouldDrop() bool {
	threshold := p.lossPct.Load()
	if threshold == 0 {
		return false
	}
	p.rngMu.Lock()
	n := p.rng.Int63n(1000)
	p.rngMu.Unlock()
	return n < threshold
}

// Close shuts down both sides. It first signals all in-flight Send
// goroutines via the done channel, then closes both sides' stop
// signals (not the rxQueue itself, to avoid racing with Send).
func (p *loopbackPair) Close() {
	select {
	case <-p.done:
		// Already closed.
		return
	default:
		close(p.done)
	}
	_ = p.aSide.Close()
	_ = p.bSide.Close()
}

// loopbackSide implements radio.Radio for one end of the loopback
// pair. The Send/Receive pair connects to the partner side via the
// parent pair.
type loopbackSide struct {
	name   string
	parent *loopbackPair

	mu     sync.Mutex
	closed bool

	// rxQueue stores envelopes that the OTHER side has sent and we
	// need to deliver. We never close rxQueue directly; instead we
	// close `stop` so Receive can bail out. This avoids the data
	// race between close(rxQueue) and concurrent Send.
	rxQueue chan *protocolpb.Envelope
	stop    chan struct{}

	// curCh / lastSendCh record channel state for tests.
	curCh     atomic.Uint64
	lastSendC atomic.Uint64
}

func newLoopbackSide(name string, parent *loopbackPair) *loopbackSide {
	return &loopbackSide{
		name:    name,
		parent:  parent,
		rxQueue: make(chan *protocolpb.Envelope, 64),
		stop:    make(chan struct{}),
	}
}

func (s *loopbackSide) Init(_ context.Context, _ radio.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("loopback: init on closed")
	}
	return nil
}

func (s *loopbackSide) Send(_ context.Context, env *protocolpb.Envelope) error {
	if env == nil {
		return errors.New("loopback: nil envelope")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("loopback: send on closed")
	}
	clone := proto.Clone(env).(*protocolpb.Envelope)
	s.lastSendC.Store(s.curCh.Load())
	other := s.parent.bSide
	if s.name == "b" {
		other = s.parent.aSide
	}
	s.mu.Unlock()

	// If the parent pair is already closed, bail out.
	select {
	case <-s.parent.done:
		return errors.New("loopback: send on closed pair")
	default:
	}

	if s.parent.shouldDrop() {
		return nil
	}

	// Non-blocking send. The non-blocking send is critical: it
	// prevents the channel send from racing with a concurrent
	// close(rxQueue) on the receiving side.
	select {
	case other.rxQueue <- clone:
		return nil
	default:
		return errors.New("loopback: rx queue full")
	}
}

func (s *loopbackSide) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env, ok := <-s.rxQueue:
		if !ok {
			return nil, io.EOF
		}
		return env, nil
	case <-s.stop:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, io.EOF
	}
}

func (s *loopbackSide) SetChannel(_ context.Context, ch radio.Channel) error {
	s.curCh.Store(uint64(ch.Index))
	return nil
}

func (s *loopbackSide) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Close `stop` (not `rxQueue`) so concurrent Send calls do not
	// race with a channel close.
	close(s.stop)
	return nil
}

// Compile-time check.
var _ radio.Radio = (*loopbackSide)(nil)
