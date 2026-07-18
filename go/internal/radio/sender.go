// Sender state machine. See plan.md §2.4.
//
// The Sender transmits a pre-fragmented sequence of Envelopes over a
// radio, waits for ACKs, and retransmits envelopes whose ACKs do not
// arrive before a timeout. It stops when:
//
//   - all envelopes are acked (returns nil error)
//   - one envelope exceeds its retry budget (returns ErrMaxRetries)
//   - the context is canceled (returns ctx.Err())
//
// The Sender does *not* re-fragment. Fragmentation is the caller's
// responsibility. The Sender is a single-message state machine: one
// Sender instance per outgoing message.
//
// The Sender and Receiver use the same wire format: the Sender reads
// ACK envelopes from the radio (filtered by MsgType), updates an
// internal AckBitmap, and only transmits the next seq once the
// previous one is acked (or its retry budget is exhausted).
package radio

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// hasNonZero reports whether any byte in b is non-zero. Used to
// distinguish a legacy all-zero ACK conversation_id (accept) from a
// real foreign conversation_id (reject).
func hasNonZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return true
		}
	}
	return false
}

// Sentinel errors returned by Sender.Run.
var (
	// ErrNoEnvelopes: NewSender was called with an empty or nil slice.
	ErrNoEnvelopes = errors.New("radio: sender has no envelopes to send")
	// ErrMaxRetries: an envelope exceeded its retry budget.
	ErrMaxRetries = errors.New("radio: sender exhausted retry budget")
	// ErrSendFailed: Send returned a non-recoverable error (e.g. queue
	// full on a closed radio).
	ErrSendFailed = errors.New("radio: send failed")
)

// Sender transmits a pre-fragmented Envelope sequence.
type Sender struct {
	radio    Radio
	envs     []*protocolpb.Envelope
	timeout  time.Duration
	maxRetry int
	logger   *slog.Logger

	// onAcked fires once per acknowledged envelope (in seq order).
	onAcked func(seq uint32)
	// onFailed fires once per envelope that exceeded its retry budget.
	onFailed func(env *protocolpb.Envelope, retries int)
	// onSuccess fires once when every envelope has been acked.
	onSuccess func()
}

// SenderOption configures a Sender at construction time.
type SenderOption func(*Sender)

// SenderOptionTimeout sets the per-ACK timeout. After this duration
// without an ACK for the current envelope, the sender retransmits.
func SenderOptionTimeout(d time.Duration) SenderOption {
	return func(s *Sender) { s.timeout = d }
}

// SenderOptionMaxRetry sets the maximum number of retransmissions
// per envelope.
func SenderOptionMaxRetry(n int) SenderOption {
	return func(s *Sender) { s.maxRetry = n }
}

// SenderOptionLogger installs a structured logger. The default is
// slog.Default().
func SenderOptionLogger(l *slog.Logger) SenderOption {
	return func(s *Sender) { s.logger = l }
}

// SenderOptionOnAcked installs a callback fired once per acked
// envelope.
func SenderOptionOnAcked(fn func(seq uint32)) SenderOption {
	return func(s *Sender) { s.onAcked = fn }
}

// SenderOptionOnFailed installs a callback fired once per envelope
// that exhausts its retry budget.
func SenderOptionOnFailed(fn func(env *protocolpb.Envelope, retries int)) SenderOption {
	return func(s *Sender) { s.onFailed = fn }
}

// SenderOptionOnSuccess installs a callback fired once when every
// envelope has been acked.
func SenderOptionOnSuccess(fn func()) SenderOption {
	return func(s *Sender) { s.onSuccess = fn }
}

