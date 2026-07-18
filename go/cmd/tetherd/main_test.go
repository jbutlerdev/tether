// main_test.go — end-to-end integration test for the tetherd daemon.
//
// The test wires the daemon against one end of a serial loopback pair
// and drives the other end as a "virtual M5": it fragments mic PCM,
// runs a Sender, reassembles the downlink TTS, and decodes it. This
// proves the daemon's full data plane — receiver → pipeline → forge →
// TTS → FragmentAndSend → M5 — works through the real Run loop (not a
// hand-wired simulator), and that conv.Sync pushes UI_UPDATEs.
package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

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

// virtualM5 models the M5 end of the bridge: it reassembles downlink
// TTS fragments (decoding them to PCM) and relays ACKs back to the
// daemon's downlink senders. It uses a radio.Mux so the uplink Sender
// (reading ACKs) and the downlink Receiver (reading DATA) do not race
// on the M5's single radio RX path — the same single-radio constraint
// the daemon faces on its bridge.
type virtualM5 struct {
	radio      radio.Radio
	mux        *radio.Mux
	codec      *codec.Mock
	recv       *radio.Receiver
	recvCancel context.CancelFunc
	mu         sync.Mutex
	pcm        []int16
	ttsEnd     bool
	uiUpdates  []*protocolpb.Envelope
}

func newVirtualM5(t *testing.T, r radio.Radio, c *codec.Mock) *virtualM5 {
	t.Helper()
	m := &virtualM5{radio: r, codec: c, mux: radio.NewMux(r)}
	m.recv = radio.NewReceiver(m.mux.DataRadio(),
		radio.ReceiverOptionOnMessage(m.handleDownlink),
		radio.ReceiverOptionOnAck(m.handleAck),
		radio.ReceiverOptionMessageTimeout(5*time.Second),
	)
	return m
}

// Run starts the M5's demuxer and downlink receiver. The demuxer runs
// for the lifetime of ctx; the downlink receiver runs on a child
// context so stopRecv() can halt it (freeing the DataRadio for
// UI_UPDATE capture) without stopping the demuxer.
func (m *virtualM5) Run(ctx context.Context) {
	go func() { _ = m.mux.Run(ctx) }()
	recvCtx, cancel := context.WithCancel(ctx)
	m.recvCancel = cancel
	go func() { _ = m.recv.Run(recvCtx) }()
}

// stopRecv halts the downlink receiver so captureUIUpdates becomes the
// sole reader of the Mux's DataRadio.
func (m *virtualM5) stopRecv() {
	if m.recvCancel != nil {
		m.recvCancel()
	}
}

// AckRadio returns the M5's ACK sub-radio — the uplink Sender transmits
// DATA on it and reads ACKs from it (via the demuxer).
func (m *virtualM5) AckRadio() radio.Radio { return m.mux.AckRadio() }

func (m *virtualM5) handleDownlink(msg *radio.IncomingMessage) {
	pcm, err := m.codec.Decode(msg.Payload)
	if err != nil || len(pcm) == 0 {
		return
	}
	m.mu.Lock()
	m.pcm = append(m.pcm, pcm...)
	m.mu.Unlock()
}

func (m *virtualM5) handleAck(ack *radio.OutgoingAck) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = radio.SendAck(ctx, m.radio, ack)
}

// captureUIUpdates drains the M5 radio for UI_UPDATE envelopes (the
// conv.Sync pushes them as single-packet messages the Receiver ignores,
// so we read them directly off the demuxer's data channel via a
// short-timeout Receive loop on the M5 radio).
func (m *virtualM5) captureUIUpdates(ctx context.Context, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		env, err := m.mux.DataRadio().Receive(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if env.MsgType == protocolpb.MsgType_MSG_TYPE_UI_UPDATE {
			m.mu.Lock()
			m.uiUpdates = append(m.uiUpdates, env)
			got := len(m.uiUpdates)
			m.mu.Unlock()
			if got >= want {
				return nil
			}
		}
	}
	return errTimeout
}

