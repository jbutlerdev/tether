// Tests for the Opus codec wrapper. See plan.md §2.8.
package codec_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/codec"
)

// TestMockOpus_RoundTrip: encode then decode yields the same PCM.
func TestMockOpus_RoundTrip(t *testing.T) {
	c := codec.NewMock()
	pcm := makePcm(160, 0x12)
	encoded, err := c.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// Re-encode the decoded PCM and compare the bytes; the mock
	// codec is deterministic so this is a robust equality check.
	reencoded, err := c.Encode(decoded)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Errorf("round-trip mismatch")
	}
}

// TestMockOpus_FrameSize: 160 samples at 8 kHz.
func TestMockOpus_FrameSize(t *testing.T) {
	c := codec.NewMock()
	if got := c.FrameSize(); got != 160 {
		t.Errorf("FrameSize: want 160, got %d", got)
	}
	if got := c.SampleRate(); got != 8000 {
		t.Errorf("SampleRate: want 8000, got %d", got)
	}
}

// TestMockOpus_EmptyInput: 0 samples returns empty output, no error.
func TestMockOpus_EmptyInput(t *testing.T) {
	c := codec.NewMock()
	encoded, err := c.Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil): %v", err)
	}
	if len(encoded) != 0 {
		t.Errorf("Encode(nil): want empty, got %d bytes", len(encoded))
	}
	decoded, err := c.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("Decode(nil): want empty, got %d samples", len(decoded))
	}
}

// TestMockOpus_OversizeFrame: 320 samples returns error (must be
// exact multiples of 160).
func TestMockOpus_OversizeFrame(t *testing.T) {
	c := codec.NewMock()
	_, err := c.Encode(make([]int16, 320))
	if err == nil {
		t.Fatal("Encode(320 samples): want error, got nil")
	}
}

// TestMockOpus_ShortFrame: 100 samples returns error.
func TestMockOpus_ShortFrame(t *testing.T) {
	c := codec.NewMock()
	_, err := c.Encode(make([]int16, 100))
	if err == nil {
		t.Fatal("Encode(100 samples): want error, got nil")
	}
}

// TestMockOpus_Concurrent: race-detector clean.
func TestMockOpus_Concurrent(t *testing.T) {
	c := codec.NewMock()
	var wg sync.WaitGroup
	const N = 100
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pcm := makePcm(160, 0x42)
			encoded, err := c.Encode(pcm)
			if err != nil {
				t.Errorf("Encode: %v", err)
				return
			}
			if _, err := c.Decode(encoded); err != nil {
				t.Errorf("Decode: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestMockOpus_DecodeOddBytes covers the "odd byte count" error
// path.
func TestMockOpus_DecodeOddBytes(t *testing.T) {
	c := codec.NewMock()
	if _, err := c.Decode([]byte{0x01, 0x02, 0x03}); err == nil {
		t.Fatal("Decode(3 bytes): want error, got nil")
	}
}

// TestMockOpus_Close is a no-op for the mock.
func TestMockOpus_Close(t *testing.T) {
	c := codec.NewMock()
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent.
	if err := c.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
}

// makePcm builds a deterministic int16 PCM buffer of the given size
// filled with `fill`.
func makePcm(n int, fill byte) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(fill)<<8 | int16(fill)
	}
	return out
}
