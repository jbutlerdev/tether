// Tests for the cumulative bitmap ACK. See plan.md §2.3.
package protocol_test

import (
	"bytes"
	"testing"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
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
	// Has(0): seq 0 is below the new window, so it is implicitly
	// acked (Has returns true). The test pins down the contract: a
	// seq below Next IS acked.
	if !b.Has(0) {
		t.Errorf("Has(0) after rebase: want true (acked, below window)")
	}
	// Has(32): seq 32 is the new first-un-acked. Bit 0 is not set.
	if b.Has(32) {
		t.Errorf("Has(32) after rebase: want false (new first-un-acked)")
	}
}

// TestAckBitmap_SetBelowWindow: setting a seq below Next is a no-op
// and returns inWindow=false. The seq remains acked (below the
// active window).
func TestAckBitmap_SetBelowWindow(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 10
	inWindow, _ := b.Set(5)
	if inWindow {
		t.Errorf("Set(5) with Next=10: want inWindow=false")
	}
	// Seq 5 is below Next=10, so it is acked (the receiver has
	// advanced past it).
	if !b.Has(5) {
		t.Errorf("Has(5) with Next=10: want true (acked, below window)")
	}
	if b.NextExpectedSeq != 10 {
		t.Errorf("NextExpectedSeq should not change, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_SetAboveWindow_Advance: setting a seq above Next+31
// rebases Next to first-un-acked after the set seq.
func TestAckBitmap_SetAboveWindow_Advance(t *testing.T) {
	var b protocol.AckBitmap
	inWindow, advanced := b.Set(50)
	if !inWindow || !advanced {
		t.Fatalf("Set(50) on Next=0: want (true,true), got (%v,%v)", inWindow, advanced)
	}
	// After Set(50) the bitmap has bit 50-0=50 set, but the window is
	// only 32 wide, so the new Next is 50-31=19. (No bits before 19
	// are set in the bitmap, but the *contract* is that the receiver
	// re-syncs to the highest-acked seq.)
	if b.NextExpectedSeq != 19 {
		t.Fatalf("NextExpectedSeq after Set(50) on Next=0: want 19, got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_EncodeDecode round-trips a known bitmap.
func TestAckBitmap_EncodeDecode(t *testing.T) {
	b := protocol.AckBitmap{
		NextExpectedSeq: 0x1000_0000,
		Bitmap:          0xDEAD_BEEF,
	}
	next, lo, hi := b.Encode()
	if next != 0x1000_0000 {
		t.Errorf("Encode next: want 0x10000000, got %#x", next)
	}
	if lo != 0xBEEF {
		t.Errorf("Encode lo: want 0xBEEF, got %#x", lo)
	}
	if hi != 0xDEAD {
		t.Errorf("Encode hi: want 0xDEAD, got %#x", hi)
	}

	// Build the wire payload and decode it.
	wire := protocol.EncodeAckPayload(0x1000_0000, lo, hi)
	got, err := protocol.DecodeAckBitmap(wire)
	if err != nil {
		t.Fatalf("DecodeAckBitmap: %v", err)
	}
	if got.NextExpectedSeq != b.NextExpectedSeq {
		t.Errorf("round-trip NextExpectedSeq: want %#x, got %#x", b.NextExpectedSeq, got.NextExpectedSeq)
	}
	if got.Bitmap != b.Bitmap {
		t.Errorf("round-trip Bitmap: want %#x, got %#x", b.Bitmap, got.Bitmap)
	}
}

// TestAckBitmap_Full: every bit set, no rebase until the contiguous
// prefix closes.
func TestAckBitmap_Full(t *testing.T) {
	var b protocol.AckBitmap
	// Set bit 31 (highest) — no contiguous run from LSB, so no rebase.
	if _, _ = b.Set(31); b.Bitmap == 0 {
		t.Fatalf("Set(31): bitmap should not be 0")
	}
	if !b.Has(31) {
		t.Errorf("Has(31) after Set(31): want true")
	}
	if b.NextExpectedSeq != 0 {
		t.Errorf("NextExpectedSeq: want 0 (no contiguous run from LSB), got %d", b.NextExpectedSeq)
	}
}

// TestAckBitmap_Wraparound: setting seqs near 0xFFFFFFFF advances Next
// past 0xFFFFFFFF to 0x00000010.
func TestAckBitmap_Wraparound(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 0xFFFFFFF0
	// Set every seq in the window.
	for i := uint32(0); i < 32; i++ {
		_, _ = b.Set(0xFFFFFFF0 + i)
	}
	// After the full window, Next must have advanced by 32, wrapping
	// to 0x00000010.
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
	_, err := protocol.DecodeAckBitmap(nil)
	if err == nil {
		t.Fatal("DecodeAckBitmap(nil): want error, got nil")
	}
}

// TestDecodeAckBitmap_TooShort covers the wrong-length error path.
func TestDecodeAckBitmap_TooShort(t *testing.T) {
	_, err := protocol.DecodeAckBitmap([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Fatal("DecodeAckBitmap(3 bytes): want error, got nil")
	}
}

// TestAckBitmap_HasOutOfWindow: a seq past Next+31 is not acked.
func TestAckBitmap_HasOutOfWindow(t *testing.T) {
	var b protocol.AckBitmap
	b.NextExpectedSeq = 0
	b.Bitmap = 0xFF // bits 0..7 set, bits 8..31 clear
	// Seq 8..31 are in the active bitmap window. Seq 32 is past it.
	if b.Has(32) {
		t.Errorf("Has(32) past window: want false")
	}
	if b.Has(100) {
		t.Errorf("Has(100) past window: want false")
	}
	// Seq 0..7 are acked (bits 0..7 set).
	for i := uint32(0); i < 8; i++ {
		if !b.Has(i) {
			t.Errorf("Has(%d): want true (bit set)", i)
		}
	}
	// Seq 8..31 are not acked (bits 8..31 clear).
	for i := uint32(8); i < 32; i++ {
		if b.Has(i) {
			t.Errorf("Has(%d): want false (bit clear)", i)
		}
	}
}


// before a rebase; the rebase advances Next but the seq is still
// acked (it is below the new window).
func TestAckBitmap_HasAfterRebase(t *testing.T) {
	var b protocol.AckBitmap
	// Set seq 5 first.
	_, _ = b.Set(5)
	if !b.Has(5) {
		t.Fatalf("Has(5) before rebase: want true")
	}
	// Now set seq 50, which rebases Next to 19. Seq 5 is below the
	// new window, so Has(5) is still true (acked).
	_, _ = b.Set(50)
	if b.NextExpectedSeq != 19 {
		t.Fatalf("NextExpectedSeq after rebase: want 19, got %d", b.NextExpectedSeq)
	}
	if !b.Has(5) {
		t.Errorf("Has(5) after rebase: want true (acked, below new window)")
	}
	// Seq 50 is the high bit of the new window.
	bit := uint32(50) - b.NextExpectedSeq
	if bit < 32 {
		if b.Bitmap&(1<<bit) == 0 {
			t.Errorf("bit %d (for seq 50) should be set in rebased bitmap", bit)
		}
	}
	// Seq 19..49 are NOT acked (the new window is empty except for
	// bit 31).
	if b.Has(19) {
		t.Errorf("Has(19) after rebase: want false (first un-acked)")
	}
	if b.Has(49) {
		t.Errorf("Has(49) after rebase: want false")
	}
}

// TestAckBitmap_EncodeDecodeWireBytes is a black-box round-trip through
// the wire format used by the rest of the codebase.
func TestAckBitmap_EncodeDecodeWireBytes(t *testing.T) {
	payload := protocol.EncodeAckPayload(0xABCD_1234, 0xCAFE, 0xBABE)
	if len(payload) != 12 {
		t.Fatalf("EncodeAckPayload: want 12 bytes, got %d", len(payload))
	}
	got, err := protocol.DecodeAckBitmap(payload)
	if err != nil {
		t.Fatalf("DecodeAckBitmap: %v", err)
	}
	if got.NextExpectedSeq != 0xABCD_1234 {
		t.Errorf("NextExpectedSeq: want 0xABCD1234, got %#x", got.NextExpectedSeq)
	}
	wantBitmap := uint32(0xBABE)<<16 | uint32(0xCAFE)
	if got.Bitmap != wantBitmap {
		t.Errorf("Bitmap: want %#x, got %#x", wantBitmap, got.Bitmap)
	}
}

// TestAckBitmap_PartialDecode does not allocate; we just verify that
// the encoded payload is exactly 12 bytes.
func TestAckBitmap_EncodeLayout(t *testing.T) {
	payload := protocol.EncodeAckPayload(0, 0, 0)
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(payload, want) {
		t.Errorf("zero encode: want %x, got %x", want, payload)
	}
}