// NewSender builds a Sender for a pre-fragmented sequence.
func NewSender(r Radio, envs []*protocolpb.Envelope, opts ...SenderOption) *Sender {
	s := &Sender{
		radio: r,
		envs:  envs,
		// research.md §8.5: "ACK timer: 2 s." This is the
		// production default; tests and the loopback tool
		// override it with SenderOptionTimeout for speed.
		timeout:  2 * time.Second,
		maxRetry: 5,
		logger:   slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run blocks until all envelopes are acked, one exceeds maxRetry, or
// ctx is canceled. The return values are:
//
//   - acked: count of envelopes that were acked before the run ended
//   - failed: the first envelope that exhausted its retry budget
//     (nil on success / cancel)
//   - retries: total number of retransmissions across all envelopes
//   - err: nil on success, ErrMaxRetries on retry exhaustion,
//     ctx.Err() on cancel, or a wrapped Send error
func (s *Sender) Run(ctx context.Context) (int, *protocolpb.Envelope, int, error) {
	if len(s.envs) == 0 {
		return 0, nil, 0, ErrNoEnvelopes
	}

	type sendState struct {
		retries atomic.Int32
		acked   atomic.Bool
	}
	states := make([]*sendState, len(s.envs))
	for i := range states {
		states[i] = &sendState{}
	}

	var totalRetries atomic.Int32
	currentSeq := uint32(0)
	ackedCount := 0

	sendOnce := func(seq uint32) error {
		// Clone the envelope so the RETRANSMIT flag we set below
		// does not race with another Sender sharing the same
		// envs slice. (The race-detector test fails intermittently
		// without this.)
		env := proto.Clone(s.envs[seq]).(*protocolpb.Envelope)
		// Mark the retransmit flag if this is a retry. (Plan §6.x:
		// bit0 of Flags = RETRANSMIT.)
		if states[seq].retries.Load() > 0 {
			env.Flags |= 1
		}
		return s.radio.Send(ctx, env)
	}

	// Send seq 0 immediately.
	if err := sendOnce(currentSeq); err != nil {
		return 0, s.envs[currentSeq], 0, errors.Join(ErrSendFailed, err)
	}

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return ackedCount, nil, int(totalRetries.Load()), ctx.Err()
		}
		if ackedCount == len(s.envs) {
			if s.onSuccess != nil {
				s.onSuccess()
			}
			return ackedCount, nil, int(totalRetries.Load()), nil
		}

		select {
		case <-ctx.Done():
			return ackedCount, nil, int(totalRetries.Load()), ctx.Err()
		case <-timer.C:
			// Timeout: retransmit the current seq.
			states[currentSeq].retries.Add(1)
			totalRetries.Add(1)
			if int(states[currentSeq].retries.Load()) > s.maxRetry {
				if s.onFailed != nil {
					s.onFailed(s.envs[currentSeq], int(states[currentSeq].retries.Load()))
				}
				return ackedCount, s.envs[currentSeq], int(totalRetries.Load()), ErrMaxRetries
			}
			if err := sendOnce(currentSeq); err != nil {
				return ackedCount, s.envs[currentSeq], int(totalRetries.Load()), errors.Join(ErrSendFailed, err)
			}
			timer.Reset(s.timeout)
		default:
			// Non-blocking check for an incoming ACK.
			rctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
			env, err := s.radio.Receive(rctx)
			cancel()
			if err != nil {
				// Timeout or ctx cancel: continue.
				continue
			}
			if env.MsgType != protocolpb.MsgType_MSG_TYPE_ACK {
				// Not an ACK: ignore and continue.
				continue
			}
			// Decode the 28-byte ACK payload (research.md §8.6). The
			// payload is self-describing: it carries its own
			// conversation_id + message_id + CRC-16, so we validate
			// the ACK belongs to this message from the PAYLOAD, not
			// just the envelope header (REVIEW.md F3).
			bmp, ackConvID, ackMsgID, err := protocol.DecodeAckBitmap(env.Payload)
			if err != nil {
				s.logger.Warn("sender: failed to decode ack", "err", err)
				continue
			}
			// Reject ACKs that do not belong to this message. The
			// ACK payload carries conversation_id + message_id
			// (research.md §8.6); an ACK for conversation A must
			// never ack envelopes in conversation B. A zero
			// conversation_id in the payload is the legacy/test
			// shape (accepted for back-compat); production wiring
			// always populates both fields.
			if !bytes.Equal(ackConvID, s.envs[0].ConversationId) {
				// Only skip if the payload actually carried a conv id
				// (a non-zero one). An all-zero payload conv id means
				// a legacy ACK — fall back to the envelope header.
				if hasNonZero(ackConvID) || (len(env.ConversationId) > 0 && !bytes.Equal(env.ConversationId, s.envs[0].ConversationId)) {
					s.logger.Warn("sender: ack for different conversation; ignoring",
						"want", s.envs[0].ConversationId, "got", ackConvID)
					continue
				}
			}
			if ackMsgID != 0 && ackMsgID != s.envs[0].MessageId {
				s.logger.Warn("sender: ack for different message; ignoring",
					"want", s.envs[0].MessageId, "got", ackMsgID)
				continue
			}
			// Merge the incoming bitmap into ours. Each seq that
			// is "acked" (per the bitmap) is marked locally.
			for i, e := range s.envs {
				if states[i].acked.Load() {
					continue
				}
				if bmp.Has(e.SeqNum) {
					states[i].acked.Store(true)
					if s.onAcked != nil {
						s.onAcked(e.SeqNum)
					}
					ackedCount++
				}
			}
			// Advance the next-send cursor. The next unsent seq is
			// the lowest index whose state is not acked.
			for currentSeq < uint32(len(s.envs)) && states[currentSeq].acked.Load() {
				currentSeq++
			}
			// If the current seq is brand new (no retries yet), send
			// it immediately.
			if currentSeq < uint32(len(s.envs)) && states[currentSeq].retries.Load() == 0 {
				if err := sendOnce(currentSeq); err != nil {
					return ackedCount, s.envs[currentSeq], int(totalRetries.Load()), errors.Join(ErrSendFailed, err)
				}
			}
			timer.Reset(s.timeout)
		}
	}
}
