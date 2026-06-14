// Receiver state machine. See plan.md §2.5.
//
// The Receiver reads Envelopes from a radio, reassembles fragmented
// messages, and dispatches:
//
//   - complete messages to OnMessage
//   - partial-state acks to OnAck (the sender uses these to know
//     what to retransmit)
//
// The Receiver is a long-running goroutine; it terminates when ctx
// is canceled.
//
// Wire format reminder: a single message is fragmented into
// envelopes with the same (ConversationId, MessageId, MsgType). The
// first envelope has MsgType=START, intermediate envelopes have
// MsgType=DATA, the last has MsgType=END. (Plan §1.4: total_seqs
// is set on every envelope, so the receiver can use it to know the
// expected count; this implementation tracks the count from the
// first DATA envelope after a START, or from START.TotalSeqs.)
package radio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// IncomingMessage is a complete, reassembled message handed to
// OnMessage. ConversationID is 16 bytes (a UUID). Payload is the
// concatenation of all chunks' payloads in seq_num order.
type IncomingMessage struct {
	ConversationID []byte
	MessageID      uint32
	Payload        []byte
	AudioKind      protocolpb.AudioKind
	CompletedAt    time.Time
}

// OutgoingAck is a partial-state ack the Receiver hands to OnAck
// after each chunk. The Sender-side state machine uses these to
// drive retransmissions.
type OutgoingAck struct {
	ConversationID []byte
	MessageID      uint32
	NextExpected   uint32
	Bitmap         uint32
}

// Receiver reads envelopes from a Radio and dispatches reassembled
// messages.
type Receiver struct {
	radio          Radio
	onMessage      func(*IncomingMessage)
	onAck          func(*OutgoingAck)
	logger         *slog.Logger
	messageTimeout time.Duration
	convIDLen      int // expected conv_id length
}

// ReceiverOption configures a Receiver.
type ReceiverOption func(*Receiver)

// ReceiverOptionOnMessage sets the per-complete-message callback.
func ReceiverOptionOnMessage(fn func(*IncomingMessage)) ReceiverOption {
	return func(r *Receiver) { r.onMessage = fn }
}

// ReceiverOptionOnAck sets the per-chunk-ack callback.
func ReceiverOptionOnAck(fn func(*OutgoingAck)) ReceiverOption {
	return func(r *Receiver) { r.onAck = fn }
}

// ReceiverOptionLogger sets the structured logger.
func ReceiverOptionLogger(l *slog.Logger) ReceiverOption {
	return func(r *Receiver) { r.logger = l }
}

// ReceiverOptionMessageTimeout sets the duration after which a
// partial message (missing chunks) is abandoned.
func ReceiverOptionMessageTimeout(d time.Duration) ReceiverOption {
	return func(r *Receiver) { r.messageTimeout = d }
}

// NewReceiver builds a Receiver that reads from r.
func NewReceiver(r Radio, opts ...ReceiverOption) *Receiver {
	rec := &Receiver{
		radio:          r,
		logger:         slog.Default(),
		messageTimeout: 5 * time.Second,
		convIDLen:      16,
	}
	for _, o := range opts {
		o(rec)
	}
	return rec
}

// Run blocks until ctx is canceled, dispatching complete messages to
// onMessage and per-chunk acks to onAck.
//
// Per-message state machine:
//
//	IDLE → START seen → RECEIVING(chunks_so_far) →
//	  total_seqs reached → EMIT → IDLE
//	RECEIVING → timeout → ABANDON → IDLE
//
// reassemblyState is the per-(conv_id, message_id) reassembly
// buffer. Keyed externally by messageKey.
type reassemblyState struct {
	convID    []byte
	messageID uint32
	totalSeqs uint32
	chunks    []*protocolpb.Envelope
	bitmap    *protocol.AckBitmap
	firstSeen time.Time
	audioKind protocolpb.AudioKind
}

func messageKey(convID []byte, messageID uint32) string {
	return string(convID) + "|" + fmt.Sprintf("%d", messageID)
}

// Run drives the receiver state machine.
func (r *Receiver) Run(ctx context.Context) error {
	type recv struct {
		inner *Receiver
	}
	_ = recv{}

	states := make(map[string]*reassemblyState)
	var statesMu sync.Mutex
	_ = statesMu

	// Sweep loop: periodically check for abandoned messages.
	sweepTicker := time.NewTicker(r.messageTimeout / 4)
	defer sweepTicker.Stop()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-sweepTicker.C:
			r.sweep(ctx, states)
		default:
			rctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			env, err := r.radio.Receive(rctx)
			cancel()
			if err != nil {
				continue
			}
			// Filter: only handle DATA, START, END, ACK (ACK is
			// handled by the Sender side, but we silently accept
			// them).
			switch env.MsgType {
			case protocolpb.MsgType_MSG_TYPE_DATA,
				protocolpb.MsgType_MSG_TYPE_START,
				protocolpb.MsgType_MSG_TYPE_END:
				r.handleDataEnvelope(ctx, states, env)
			default:
				// Ignore other msg types (ACK, TTS_DATA, etc).
			}
		}
	}
}

