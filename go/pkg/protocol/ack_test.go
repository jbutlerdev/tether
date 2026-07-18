// Tests for the cumulative bitmap ACK. See research.md §8.5 / §8.6.
package protocol_test

import (
	"bytes"
	"testing"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// TestAckBitmap_SetInWindow: setting a seq in the window must mark it
// acked and must NOT advance NextExpectedSeq.
func TestAckBitmap_SetInWindow(t *testing.T) {
	var b protocol.AckBitmap
	// Next starts at 0, so seq 5 is in the window.
	inWindow, advanced := b.Set(5)
	if !inWindow || advanced {
		t.Fatalf("Set(5) on Next=0: want (true,false), got (%v,%v)", inWindow, advanced)
	}
	if !b.Has(5) {
		t.Errorf("Has(5) after Set(5): want true")
	}
	if b.NextExpectedSeq != 0 {
		t.Errorf("NextExpectedSeq: want 0, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_RebaseAdvancesNext: setting every seq in the window
// (Next..Next+31) advances Next by 32.
func TestAckBitmap_RebaseAdvancesNext(t *testing.T) {
	var b protocol.AckBitmap
	for i := uint32(0); i < 32; i++ {
		_, _ = b.Set(i)
	}
	if b.NextExpectedSeq != 32 {
		t.Fatalf("NextExpectedSeq after 32 Sets from 0: want 32, got %d", b.NextExpectedSeq)
	}
	// Bitmap should now be empty (rolled past).
	if b.Bitmap != 0 {
		t.Errorf("Bitmap after full window: want 0, got %#x", b.Bitmap)
	}
	if !b.Has(0) {
		t.Errorf("Has(0) after rebase: want true (acked, below window)")
	}
	if b.Has(32) {
		t.Errorf("Has(32) after rebase: want false (new first-un-acked)")
	}
}

// TestAckBitmap_SetBelowWindow: setting a seq below Next is a no-op
// and returns inWindow=false.
func TestAckBitmap_SetBelowWindow(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 10
	inWindow, _ := b.Set(5)
	if inWindow {
		t.Errorf("Set(5) with Next=10: want inWindow=false")
	}
	if !b.Has(5) {
		t.Errorf("Has(5) with Next=10: want true (acked, below window)")
	}
	if b.NextExpectedSeq != 10 {
		t.Errorf("NextExpectedSeq should not change, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_SetAboveWindow_Advance: setting a seq above Next+31
// rebases Next.
func TestAckBitmap_SetAboveWindow_Advance(t *testing.T) {
	var b protocol.AckBitmap
	inWindow, advanced := b.Set(50)
	if !inWindow || !advanced {
		t.Fatalf("Set(50) on Next=0: want (true,true), got (%v,%v)", inWindow, advanced)
	}
	if b.NextExpectedSeq != 19 {
		t.Fatalf("NextExpectedSeq after Set(50) on Next=0: want 19, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_EncodeDecode round-trips a known bitmap through the
// 28-byte wire payload (research.md §8.6).
func TestAckBitmap_EncodeDecode(t *testing.T) {
	convID := bytes.Repeat([]byte{0xAB}, 16)
	const msgID = uint32(0xCAFEBABE)
	b := protocol.AckBitmap{
		NextExpectedSeq: 0x0000_BEEF, // fits in uint16 on the wire
		Bitmap:          0xDEAD_BEEF,
	}
	next, lo, hi := b.Encode()
	wire := protocol.EncodeAckPayload(convID, msgID, next, lo, hi)
	if len(wire) != 28 {
		t.Fatalf("EncodeAckPayload: want 28 bytes, got %d", len(wire))
	}
	got, gotConv, gotMsg, err := protocol.DecodeAckBitmap(wire)
	if err != nil {
		t.Fatalf("DecodeAckBitmap: %v", err)
	}
	if got.NextExpectedSeq != b.NextExpectedSeq {
		t.Errorf("round-trip NextExpectedSeq: want %#x, got %#x", b.NextExpectedSeq, got.NextExpectedSeq)
	}
	if got.Bitmap != b.Bitmap {
		t.Errorf("round-trip Bitmap: want %#x, got %#x", b.Bitmap, got.Bitmap)
	}
	if !bytes.Equal(gotConv, convID) {
		t.Errorf("round-trip convID: want %x, got %x", convID, gotConv)
	}
	if gotMsg != msgID {
		t.Errorf("round-trip msgID: want %#x, got %#x", msgID, gotMsg)
	}
}

// TestAckBitmap_Full: every bit set, no rebase until the contiguous
// prefix closes.
func TestAckBitmap_Full(t *testing.T) {
	var b protocol.AckBitmap
	if _, _ = b.Set(31); b.Bitmap == 0 {
		t.Fatalf("Set(31): bitmap should not be 0")
	}
	if !b.Has(31) {
		t.Errorf("Has(31) after Set(31): want true")
	}
	if b.NextExpectedSeq != 0 {
		t.Errorf("NextExpectedSeq: want 0, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_Wraparound: setting seqs near 0xFFFFFFFF advances Next
// past 0xFFFFFFFF to 0x00000010.
func TestAckBitmap_Wraparound(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 0xFFFFFFF0
	for i := uint32(0); i < 32; i++ {
		_, _ = b.Set(0xFFFFFFF0 + i)
	}
	if b.NextExpectedSeq != 0x00000010 {
		t.Fatalf("NextExpectedSeq wraparound: want 0x00000010, got %#x", b.NextExpectedSeq)
	}
	if b.Bitmap != 0 {
		t.Errorf("Bitmap after wraparound: want 0, got %#x", b.Bitmap)
	}
}

// TestAckBitmap_EncodeZero covers the zero-state edge of Encode.
func TestAckBitmap_EncodeZero(t *testing.T) {
	var b protocol.AckBitmap
	next, lo, hi := b.Encode()
	if next != 0 || lo != 0 || hi != 0 {
		t.Errorf("zero bitmap encode: want (0,0,0), got (%#x,%#x,%#x)", next, lo, hi)
	}
}

// TestDecodeAckBitmap_Empty covers the empty-buffer error path.
func TestDecodeAckBitmap_Empty(t *testing.T) {
	_, _, _, err := protocol.DecodeAckBitmap(nil)
	if err == nil {
		t.Fatal("DecodeAckBitmap(nil): want error, got nil")
	}
}

// TestDecodeAckBitmap_TooShort covers the wrong-length error path.
func TestDecodeAckBitmap_TooShort(t *testing.T) {
	_, _, _, err := protocol.DecodeAckBitmap([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("DecodeAckBitmap(3 bytes): want error, got nil")
	}
}

// TestAckBitmap_HasOutOfWindow: a seq past Next+31 is not acked.
func TestAckBitmap_HasOutOfWindow(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 0
	b.Bitmap = 0xFF // bits 0..7 set
	if b.Has(32) {
		t.Errorf("Has(32) past window: want false")
	}
	if b.Has(100) {
		t.Errorf("Has(100) past window: want false")
	}
	for i := uint32(0); i < 8; i++ {
		if !b.Has(i) {
			t.Errorf("Has(%d): want true (bit set)", i)
		}
	}
	for i := uint32(8); i < 32; i++ {
		if b.Has(i) {
			t.Errorf("Has(%d): want false (bit clear)", i)
		}
	}
}

// TestAckBitmap_HasAfterRebase: the rebase advances Next but a
// previously-acked seq is still acked (below the new window).
func TestAckBitmap_HasAfterRebase(t *testing.T) {
	var b protocol.AckBitmap
	_, _ = b.Set(5)
	if !b.Has(5) {
		t.Fatalf("Has(5) before rebase: want true")
	}
	_, _ = b.Set(50)
	if b.NextExpectedSeq != 19 {
		t.Fatalf("NextExpectedSeq after rebase: want 19, got %d", b.NextExpectedSeq)
	}
	if !b.Has(5) {
		t.Errorf("Has(5) after rebase: want true (acked, below new window)")
	}
	bit := uint32(50) - b.NextExpectedSeq
	if bit < 32 {
		if b.Bitmap&(1<<bit) == 0 {
			t.Errorf("bit %d (for seq 50) should be set in rebased bitmap", bit)
		}
	}
	if b.Has(19) {
		t.Errorf("Has(19) after rebase: want false (first un-acked)")
	}
}

// TestAckBitmap_EncodeDecodeWireBytes is a black-box round-trip
// through the 28-byte wire payload (research.md §8.6), verifying the
// conversation_id and message_id are carried and the CRC verifies.
func TestAckBitmap_EncodeDecodeWireBytes(t *testing.T) {
	convID := bytes.Repeat([]byte{0x0F}, 16)
	const msgID = uint32(0x12345678)
	payload := protocol.EncodeAckPayload(convID, msgID, 0xABCD, 0xCAFE, 0xBABE)
	if len(payload) != 28 {
		t.Fatalf("EncodeAckPayload: want 28 bytes, got %d", len(payload))
	}
	got, gotConv, gotMsg, err := protocol.DecodeAckBitmap(payload)
	if err != nil {
		t.Fatalf("DecodeAckBitmap: %v", err)
	}
	if got.NextExpectedSeq != 0xABCD {
		t.Errorf("NextExpectedSeq: want 0xABCD, got %#x", got.NextExpectedSeq)
	}
	wantBitmap := uint32(0xBABE)<<16 | uint32(0xCAFE)
	if got.Bitmap != wantBitmap {
		t.Errorf("Bitmap: want %#x, got %#x", wantBitmap, got.Bitmap)
	}
	if !bytes.Equal(gotConv, convID) {
		t.Errorf("convID: want %x, got %x", convID, gotConv)
	}
	if gotMsg != msgID {
		t.Errorf("msgID: want %#x, got %#x", msgID, gotMsg)
	}
}

// TestAckPayload_CRCRejectsCorruption: flipping one bit in the payload
// must fail the CRC check.
func TestAckPayload_CRCRejectsCorruption(t *testing.T) {
	convID := bytes.Repeat([]byte{0x11}, 16)
	payload := protocol.EncodeAckPayload(convID, 42, 5, 0x00FF, 0)
	// Corrupt one byte in the bitmap region (offset 22).
	payload[22] ^= 0x01
	_, _, _, err := protocol.DecodeAckBitmap(payload)
	if err == nil {
		t.Fatal("DecodeAckBitmap(corrupted): want CRC error, got nil")
	}
}

// TestAckPayload_ZeroConvIDAccepted: a legacy/test ACK with an
// all-zero conversation_id is still decodable (the sender treats a
// zero conv_id as "accept regardless" for back-compat).
func TestAckPayload_ZeroConvIDAccepted(t *testing.T) {
	payload := protocol.EncodeAckPayload(nil, 0, 4, 0, 0)
	got, gotConv, gotMsg, err := protocol.DecodeAckBitmap(payload)
	if err != nil {
		t.Fatalf("DecodeAckBitmap: %v", err)
	}
	if hasNonZero(gotConv) {
		t.Errorf("zero convID: want all-zero, got %x", gotConv)
	}
	if gotMsg != 0 {
		t.Errorf("zero msgID: want 0, got %#x", gotMsg)
	}
	if got.NextExpectedSeq != 4 {
		t.Errorf("NextExpectedSeq: want 4, got %d", got.NextExpectedSeq)
	}
}

// TestMarshalAck_RoundTrip: MarshalAck/DecodeAck round-trip the
// protocolpb.Ack through the 28-byte fixed payload.
func TestMarshalAck_RoundTrip(t *testing.T) {
	convID := bytes.Repeat([]byte{0x22}, 16)
	wire, err := protocol.MarshalAck(protocolpbAck(convID, 99, 7, 0x00AA, 0))
	if err != nil {
		t.Fatalf("MarshalAck: %v", err)
	}
	if len(wire) != 28 {
		t.Fatalf("MarshalAck: want 28 bytes, got %d", len(wire))
	}
	got, err := protocol.DecodeAck(wire)
	if err != nil {
		t.Fatalf("DecodeAck: %v", err)
	}
	if !bytes.Equal(got.ConversationId, convID) {
		t.Errorf("convID: want %x, got %x", convID, got.ConversationId)
	}
	if got.MessageId != 99 {
		t.Errorf("msgID: want 99, got %d", got.MessageId)
	}
	if got.NextExpectedSeq != 7 {
		t.Errorf("next: want 7, got %d", got.NextExpectedSeq)
	}
}

// hasNonZero reports whether any byte is non-zero.
func hasNonZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return true
		}
	}
	return false
}

// protocolpbAck builds a *protocolpb.Ack for test brevity.
func protocolpbAck(convID []byte, msgID, next, lo, hi uint32) *protocolpb.Ack {
	return &protocolpb.Ack{
		ConversationId:  convID,
		MessageId:       msgID,
		NextExpectedSeq: next,
		AckBitmapLo:     lo,
		AckBitmapHi:     hi,
	}
}
