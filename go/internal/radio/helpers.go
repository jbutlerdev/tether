// helpers.go — shared data-plane glue used by the daemon (cmd/tetherd)
// and the e2e simulator (internal/e2e).
//
// Two helpers live here because they are generic transport operations
// that were previously duplicated:
//
//   - FragmentAndSend: split a payload into chunks and run a Sender
//     over a Radio (with per-chunk ACK + retransmit). This is the
//     downlink emit path for TTS audio.
//   - SendAck: turn a Receiver OutgoingAck (cumulative bitmap) into a
//     wire ACK envelope and transmit it, so the peer Sender can
//     validate conv_id + msg_id (research.md §8.6) and advance.
//
// Keeping them in the radio package means the daemon and the simulator
// share one implementation and one set of tests, rather than each
// reinventing the fragment→Sender→ACK relay.

package radio

import (
	"context"
	"fmt"
	"time"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// FragmentAndSend splits payload into ≤ MaxPayloadSize chunks (all
// sharing msgID + convID + msgType + audioKind), runs a Sender over r
// to transmit them with per-chunk ACK and retransmit, and returns when
// every chunk is acked or one exhausts its retry budget. An empty
// payload is a no-op (returns nil) — callers use it for marker
// envelopes that carry no data.
//
// ackTimeout and maxRetry configure the Sender; pass the production
// 2 s / 5 (research.md §8.5) or a shorter pair for tests. A zero
// ackTimeout or maxRetry falls back to the Sender defaults.
func FragmentAndSend(ctx context.Context, r Radio, payload []byte,
	msgID uint32, convID []byte,
	msgType protocolpb.MsgType, audioKind protocolpb.AudioKind,
	ackTimeout time.Duration, maxRetry int) error {
	if len(payload) == 0 {
		return nil
	}
	envs, err := protocol.Fragment(payload, msgID, convID, msgType, audioKind)
	if err != nil {
		return fmt.Errorf("radio: fragment: %w", err)
	}
	if len(envs) == 0 {
		return nil
	}
	var opts []SenderOption
	if ackTimeout > 0 {
		opts = append(opts, SenderOptionTimeout(ackTimeout))
	}
	if maxRetry > 0 {
		opts = append(opts, SenderOptionMaxRetry(maxRetry))
	}
	sender := NewSender(r, envs, opts...)
	acked, failed, _, err := sender.Run(ctx)
	if err != nil {
		return err
	}
	if failed != nil {
		return fmt.Errorf("radio: fragment exhausted retries: acked %d/%d", acked, len(envs))
	}
	return nil
}

// SendAck turns a Receiver OutgoingAck (cumulative bitmap) into a wire
// ACK envelope and transmits it over r. The ACK payload is the
// self-describing 28-byte format (research.md §8.6) carrying the
// conversation_id + message_id, so the peer Sender can validate the
// ACK belongs to its message (REVIEW.md F3).
func SendAck(ctx context.Context, r Radio, ack *OutgoingAck) error {
	if ack == nil {
		return nil
	}
	bmp := protocol.AckBitmap{
		NextExpectedSeq: ack.NextExpected,
		Bitmap:          ack.Bitmap,
	}
	next, lo, hi := bmp.Encode()
	env := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_ACK,
		ConversationId:  append([]byte(nil), ack.ConversationID...),
		MessageId:       ack.MessageID,
		Payload:         protocol.EncodeAckPayload(ack.ConversationID, ack.MessageID, next, lo, hi),
	}
	return r.Send(ctx, env)
}