// sweep removes abandoned messages from the states map.
func (r *Receiver) sweep(ctx context.Context, states map[string]*reassemblyState) {
	now := time.Now()
	for k, st := range states {
		if now.Sub(st.firstSeen) > r.messageTimeout {
			r.logger.Warn("receiver: abandoning stale message",
				"convID", st.convID, "messageID", st.messageID)
			delete(states, k)
		}
	}
	_ = ctx
}

// handleDataEnvelope processes a single DATA/START/END envelope.
func (r *Receiver) handleDataEnvelope(ctx context.Context, states map[string]*reassemblyState, env *protocolpb.Envelope) {
	if len(env.ConversationId) != r.convIDLen {
		r.logger.Warn("receiver: bad conv_id length",
			"got", len(env.ConversationId), "want", r.convIDLen)
		return
	}
	key := messageKey(env.ConversationId, env.MessageId)

	// START: just set up state, no chunk is added.
	if env.MsgType == protocolpb.MsgType_MSG_TYPE_START {
		st, ok := states[key]
		if !ok {
			st = &reassemblyState{
				convID:    bytes.Clone(env.ConversationId),
				messageID: env.MessageId,
				bitmap:    &protocol.AckBitmap{},
				firstSeen: time.Now(),
				audioKind: env.AudioKind,
			}
			states[key] = st
		}
		if env.TotalSeqs > 0 {
			st.totalSeqs = env.TotalSeqs
		}
		return
	}

	st, ok := states[key]
	if !ok {
		st = &reassemblyState{
			convID:    bytes.Clone(env.ConversationId),
			messageID: env.MessageId,
			bitmap:    &protocol.AckBitmap{},
			firstSeen: time.Now(),
			audioKind: env.AudioKind,
		}
		states[key] = st
	}

	// END: a marker. By the time we get here, the message has
	// either already been emitted (because all DATA was in) or is
	// still incomplete (the sweeper will abandon it on timeout).
	// We use END as a chance to learn the canonical TotalSeqs in
	// case it was missing on the START or DATA envelopes.
	if env.MsgType == protocolpb.MsgType_MSG_TYPE_END {
		if env.TotalSeqs > 0 {
			st.totalSeqs = env.TotalSeqs
		}
		return
	}

	// DATA: add to chunks, update bitmap, emit ACK.
	if env.TotalSeqs > 0 {
		st.totalSeqs = env.TotalSeqs
	}
	if len(st.chunks) <= int(env.SeqNum) {
		// Grow the slice to hold this seq.
		newChunks := make([]*protocolpb.Envelope, env.SeqNum+1)
		copy(newChunks, st.chunks)
		st.chunks = newChunks
	}
	// Ignore duplicate seq (already filled).
	if st.chunks[env.SeqNum] != nil {
		return
	}
	st.chunks[env.SeqNum] = env
	st.bitmap.Set(env.SeqNum)

	// Emit an ACK for this chunk.
	if r.onAck != nil {
		r.onAck(&OutgoingAck{
			ConversationID: st.convID,
			MessageID:      st.messageID,
			NextExpected:   st.bitmap.NextExpectedSeq,
			Bitmap:         st.bitmap.Bitmap,
		})
	}

	// Check if the message is complete. The check is "every chunk
	// in [0, total_seqs) is non-nil" — a sparse chunks slice (with
	// a nil in the middle) does not count.
	if st.totalSeqs > 0 && len(st.chunks) >= int(st.totalSeqs) {
		allFilled := true
		for i := uint32(0); i < st.totalSeqs; i++ {
			if st.chunks[i] == nil {
				allFilled = false
				break
			}
		}
		if allFilled {
			r.emitMessage(ctx, states, key, st)
		}
	}
}

// emitMessage concatenates the chunks and hands the result to
// onMessage.
func (r *Receiver) emitMessage(_ context.Context, states map[string]*reassemblyState, key string, st *reassemblyState) {
	if r.onMessage == nil {
		delete(states, key)
		return
	}
	// Concatenate the payloads in seq_num order. We assume all
	// chunks in [0, total_seqs) are non-nil because the caller
	// (handleDataEnvelope) verified this before calling emitMessage.
	var payload []byte
	for _, ch := range st.chunks {
		payload = append(payload, ch.Payload...)
	}
	r.onMessage(&IncomingMessage{
		ConversationID: st.convID,
		MessageID:      st.messageID,
		Payload:        payload,
		AudioKind:      st.audioKind,
		CompletedAt:    time.Now(),
	})
	delete(states, key)
}

// Sentinel errors. Receiver.Run never returns them; they are
// reserved for future use.
var (
	ErrBadConvID = errors.New("receiver: bad conversation id length")
)
