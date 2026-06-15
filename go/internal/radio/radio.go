// Package radio defines the LoRa radio interface and a mock implementation.
//
// The Radio interface is the seam between the bridge/M5 firmware (Phase 3+)
// and the higher-level Go data plane. The Mock is the in-process stand-in
// that unit tests and the Phase 1 loopback tool use. Plan §0.5 / §1.5.
package radio

import (
	"context"
	"errors"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// ErrQueueFull is returned by Mock.Send when the in-process queue is full
// and no consumer is draining it. The real radio (Phase 3+) maps this to
// backpressure on the air interface.
var ErrQueueFull = errors.New("radio: send queue full")

// Radio is the abstract LoRa radio. Implementations: SX1262 (real, Phase 3),
// Mock (test, Phase 0).
type Radio interface {
	// Init configures the radio for the given preset. Idempotent.
	Init(ctx context.Context, preset Preset) error

	// Send queues a packet for transmission. Returns when the packet has
	// been handed to the radio (not when the air-time is complete).
	Send(ctx context.Context, env *protocolpb.Envelope) error

	// Receive blocks until a packet is available or ctx is canceled.
	// Returns io.EOF on context cancel.
	Receive(ctx context.Context) (*protocolpb.Envelope, error)

	// SetChannel switches to a new US915 channel. Takes effect on next TX/RX.
	SetChannel(ctx context.Context, ch Channel) error

	// Close releases the radio. Idempotent.
	Close() error
}

// Preset describes the LoRa physical-layer configuration.
type Preset struct {
	SpreadingFactor uint8  // 7..12
	BandwidthHz     uint32 // 125000, 250000, 500000
	CodingRate      uint8  // 5..8 (meaning 4/5 .. 4/8)
	TxPowerDbm      int8   // -9..+22
	SyncWord        uint8  // 0xF3 for private
}

// Channel identifies a US915 sub-band. Index 0..63; Hz is the computed
// centre frequency.
type Channel struct {
	Index uint8  // 0..63
	Hz    uint64 // computed from index
}
