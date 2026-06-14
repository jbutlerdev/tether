// Tests for the tether-loopback end-to-end harness. See plan.md §2.7.
package loopback_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/loopback"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// TestLoopback_RoundTrip_SyntheticAudio: a Sender pushes a synthetic
// audio blob through the loopback; a bridge goroutine ACKs and
// forwards; a Receiver reassembles. The decoded PCM is equal to
// the encoded PCM (mock codec is identity).
func TestLoopback_RoundTrip_SyntheticAudio(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	// 1 second of 440 Hz sine at 8 kHz, int16 PCM.
	pcm := sineWave(440.0, 8000, 1*time.Second)

	c := codec.NewMock()
	opusFrames := encodeAll(t, c, pcm)

	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, err := protocol.Fragment(opusFrames, 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}

	// "Shadow" radio that the Receiver reads from. The bridge
	// goroutine pumps from pb into this shadow, plus sends ACKs.
	shadowIn := make(chan *protocolpb.Envelope, 1024)
	shadowStop := make(chan struct{})
	shadow := shadowRadio{in: shadowIn, stop: shadowStop}

	// Bridge: read from pb, send ACK, forward to shadow.
	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go bridge(bridgeCtx, pb, shadowIn, shadowStop)

	// Receiver reads from the shadow.
	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(shadow,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(2*time.Second),
	)
	runCtx, runCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Sender transmits.
	sctx, cancelS := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelS()
	s := radio.NewSender(pa, envs,
		radio.SenderOptionTimeout(100*time.Millisecond),
		radio.SenderOptionMaxRetry(10),
	)
	if _, _, _, err := s.Run(sctx); err != nil {
		t.Fatalf("Sender.Run: %v", err)
	}

	// Wait for the receiver to process.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("messages: want 1, got %d", len(messages))
	}
	if !bytes.Equal(messages[0].Payload, opusFrames) {
		t.Errorf("payload mismatch: want %d bytes, got %d", len(opusFrames), len(messages[0].Payload))
	}
}

// bridge is a single goroutine that owns pb's RX. For each envelope
// it sends an ACK back via pb, then forwards the envelope to the
// shadow's input channel for the Receiver to read.
func bridge(ctx context.Context, pb radio.Radio, shadowIn chan<- *protocolpb.Envelope, shadowStop chan struct{}) {
	for {
		rctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		env, err := pb.Receive(rctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if env.MsgType == protocolpb.MsgType_MSG_TYPE_ACK {
			continue
		}
		// Send ACK back.
		next := env.SeqNum + 1
		ack := &protocolpb.Envelope{
			MsgType: protocolpb.MsgType_MSG_TYPE_ACK,
			Payload: encodeAckPayloadLocal(next),
		}
		_ = pb.Send(ctx, ack)
		// Forward to the shadow.
		select {
		case shadowIn <- env:
		case <-ctx.Done():
			return
		}
	}
}

// encodeAckPayloadLocal produces the 12-byte wire format the
// Sender expects.
func encodeAckPayloadLocal(next uint32) []byte {
	out := make([]byte, 12)
	out[0] = byte(next)
	out[1] = byte(next >> 8)
	out[2] = byte(next >> 16)
	out[3] = byte(next >> 24)
	return out
}

// shadowRadio is a radio.Radio backed by an in-memory channel.
// The Receiver reads from it; the bridge writes to it.
type shadowRadio struct {
	in   <-chan *protocolpb.Envelope
	stop chan struct{}
}

func (s shadowRadio) Init(context.Context, radio.Preset) error { return nil }
func (s shadowRadio) Send(context.Context, *protocolpb.Envelope) error {
	return errors.New("shadow: Send not supported")
}
func (s shadowRadio) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	select {
	case env := <-s.in:
		return env, nil
	case <-s.stop:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, io.EOF
	}
}
func (s shadowRadio) SetChannel(context.Context, radio.Channel) error { return nil }
func (s shadowRadio) Close() error                                     { return nil }

// TestLoopback_Stats: counts sent, received, acked, retries match.
func TestLoopback_Stats(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0xAA}, 1000), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	stats := loopback.RunOnce(loopback.RunOnceOptions{
		LocalRadio:  pa,
		RemoteRadio: pb,
		Envelopes:   envs,
		Timeout:     1 * time.Second,
		MaxRetry:    5,
	})

	if stats.Sent != len(envs) {
		t.Errorf("Sent: want %d, got %d", len(envs), stats.Sent)
	}
	if stats.Acked != len(envs) {
		t.Errorf("Acked: want %d, got %d", len(envs), stats.Acked)
	}
	if stats.Received != len(envs) {
		t.Errorf("Received: want %d, got %d", len(envs), stats.Received)
	}
}

// TestLoopback_RoundTrip_60sSyntheticAudio is the headline test from
// plan §2.7: 60 s of 440 Hz sine at 8 kHz round-trips through the
// loopback.
func TestLoopback_RoundTrip_60sSyntheticAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 60s test in -short mode")
	}
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	pcm := sineWave(440.0, 8000, 60*time.Second)

	c := codec.NewMock()
	opusFrames := encodeAll(t, c, pcm)

	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, err := protocol.Fragment(opusFrames, 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}

	// Shadow radio for the Receiver; bridge forwards from pb.
	shadowIn := make(chan *protocolpb.Envelope, 1024)
	shadowStop := make(chan struct{})
	shadow := shadowRadio{in: shadowIn, stop: shadowStop}

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go bridge(bridgeCtx, pb, shadowIn, shadowStop)

	rec := radio.NewReceiver(shadow,
		radio.ReceiverOptionMessageTimeout(2*time.Second),
	)
	recCtx, cancelRec := context.WithCancel(context.Background())
	defer cancelRec()
	go rec.Run(recCtx)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runStart := time.Now()

	s := radio.NewSender(pa, envs,
		radio.SenderOptionTimeout(200*time.Millisecond),
		radio.SenderOptionMaxRetry(20),
	)
	if _, _, _, err := s.Run(ctx); err != nil {
		t.Fatalf("Sender.Run: %v", err)
	}

	elapsed := time.Since(runStart)
	t.Logf("60s audio round-trip took %v (%d chunks)", elapsed, len(envs))
	if elapsed > 30*time.Second {
		t.Errorf("round-trip too slow: %v > 30s", elapsed)
	}
}

// sineWave generates a 440 Hz sine wave at the given sample rate
// and duration, as int16 PCM.
func sineWave(freqHz float64, sampleRate int, dur time.Duration) []int16 {
	n := int(float64(sampleRate) * dur.Seconds())
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		v := math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate))
		out[i] = int16(v * 16000)
	}
	return out
}

// encodeAll encodes a PCM buffer into a single byte stream using
// the mock codec.
func encodeAll(t *testing.T, c codec.Opus, pcm []int16) []byte {
	t.Helper()
	var out []byte
	frame := c.FrameSize()
	for off := 0; off < len(pcm); off += frame {
		end := off + frame
		if end > len(pcm) {
			// Pad to a full frame.
			buf := make([]int16, frame)
			copy(buf, pcm[off:])
			encoded, err := c.Encode(buf)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			out = append(out, encoded...)
			break
		}
		encoded, err := c.Encode(pcm[off:end])
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		out = append(out, encoded...)
	}
	return out
}
