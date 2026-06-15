// Cumulative bitmap ACK. See plan.md §2.3.
//
// The Tether ACK format is a 32-bit rolling window: NextExpectedSeq
// is the first un-acked seq, and Bitmap's bit i (LSB = i=0) covers
// seq (NextExpectedSeq + i). When all 32 bits are set, Next advances
// by 32 and Bitmap is reset to 0. Setting a seq past the window
// causes an immediate rebase: Next becomes max(Next, seq-31), so the
// caller can re-sync after a long gap.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

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

// Encode returns the three on-the-wire fields: the next-expected
// seq and the bitmap split into lo (bits 0..15) and hi (bits 16..31).
func (a *AckBitmap) Encode() (next, lo, hi uint32) {
	return a.NextExpectedSeq, a.Bitmap & 0xFFFF, (a.Bitmap >> 16) & 0xFFFF
}

// EncodeAckPayload returns the 12-byte on-the-wire payload used by
// the Ack protobuf message: little-endian uint32 next, lo, hi. This
// is the format the existing MarshalAck/DecodeAck expect.
func EncodeAckPayload(next, lo, hi uint32) []byte {
	out := make([]byte, 12)
	binary.LittleEndian.PutUint32(out[0:4], next)
	binary.LittleEndian.PutUint32(out[4:8], lo)
	binary.LittleEndian.PutUint32(out[8:12], hi)
	return out
}

// DecodeAckBitmap parses the 12-byte payload from EncodeAckPayload
// into an AckBitmap.
func DecodeAckBitmap(payload []byte) (AckBitmap, error) {
	if len(payload) == 0 {
		return AckBitmap{}, errors.New("ack: empty payload")
	}
	if len(payload) != 12 {
		return AckBitmap{}, fmt.Errorf("ack: payload length %d, want 12", len(payload))
	}
	next := binary.LittleEndian.Uint32(payload[0:4])
	lo := binary.LittleEndian.Uint32(payload[4:8])
	hi := binary.LittleEndian.Uint32(payload[8:12])
	return AckBitmap{
		NextExpectedSeq: next,
		Bitmap:          (hi << 16) | lo,
	}, nil
}
