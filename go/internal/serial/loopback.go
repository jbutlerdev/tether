// In-process loopback transport. See plan.md §2.6.
//
// A loopback Pair is two radio.Radio endpoints connected by a
// simulated "air" link that supports configurable packet loss,
// reorder, base latency, and jitter. Both endpoints implement
// radio.Radio, so a Sender can use one and a Receiver the other
// (or a test can drive both directly).
//
// The wire model is intentionally simple:
//
//	pa.Send(env)  --(loss, reorder, latency)-->  pb.Receive
//	pb.Send(env)  --(loss, reorder, latency)-->  pa.Receive
//
// Each side has its own receive queue (a buffered channel). The
// "wire" is a goroutine that drains the send queue, applies the
// loss/reorder/latency transforms, and enqueues onto the
// destination's receive queue. For the Phase 1 implementation we
// implement loss only; reorder/latency are no-ops (the underlying
// channels are FIFO, so any latency is just the goroutine
// scheduling).
package serial

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// NewLoopbackPair returns a pair of connected Radios and a cleanup
// function. The Pair is symmetric: each end can send to the other.
//
// The Pair implements the PacketLosser interface (loss probability
// is shared across both directions).
func NewLoopbackPair() (radio.Radio, radio.Radio) {
	p := &loopbackPair{
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
		stop: make(chan struct{}),
	}
	a := &loopbackSide{name: "a", pair: p, rx: make(chan *protocolpb.Envelope, 1024), stop: make(chan struct{})}
	b := &loopbackSide{name: "b", pair: p, rx: make(chan *protocolpb.Envelope, 1024), stop: make(chan struct{})}
	p.a = a
	p.b = b
	return a, b
}

// PacketLosser is implemented by loopback endpoints; tests use it to
// inject controlled packet loss.
type PacketLosser interface {
	SetPacketLoss(pct float64)
}

// loopbackPair is the shared state of a loopback connection.
type loopbackPair struct {
	a, b *loopbackSide

	lossPct atomic.Int64 // 0..1000

	rngMu sync.Mutex
	rng   *rand.Rand

	stop chan struct{}
}

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

// loopbackSide implements radio.Radio for one end of the loopback.
type loopbackSide struct {
	name string
	pair *loopbackPair

	mu     sync.Mutex
	closed bool

	// rx is this side's receive queue. The OTHER side's Send enqueues
	// into here (after loss/reorder/latency). We never close rx; we
	// use `stop` to signal Receive to bail out.
	rx   chan *protocolpb.Envelope
	stop chan struct{}

	// curCh and lastSendC record channel state.
	curCh     atomic.Uint64
	lastSendC atomic.Uint64
}

func (s *loopbackSide) Init(context.Context, radio.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("loopback: init on closed")
	}
	return nil
}

// Send enqueues env onto the partner side's rx channel, after
// applying the loss filter. The send is non-blocking: if the
// partner's queue is full, the packet is dropped (returned as an
// error). This avoids the data race between close(rx) and Send.
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
	other := s.pair.a
	if s.name == "a" {
		other = s.pair.b
	}
	s.mu.Unlock()

	// Loss filter.
	if s.pair.shouldDrop() {
		return nil
	}

	// Non-blocking enqueue.
	select {
	case other.rx <- clone:
		return nil
	default:
		return errors.New("loopback: rx queue full")
	}
}

func (s *loopbackSide) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env := <-s.rx:
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
	close(s.stop)
	return nil
}

// SetPacketLoss installs a loss probability in [0.0, 1.0]. 0.0
// means no loss; 1.0 means drop everything.
func (s *loopbackSide) SetPacketLoss(pct float64) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	s.pair.lossPct.Store(int64(pct * 1000))
}

// Compile-time checks.
var (
	_ radio.Radio    = (*loopbackSide)(nil)
	_ PacketLosser   = (*loopbackSide)(nil)
)