var errTimeout = errors.New("test: timed out")

// TestDaemon_FullRoundTrip drives a complete uplink + downlink cycle
// through the daemon's Run loop, plus a conv.Sync UI_UPDATE push.
func TestDaemon_FullRoundTrip(t *testing.T) {
	t.Parallel()
	bridge, m5Side := serial.NewLoopbackPair()
	store := conv.NewMemStore()
	fc := forge.NewMockClient()
	defer fc.Close()
	c := codec.NewMock()

	d, err := NewDaemon(DaemonConfig{
		Bridge:     bridge,
		Store:      store,
		Forge:      fc,
		STT:        stt.NewMock(),
		TTS:        tts.NewMock(),
		Codec:      c,
		AckTimeout: 200 * time.Millisecond, // fast for the test
		MaxRetry:   10,
		SenderID:   0x0002,
		TargetID:   0xFFFF,
	})
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()

	// Create a forge session + subscribe the pipeline so the uplink
	// can resolve conv→session and the downlink has a consumer.
	sessionID, err := fc.CreateSession(ctx, "coder")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := d.Pipeline().HandleSSESubscribe(ctx, sessionID); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	convID := forge.SessionToConvID16(sessionID)

	// Start the virtual M5 demuxer + downlink receiver.
	m5 := newVirtualM5(t, m5Side, c)
	m5.Run(ctx)

	// ── Uplink: M5 captures PCM, fragments, sends to the daemon. ──
	pcm := sine(1600)
	opus, err := encodePCM(c, pcm)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var msgID atomic.Uint32
	msgID.Store(1)
	envs, err := protocol.Fragment(opus, msgID.Add(1), convID[:],
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("fragment: %v", err)
	}
	// The uplink Sender uses the M5's ACK sub-radio so its ACK reads
	// come from the demuxer (not the downlink Receiver's RX path).
	sender := radio.NewSender(m5.AckRadio(), envs,
		radio.SenderOptionTimeout(200*time.Millisecond),
		radio.SenderOptionMaxRetry(10),
	)
	if acked, failed, _, err := sender.Run(ctx); err != nil || failed != nil {
		t.Fatalf("uplink send: err=%v failed=%v acked=%d/%d", err, failed, acked, len(envs))
	}

	// Wait for the daemon to dispatch the transcript to forge.
	if err := waitForForgeMessage(ctx, fc, store, convID, 3*time.Second); err != nil {
		t.Fatalf("forge message: %v", err)
	}

	// ── Downlink: forge agent replies → daemon TTS → M5 speaker. ──
	if ok := fc.InjectEvent(sessionID, forge.Event{
		Type: forge.EventTextDelta, Content: `{"delta":"hello world"}`, Seq: 1, At: time.Now(),
	}); !ok {
		t.Fatal("inject text_delta failed")
	}
	if ok := fc.InjectEvent(sessionID, forge.Event{
		Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now(),
	}); !ok {
		t.Fatal("inject agent_end failed")
	}

	// Wait for the M5 to decode the TTS PCM.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m5.mu.Lock()
		got := len(m5.pcm)
		m5.mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	m5.mu.Lock()
	downlinkPCM := m5.pcm
	m5.mu.Unlock()
	if len(downlinkPCM) == 0 {
		t.Fatal("downlink: M5 received no TTS PCM")
	}

	// ── Conv sync: push a UI_UPDATE for a new conversation. ───────
	// Stop the M5 downlink receiver so captureUIUpdates is the sole
	// reader of the Mux's DataRadio (otherwise the Receiver would
	// consume and drop the UI_UPDATE).
	m5.stopRecv()
	m5.mu.Lock()
	m5.uiUpdates = nil
	m5.mu.Unlock()
	otherConv := [16]byte{0x55, 0x55}
	if _, _, err := store.Upsert(ctx, otherConv, conv.ConvInfo{
		Name: "Matrix #test", Kind: conv.KindMatrix, Target: "!test:hs",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := m5.captureUIUpdates(ctx, 1, 2*time.Second); err != nil {
		t.Fatalf("ui_update: %v", err)
	}
	m5.mu.Lock()
	updates := m5.uiUpdates
	m5.mu.Unlock()
	if len(updates) == 0 {
		t.Fatal("no UI_UPDATE captured")
	}
	if updates[0].MsgType != protocolpb.MsgType_MSG_TYPE_UI_UPDATE {
		t.Errorf("UI_UPDATE msg_type: want %v, got %v",
			protocolpb.MsgType_MSG_TYPE_UI_UPDATE, updates[0].MsgType)
	}
	if err := protocol.ValidateConvInfo(decodeConvInfo(t, updates[0].Payload)); err != nil {
		t.Errorf("UI_UPDATE payload invalid: %v", err)
	}
}

// TestDaemon_NilConfig verifies the constructor guards.
func TestDaemon_NilConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  DaemonConfig
	}{
		{"nil bridge", DaemonConfig{Store: conv.NewMemStore(), Forge: forge.NewMockClient(),
			STT: stt.NewMock(), TTS: tts.NewMock(), Codec: codec.NewMock()}},
		{"nil store", DaemonConfig{Forge: forge.NewMockClient(),
			STT: stt.NewMock(), TTS: tts.NewMock(), Codec: codec.NewMock()}},
		{"nil stt", DaemonConfig{Bridge: mustLoopback(), Store: conv.NewMemStore(),
			Forge: forge.NewMockClient(), TTS: tts.NewMock(), Codec: codec.NewMock()}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewDaemon(c.cfg); err == nil {
				t.Fatal("NewDaemon: want error for nil field, got nil")
			}
		})
	}
}

