// End-to-end loopback harness. See plan.md §2.7.
//
// The loopback tool runs a Sender and an auto-ACK helper over a
// pair of in-process Radios. In Phase 1, the in-process radios are
// the loopback Pair from go/internal/serial. In Phase 7+ they will
// be a real SX1262 driver talking to a real M5 over LoRa.
//
// Two entry points:
//
//   - RunOnce(opts): synchronous, returns Stats. Used by tests and
//     the `tether-loopback send` CLI.
//
// The CLI lives in go/tools/tether-loopback/main.go.
package loopback

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Stats is the result of a single RunOnce invocation.
type Stats struct {
	Sent     int // envelopes handed to the Sender
	Acked    int // envelopes acked by the remote
	Received int // envelopes delivered to the remote
	Retries  int // retransmissions performed
	Failed   *protocolpb.Envelope
	Duration time.Duration
}

// RunOnceOptions configures RunOnce.
type RunOnceOptions struct {
	// LocalRadio is the local side of the loopback (where the
	// Sender transmits from).
	LocalRadio radio.Radio
	// RemoteRadio is the remote side of the loopback (where the
	// remote station listens).
	RemoteRadio radio.Radio
	// Envelopes is the pre-fragmented sequence to send.
	Envelopes []*protocolpb.Envelope
	// Timeout is the per-ACK timeout.
	Timeout time.Duration
	// MaxRetry is the per-envelope retry budget.
	MaxRetry int
	// MessageTimeout is reserved for future use; RunOnce does not
	// run the Receiver, so this has no effect today.
	MessageTimeout time.Duration
}

// RunOnce runs a single Sender round-trip and returns the resulting
// Stats. The Sender transmits opts.Envelopes; a helper goroutine
// reads each envelope from opts.RemoteRadio and sends an ACK back
// (simulating a remote station).
//
// RunOnce is intended for tests and the `tether-loopback` CLI; it
// is not safe to call RunOnce multiple times concurrently on the
// same opts.
//
// Note: RunOnce does NOT run the Receiver. The Received count is
// the number of envelopes the auto-ack goroutine observed. For an
// end-to-end test that exercises both Sender and Receiver, use the
// components directly (see TestLoopback_RoundTrip_SyntheticAudio).
func RunOnce(opts RunOnceOptions) Stats {
	if opts.Timeout == 0 {
		opts.Timeout = 200 * time.Millisecond
	}
	if opts.MaxRetry == 0 {
		opts.MaxRetry = 5
	}
	if opts.MessageTimeout == 0 {
		opts.MessageTimeout = 2 * time.Second
	}

	start := time.Now()

	var receivedCount int64
	var wg sync.WaitGroup

	// Auto-ack goroutine: drain RemoteRadio, send an ACK for each
	// non-ACK envelope. This stands in for a real remote station.
	ackCtx, cancelAck := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			ctx, cancel := context.WithTimeout(ackCtx, 50*time.Millisecond)
			env, err := opts.RemoteRadio.Receive(ctx)
			cancel()
			if err != nil {
				if ackCtx.Err() != nil {
					return
				}
				continue
			}
			if env.MsgType == protocolpb.MsgType_MSG_TYPE_ACK {
				continue
			}
			atomic.AddInt64(&receivedCount, 1)
			// Build the 28-byte ACK payload (research.md §8.6):
			// conv_id + msg_id + next_expected_seq + bitmap + crc16.
			// The payload is self-describing so the Sender can
			// validate the ACK belongs to this message even without
			// the envelope header.
			next := env.SeqNum + 1
			ack := &protocolpb.Envelope{
				MsgType:        protocolpb.MsgType_MSG_TYPE_ACK,
				ConversationId: append([]byte(nil), env.ConversationId...),
				MessageId:      env.MessageId,
				Payload:        protocol.EncodeAckPayload(env.ConversationId, env.MessageId, next, 0, 0),
			}
			_ = opts.RemoteRadio.Send(ackCtx, ack)
		}
	}()

	sender := radio.NewSender(opts.LocalRadio, opts.Envelopes,
		radio.SenderOptionTimeout(opts.Timeout),
		radio.SenderOptionMaxRetry(opts.MaxRetry),
	)
	sendCtx, cancelSend := context.WithTimeout(context.Background(), 30*time.Second)
	acked, failed, retries, _ := sender.Run(sendCtx)
	cancelSend()

	cancelAck()
	wg.Wait()

	return Stats{
		Sent:     len(opts.Envelopes),
		Acked:    acked,
		Received: int(atomic.LoadInt64(&receivedCount)),
		Retries:  retries,
		Failed:   failed,
		Duration: time.Since(start),
	}
}
