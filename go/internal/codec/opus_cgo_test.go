//go:build opus

package codec

import (
	"math"
	"testing"
)

func TestCgoCodecRoundTrip(t *testing.T) {
	c, err := NewCgoCodec()
	if err != nil {
		t.Fatalf("NewCgoCodec: %v", err)
	}
	defer c.Close()

	if c.FrameSize() != FrameSize {
		t.Errorf("FrameSize: got %d, want %d", c.FrameSize(), FrameSize)
	}
	if c.SampleRate() != SampleRate {
		t.Errorf("SampleRate: got %d, want %d", c.SampleRate(), SampleRate)
	}

	// Generate a 1 kHz sine wave at 8 kHz / 16-bit / mono for one
	// frame (160 samples = 20 ms).
	pcm := make([]int16, FrameSize)
	for i := range pcm {
		t := float64(i) / float64(SampleRate)
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*1000*t))
	}

	encoded, err := c.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// At 16 kbps / 20 ms, a frame is ~40 bytes. VBR can vary; just
	// check it's non-trivial.
	if len(encoded) == 0 {
		t.Fatal("encoded output is empty")
	}
	if len(encoded) > 1276 {
		t.Errorf("encoded too large: %d bytes (max 1276)", len(encoded))
	}

	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(decoded) != FrameSize {
		t.Errorf("decoded length: got %d, want %d", len(decoded), FrameSize)
	}

	// The round-trip is lossy (Opus is a lossy codec), but the RMS
	// energy should be in the same ballpark. We check that the
	// decoded signal has non-trivial energy (not silence).
	var sumSq float64
	for _, s := range decoded {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(decoded)))
	if rms < 100 {
		t.Errorf("decoded RMS too low (%.1f) — codec may be broken", rms)
	}
}

func TestCgoCodecMultipleFrames(t *testing.T) {
	c, err := NewCgoCodec()
	if err != nil {
		t.Fatalf("NewCgoCodec: %v", err)
	}
	defer c.Close()

	// Encode 10 frames and decode them; verify no crash and
	// consistent frame sizes.
	for frame := 0; frame < 10; frame++ {
		pcm := make([]int16, FrameSize)
		freq := 500.0 + float64(frame)*100 // 500..1400 Hz
		for i := range pcm {
			tt := float64(i) / float64(SampleRate)
			pcm[i] = int16(8000 * math.Sin(2*math.Pi*freq*tt))
		}
		encoded, err := c.Encode(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frame, err)
		}
		decoded, err := c.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode frame %d: %v", frame, err)
		}
		if len(decoded) != FrameSize {
			t.Errorf("frame %d decoded len: got %d, want %d", frame, len(decoded), FrameSize)
		}
	}
}

func TestCgoCodecWrongFrameSize(t *testing.T) {
	c, err := NewCgoCodec()
	if err != nil {
		t.Fatalf("NewCgoCodec: %v", err)
	}
	defer c.Close()

	// Encode with the wrong number of samples.
	_, err = c.Encode(make([]int16, FrameSize-1))
	if err == nil {
		t.Fatal("expected error for wrong frame size")
	}
}

func TestCgoCodecDecodeEmpty(t *testing.T) {
	c, err := NewCgoCodec()
	if err != nil {
		t.Fatalf("NewCgoCodec: %v", err)
	}
	defer c.Close()

	decoded, err := c.Decode(nil)
	if err != nil {
		t.Fatalf("Decode(nil): %v", err)
	}
	if decoded != nil {
		t.Errorf("Decode(nil): got %d samples, want nil", len(decoded))
	}
}

func TestCgoCodecCloseIsIdempotent(t *testing.T) {
	c, err := NewCgoCodec()
	if err != nil {
		t.Fatalf("NewCgoCodec: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
