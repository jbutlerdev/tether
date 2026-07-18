// Tests for the e2e simulator. These are the only tests in the repo
// that chain every data-plane component (codec → fragmentation →
// radio Sender/Receiver with ACK+retransmit → forge pipeline → conv
// store) into one round trip. See REVIEW.md F17.
package e2e_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/e2e"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

var errTimeout = errors.New("e2e_test: timed out")

// decodeConvInfo parses a UI_UPDATE payload into a ConvInfo for
// validation.
func decodeConvInfo(t *testing.T, payload []byte) *protocolpb.ConvInfo {
	t.Helper()
	ci := &protocolpb.ConvInfo{}
	if err := proto.Unmarshal(payload, ci); err != nil {
		t.Fatalf("unmarshal ConvInfo: %v", err)
	}
	return ci
}

// sine produces n samples of a mid-band sine wave — non-silent so
// the STT mock's digest is non-empty and the TTS mock has input.
func sine(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(math.Sin(2*math.Pi*440*float64(i)/8000) * 8000)
	}
	return out
}

// TestSimulator_FullRoundTrip is the headline e2e check: the M5
// captures audio → Opus → fragment → LoRa (ACK+retransmit) → PC
// reassembles → STT → forge POST, then the forge agent replies over
// SSE → TTS → Opus → fragment → LoRa → M5 reassembles → decodes to
// speaker PCM. Both directions must succeed.
func TestSimulator_FullRoundTrip(t *testing.T) {
	t.Parallel()
	sim, err := e2e.NewSimulator()
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, convID, err := sim.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}

	// Uplink: 0.2 s of mic audio.
	if err := sim.RunUplink(ctx, convID, sine(1600)); err != nil {
		t.Fatalf("RunUplink: %v", err)
	}
	// The forge agent must have received a non-empty transcript.
	gotText := false
	for _, c := range sim.ForgeSendMessageCalls() {
		if c.SessionID == sessionID && c.Text != "" {
			gotText = true
			break
		}
	}
	if !gotText {
		t.Fatal("forge received no transcript for the uplink")
	}

	// Downlink: a short agent reply.
	pcm, err := sim.RunDownlink(ctx, sessionID, "Hello world.")
	if err != nil {
		t.Fatalf("RunDownlink: %v", err)
	}
	if len(pcm) == 0 {
		t.Fatal("M5 received no TTS PCM")
	}
}

// TestSimulator_UplinkMultipleConversations: two conversations route
// to the right forge session. ACKs are scoped per (conv_id, msg_id)
// so neither conversation's ACK can ack the other's packets.
func TestSimulator_UplinkMultipleConversations(t *testing.T) {
	t.Parallel()
	sim, err := e2e.NewSimulator()
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s1, c1, err := sim.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation 1: %v", err)
	}
	s2, c2, err := sim.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation 2: %v", err)
	}

	if err := sim.RunUplink(ctx, c1, sine(1600)); err != nil {
		t.Fatalf("RunUplink 1: %v", err)
	}
	if err := sim.RunUplink(ctx, c2, sine(2000)); err != nil {
		t.Fatalf("RunUplink 2: %v", err)
	}

	calls := sim.ForgeSendMessageCalls()
	saw1, saw2 := false, false
	for _, c := range calls {
		if c.SessionID == s1 && c.Text != "" {
			saw1 = true
		}
		if c.SessionID == s2 && c.Text != "" {
			saw2 = true
		}
	}
	if !saw1 {
		t.Error("conversation 1 received no transcript")
	}
	if !saw2 {
		t.Error("conversation 2 received no transcript")
	}
}

// TestSimulator_UplinkWithPacketLoss: 30% LoRa loss on the uplink
// must still deliver the message via retransmits.
func TestSimulator_UplinkWithPacketLoss(t *testing.T) {
	t.Parallel()
	sim, err := e2e.NewSimulator()
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	defer sim.Close()
	sim.SetUplinkLoss(0.30)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, convID, err := sim.NewConversation(ctx)
	if err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	if err := sim.RunUplink(ctx, convID, sine(1600)); err != nil {
		t.Fatalf("RunUplink with 30%% loss: %v", err)
	}
}

// TestConvSync_PushesUIUpdate: a conv.Store mutation flows through
// conv.Sync as a UI_UPDATE envelope on the radio. This validates the
// PC→M5 conversation-sync path that the daemon will use to push new
// Matrix rooms / forge sessions to the device.
func TestConvSync_PushesUIUpdate(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	store := conv.NewMemStore()
	sink := newCaptureSender()
	sync := conv.NewSync(conv.SyncConfig{
		Store:       store,
		Radio:       sink,
		SenderID:    0x0001,
		TargetID:    0xFFFF,
		MinInterval: -1, // no coalescing: one packet per event
	})
	go func() { _ = sync.Run(ctx) }()
	// Give Run a moment to register its store.Changes subscription
	// before the Upsert — the MemStore drops events for subscribers
	// that have not yet subscribed (same convention as conv/sync_test).
	time.Sleep(50 * time.Millisecond)

	convID := [16]byte{0xAA, 0xBB, 0xCC, 0xDD}
	if _, _, err := store.Upsert(ctx, convID, conv.ConvInfo{
		Name:   "test-room",
		Kind:   conv.KindMatrix,
		Target: "!room:example.org",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := sink.waitFor(1, 2*time.Second); err != nil {
		t.Fatalf("waitFor: %v", err)
	}
	envs := sink.envelopes()
	if len(envs) < 1 {
		t.Fatalf("expected ≥1 UI_UPDATE envelope, got %d", len(envs))
	}
	env := envs[0]
	if env.MsgType != protocolpb.MsgType_MSG_TYPE_UI_UPDATE {
		t.Errorf("MsgType: want UI_UPDATE, got %v", env.MsgType)
	}
	if err := protocol.ValidateConvInfo(decodeConvInfo(t, env.Payload)); err != nil {
		t.Errorf("ValidateConvInfo: %v", err)
	}
}

// captureSender is a radio.PacketSender that records every envelope.
type captureSender struct {
	mu   sync.Mutex
	envs []*protocolpb.Envelope
}

func newCaptureSender() *captureSender {
	return &captureSender{}
}

func (c *captureSender) Send(_ context.Context, env *protocolpb.Envelope) error {
	c.mu.Lock()
	c.envs = append(c.envs, env)
	c.mu.Unlock()
	return nil
}
func (c *captureSender) waitFor(n int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := len(c.envs)
		c.mu.Unlock()
		if got >= n {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errTimeout
}
func (c *captureSender) envelopes() []*protocolpb.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*protocolpb.Envelope, len(c.envs))
	copy(out, c.envs)
	return out
}
