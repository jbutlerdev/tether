// Cumulative bitmap ACK. See research.md §8.5 / §8.6.
//
// The Tether ACK is a self-describing 28-byte fixed payload
// (research.md §8.6) that rides in an ACK Envelope's Payload:
//
//	Offset  Size  Field
//	0       16    conversation_id   // matches the acked message
//	16      4     msg_id            // LE uint32; matches the acked message
//	20      2     next_expected_seq // LE uint16; first un-acked seq
//	22      2     ack_bitmap_lo     // 16 bits covering [next..next+15]
//	24      2     ack_bitmap_hi     // 16 bits covering [next+16..next+31]
//	26      2     crc16             // CRC-16/CCITT-FALSE over bytes 0..25
//
// Carrying conversation_id + msg_id in the ACK payload (not just on
// the envelope header) makes the ACK self-describing: a sender can
// reject an ACK that does not belong to its message even if the
// envelope header were stripped or corrupted. This is the
// multi-conversation safety guarantee (REVIEW.md F3) — an ACK for
// conversation A can never ack envelopes in conversation B.
//
// The 32-bit rolling window: NextExpectedSeq is the first un-acked
// seq, and Bitmap's bit i (LSB = i=0) covers seq (NextExpectedSeq +
// i). When all 32 bits are set, Next advances by 32 and Bitmap is
// reset to 0. Setting a seq past the window causes an immediate
// rebase: Next becomes max(Next, seq-31), so the caller can re-sync
// after a long gap.

package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// ackPayloadSize is the fixed on-wire size of an ACK payload
// (research.md §8.6): 16 + 4 + 2 + 2 + 2 + 2 = 28 bytes.
const ackPayloadSize = 28

// AckBitmap is a 32-bit rolling ACK window starting at NextExpectedSeq.
//
// Invariant: at any time, every seq < NextExpectedSeq is "acked" by
// implication (the bitmap is a sliding window). The Bitmap field only
// describes the 32 seqs in [Next, Next+31].
type AckBitmap struct {
	NextExpectedSeq uint32
	Bitmap          uint32
}

// windowSize is the fixed width of the bitmap.
const windowSize = 32

// Set marks seq as acked. Returns:
//
//   - inWindow: true if seq is in [Next, Next+31] OR above the window
//     (i.e., the call is meaningful). False if seq is below Next, in
//     which case the ACK is a no-op.
//   - advanced: true if the call caused Next to advance (either
//     because the contiguous set-bit run from LSB reached a clear
//     bit, or because seq was past the window and we rebased).
func (a *AckBitmap) Set(seq uint32) (inWindow bool, advanced bool) {
	// Below window: a duplicate ACK, no-op.
	if seq < a.NextExpectedSeq {
		return false, false
	}

	offset := seq - a.NextExpectedSeq

	// Inside the window: set the bit.
	if offset < windowSize {
		a.Bitmap |= 1 << offset
	} else {
		// Past the window: rebase. The new Next is seq-31, so the
		// high bit of the new window (offset=31) corresponds to seq
		// itself. The rebase clears the bitmap and sets just that
		// bit.
		a.NextExpectedSeq = seq - (windowSize - 1)
		a.Bitmap = 1 << (windowSize - 1)
		return true, true
	}

	// Rebase forward: advance Next past the contiguous run of set
	// bits from LSB. This implements the cumulative semantics — as
	// soon as the LSB run is closed by a clear bit, the receiver
	// commits to "everything below is acked" and shifts the window.
	shift := uint32(0)
	for shift < windowSize {
		if a.Bitmap&(1<<shift) == 0 {
			break
		}
		shift++
	}
	if shift > 0 {
		a.NextExpectedSeq += shift
		a.Bitmap >>= shift
		return true, true
	}
	return true, false
}

// Has reports whether seq is acked (either by the bitmap or by being
// below NextExpectedSeq).
func (a *AckBitmap) Has(seq uint32) bool {
	if seq < a.NextExpectedSeq {
		return true
	}
	offset := seq - a.NextExpectedSeq
	if offset >= windowSize {
		return false
	}
	return a.Bitmap&(1<<offset) != 0
}