func mustLoopback() radio.Radio {
	r, _ := serial.NewLoopbackPair()
	return r
}

// encodePCM frames pcm into codec.FrameSize chunks, encodes each, and
// concatenates (matching the e2e simulator's encodePCM).
func encodePCM(c *codec.Mock, pcm []int16) ([]byte, error) {
	frame := c.FrameSize()
	if frame <= 0 {
		return nil, errors.New("frame size <= 0")
	}
	var out []byte
	for off := 0; off < len(pcm); off += frame {
		end := off + frame
		if end > len(pcm) {
			pad := make([]int16, end-len(pcm))
			f := append(append([]int16(nil), pcm[off:]...), pad...)
			b, err := c.Encode(f)
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
			break
		}
		b, err := c.Encode(pcm[off:end])
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// sine produces n samples of a 440 Hz sine wave at 8 kHz.
func sine(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(math.Sin(2*math.Pi*440*float64(i)/8000) * 8000)
	}
	return out
}

// waitForForgeMessage polls the forge mock until it has a SendMessage
// call whose session maps to convID.
func waitForForgeMessage(ctx context.Context, fc *forge.MockClient, store conv.Store, convID [16]byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		list, err := store.List(ctx)
		if err == nil {
			for _, c := range list {
				if c.Info.Kind != conv.KindForge || !bytes.Equal(c.ID[:], convID[:]) {
					continue
				}
				for _, call := range fc.SendMessageCalls() {
					if call.SessionID == c.Info.Target && call.Text != "" {
						return nil
					}
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errTimeout
}

// decodeConvInfo parses a UI_UPDATE payload into a ConvInfo for
// validation.
func decodeConvInfo(t *testing.T, payload []byte) *protocolpb.ConvInfo {
	t.Helper()
	// ConvInfo is the protobuf body; use proto.Unmarshal via the
	// protocol package's ValidateConvInfo which re-parses. We import
	// the generated type and unmarshal directly here.
	ci := &protocolpb.ConvInfo{}
	if err := proto.Unmarshal(payload, ci); err != nil {
		t.Fatalf("unmarshal convinfo: %v", err)
	}
	return ci
}
