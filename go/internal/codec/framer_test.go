package codec

import (
	"testing"
)

// TestFramer_RoundTrip_Mock verifies that EncodeBlob + DecodeBlob
// round-trips correctly with the Mock (identity) codec.
func TestFramer_RoundTrip_Mock(t *testing.T) {
	mock := NewMock()
	framer := NewFramer(mock)

	// 5 frames of 160 samples each.
	pcm := make([]int16, 5*FrameSize)
	for i := range pcm {
		pcm[i] = int16(i)
	}

	blob, err := framer.EncodeBlob(pcm)
	if err != nil {
		t.Fatalf("EncodeBlob: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("EncodeBlob returned empty blob")
	}

	// The blob should have 5 frames, each with a 2-byte length prefix
	// + 320 bytes (160 samples × 2 bytes/sample for the Mock).
	// Mock frame size = 320 bytes. Total = 5 × (2 + 320) = 1610.
	expected := 5 * (2 + FrameSize*2)
	if len(blob) != expected {
		t.Errorf("blob len = %d, want %d", len(blob), expected)
	}

	decoded, err := framer.DecodeBlob(blob)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}
	if len(decoded) != len(pcm) {
		t.Fatalf("decoded len = %d, want %d", len(decoded), len(pcm))
	}
	for i := range pcm {
		if decoded[i] != pcm[i] {
			t.Errorf("decoded[%d] = %d, want %d", i, decoded[i], pcm[i])
		}
	}
}

// TestFramer_PartialFrame verifies that a trailing partial frame
// is zero-padded and decoded correctly.
func TestFramer_PartialFrame(t *testing.T) {
	mock := NewMock()
	framer := NewFramer(mock)

	// 200 samples = 1 full frame (160) + 40 partial.
	pcm := make([]int16, 200)
	for i := range pcm {
		pcm[i] = int16(i + 1)
	}

	blob, err := framer.EncodeBlob(pcm)
	if err != nil {
		t.Fatalf("EncodeBlob: %v", err)
	}

	decoded, err := framer.DecodeBlob(blob)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}
	// The partial frame is padded to 160, so we get 160 + 160 = 320.
	if len(decoded) != 2*FrameSize {
		t.Fatalf("decoded len = %d, want %d", len(decoded), 2*FrameSize)
	}
	// First 200 samples should match the input.
	for i := 0; i < 200; i++ {
		if decoded[i] != pcm[i] {
			t.Errorf("decoded[%d] = %d, want %d", i, decoded[i], pcm[i])
		}
	}
	// Remaining 120 samples should be zero (padding).
	for i := 200; i < len(decoded); i++ {
		if decoded[i] != 0 {
			t.Errorf("decoded[%d] = %d, want 0 (padding)", i, decoded[i])
		}
	}
}

// TestFramer_EmptyInput verifies that empty PCM produces an empty blob.
func TestFramer_EmptyInput(t *testing.T) {
	framer := NewFramer(NewMock())
	blob, err := framer.EncodeBlob(nil)
	if err != nil {
		t.Fatalf("EncodeBlob(nil): %v", err)
	}
	if blob != nil {
		t.Errorf("EncodeBlob(nil) = %v, want nil", blob)
	}
	decoded, err := framer.DecodeBlob(nil)
	if err != nil {
		t.Fatalf("DecodeBlob(nil): %v", err)
	}
	if decoded != nil {
		t.Errorf("DecodeBlob(nil) = %v, want nil", decoded)
	}
}

// TestFramer_TruncatedLengthPrefix verifies that a truncated
// length prefix returns an error.
func TestFramer_TruncatedLengthPrefix(t *testing.T) {
	framer := NewFramer(NewMock())
	// Only 1 byte — not enough for a length prefix.
	_, err := framer.DecodeBlob([]byte{0x01})
	if err == nil {
		t.Fatal("DecodeBlob with 1 byte should error")
	}
}

// TestFramer_TruncatedFrame verifies that a truncated frame
// returns an error.
func TestFramer_TruncatedFrame(t *testing.T) {
	framer := NewFramer(NewMock())
	// Length prefix says 320 bytes, but only 10 follow.
	blob := []byte{0x40, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, err := framer.DecodeBlob(blob)
	if err == nil {
		t.Fatal("DecodeBlob with truncated frame should error")
	}
}
