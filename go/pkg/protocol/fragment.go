// Fragmentation and reassembly for the Tether wire format.
//
// Wire format: see proto/tether.proto. A payload that does not fit in a
// single Envelope (max 227 bytes per chunk) is split into multiple
// Envelopes with the same (message_id, conversation_id) and seq_num
// 0..total_seqs-1. The Sender appends START/END control messages
// around the data sequence; this file is concerned only with the
// splitting and joining, not with the control messages. See plan §2.2.
package protocol

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Sentinel errors for fragmentation failures. Tests assert on these
// with errors.Is.
var (
	// ErrOutOfOrder: envelopes not sorted by seq_num.
	ErrOutOfOrder = errors.New("protocol: fragment out of order")
	// ErrMissingChunk: a seq in [0, total_seqs) was not present.
	ErrMissingChunk = errors.New("protocol: missing chunk in sequence")
	// ErrDuplicateSeq: the same seq appeared more than once.
	ErrDuplicateSeq = errors.New("protocol: duplicate seq_num")
	// ErrSeqMismatch: an envelope's TotalSeqs field disagrees with
	// the count of envelopes actually provided.
	ErrSeqMismatch = errors.New("protocol: seq_num does not match expected")
)

// Fragment splits payload into a slice of Envelopes ready for
// transmission. The envelopes are returned in order (seq_num 0, 1,
// …, n-1) with the same message_id, conversation_id, msg_type, and
// audio_kind. Each envelope's TotalSeqs is set to len(envs).
//
// An empty payload returns a nil slice and no error. Payloads larger
// than the wire's per-chunk limit (MaxPayloadSize) are split across
// as many envelopes as needed; the returned chunks are always ≤
// MaxPayloadSize. The Envelope.Payload field is what gets serialized
// to the wire, so each chunk is guaranteed to fit a single
// transmission.
func Fragment(
	payload []byte,
	msgID uint32,
	convID []byte,
	msgType protocolpb.MsgType,
	audioKind protocolpb.AudioKind,
) ([]*protocolpb.Envelope, error) {
	if len(payload) == 0 {
		return nil, nil
	}

	var envs []*protocolpb.Envelope
	for off := 0; off < len(payload); off += MaxPayloadSize {
		end := off + MaxPayloadSize
		if end > len(payload) {
			end = len(payload)
		}
		envs = append(envs, &protocolpb.Envelope{
			ProtocolVersion: 1,
			ConversationId:  bytes.Clone(convID),
			MessageId:       msgID,
			SeqNum:          uint32(off / MaxPayloadSize),
			TotalSeqs:       0, // filled in below
			MsgType:         msgType,
			AudioKind:       audioKind,
			Payload:         bytes.Clone(payload[off:end]),
		})
	}
	for _, env := range envs {
		env.TotalSeqs = uint32(len(envs))
	}
	return envs, nil
}

// Reassemble is the inverse of Fragment: given a slice of envelopes
// that all share a (conversation_id, message_id), validate the
// sequence and concatenate their payloads in seq_num order.
//
// The slice must already be in seq_num order 0, 1, …, n-1. Reassemble
// does not sort; the contract is that the caller (the Receiver
// state machine) buffers out-of-order chunks and presents them in
// order. A complete-message call returns the joined payload; a
// partial or inconsistent call returns one of the sentinel errors
// above. Error priority is documented in the body below.
func Reassemble(envs []*protocolpb.Envelope) ([]byte, error) {
	if len(envs) == 0 {
		return nil, nil
	}

	// All envelopes must share the same TotalSeqs.
	want := envs[0].TotalSeqs
	if want == 0 {
		return nil, fmt.Errorf("total_seqs=0 on first envelope: %w", ErrSeqMismatch)
	}

	// Pass 1: detect duplicates, mismatched TotalSeqs, and out-of-
	// range seq_nums. We do this in slice order so the first
	// violation wins — that is, a duplicate is reported as a
	// duplicate even if it would also be "out of order" if we sorted.
	seen := make(map[uint32]struct{}, len(envs))
	for _, env := range envs {
		if env.TotalSeqs != want {
			return nil, fmt.Errorf("seq %d has TotalSeqs=%d, want %d: %w", env.SeqNum, env.TotalSeqs, want, ErrSeqMismatch)
		}
		if env.SeqNum >= want {
			return nil, fmt.Errorf("seq %d >= total_seqs %d: %w", env.SeqNum, want, ErrSeqMismatch)
		}
		if _, dup := seen[env.SeqNum]; dup {
			return nil, fmt.Errorf("seq %d appears twice: %w", env.SeqNum, ErrDuplicateSeq)
		}
		seen[env.SeqNum] = struct{}{}
	}

	// Pass 2: detect gaps (missing chunks).
	for i := uint32(0); i < want; i++ {
		if _, ok := seen[i]; !ok {
			return nil, fmt.Errorf("seq %d missing: %w", i, ErrMissingChunk)
		}
	}

	// Pass 3: detect out-of-order. If we reach here, all seqs are
	// unique, in-range, and contiguous from 0 — but the caller's
	// slice order may still be wrong (e.g. shuffled).
	for i, env := range envs {
		if env.SeqNum != uint32(i) {
			return nil, fmt.Errorf("env[%d].SeqNum=%d, want %d: %w", i, env.SeqNum, i, ErrOutOfOrder)
		}
	}

	// Concatenate.
	var out []byte
	for _, env := range envs {
		out = append(out, env.Payload...)
	}
	return out, nil
}