// Encode returns the three logical fields: the next-expected
// seq and the bitmap split into lo (bits 0..15) and hi (bits 16..31).
func (a *AckBitmap) Encode() (next, lo, hi uint32) {
	return a.NextExpectedSeq, a.Bitmap & 0xFFFF, (a.Bitmap >> 16) & 0xFFFF
}

// EncodeAckPayload returns the 28-byte on-wire ACK payload
// (research.md §8.6): conversation_id(16) + msg_id(4) +
// next_expected_seq(2) + ack_bitmap_lo(2) + ack_bitmap_hi(2) +
// crc16(2). The CRC-16/CCITT-FALSE covers the 26 bytes before it.
// conversation_id must be 16 bytes (or nil/empty for a legacy test
// ACK, which is zero-padded).
func EncodeAckPayload(convID []byte, msgID, next, lo, hi uint32) []byte {
	out := make([]byte, ackPayloadSize)
	if len(convID) >= 16 {
		copy(out[0:16], convID[:16])
	} else if len(convID) > 0 {
		copy(out[0:len(convID)], convID)
	}
	binary.LittleEndian.PutUint32(out[16:20], msgID)
	binary.LittleEndian.PutUint16(out[20:22], uint16(next))
	binary.LittleEndian.PutUint16(out[22:24], uint16(lo))
	binary.LittleEndian.PutUint16(out[24:26], uint16(hi))
	crc := Crc16CCITT(out[0:26])
	binary.LittleEndian.PutUint16(out[26:28], crc)
	return out
}

// DecodeAckBitmap parses the 28-byte ACK payload from EncodeAckPayload
// into an AckBitmap, verifying the CRC-16. It also returns the
// conversation_id and message_id carried in the payload so the sender
// can validate the ACK belongs to its message (REVIEW.md F3).
func DecodeAckBitmap(payload []byte) (AckBitmap, []byte, uint32, error) {
	if len(payload) != ackPayloadSize {
		return AckBitmap{}, nil, 0, fmt.Errorf("ack: payload length %d, want %d: %w", len(payload), ackPayloadSize, ErrTruncated)
	}
	stored := binary.LittleEndian.Uint16(payload[26:28])
	if Crc16CCITT(payload[0:26]) != stored {
		return AckBitmap{}, nil, 0, ErrBadCRC
	}
	convID := make([]byte, 16)
	copy(convID, payload[0:16])
	msgID := binary.LittleEndian.Uint32(payload[16:20])
	next := uint32(binary.LittleEndian.Uint16(payload[20:22]))
	lo := uint32(binary.LittleEndian.Uint16(payload[22:24]))
	hi := uint32(binary.LittleEndian.Uint16(payload[24:26]))
	return AckBitmap{
		NextExpectedSeq: next,
		Bitmap:          (hi << 16) | lo,
	}, convID, msgID, nil
}

// ErrAckCRC is returned when an ACK payload's CRC-16 does not verify.
var ErrAckCRC = errors.New("ack: CRC mismatch")

// MarshalAck serialises an Ack into the 28-byte fixed payload
// (research.md §8.6). It is the Ack-typed counterpart of Encode;
// the data path uses EncodeAckPayload directly.
func MarshalAck(ack *protocolpb.Ack) ([]byte, error) {
	if ack == nil {
		return nil, errors.New("protocol: nil ack")
	}
	return EncodeAckPayload(ack.ConversationId, ack.MessageId,
		ack.NextExpectedSeq, ack.AckBitmapLo, ack.AckBitmapHi), nil
}

// DecodeAck parses a 28-byte fixed ACK payload into an Ack,
// verifying the CRC-16.
func DecodeAck(buf []byte) (*protocolpb.Ack, error) {
	bmp, convID, msgID, err := DecodeAckBitmap(buf)
	if err != nil {
		return nil, fmt.Errorf("decode ack: %w", err)
	}
	return &protocolpb.Ack{
		ConversationId:  convID,
		MessageId:       msgID,
		NextExpectedSeq: bmp.NextExpectedSeq,
		AckBitmapLo:     bmp.Bitmap & 0xFFFF,
		AckBitmapHi:     (bmp.Bitmap >> 16) & 0xFFFF,
	}, nil
}
