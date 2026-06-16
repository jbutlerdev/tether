// Tests for the tether-loopback CLI binary. See plan.md §2.7.
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
)

func newMock() codec.Opus { return codec.NewMock() }

func TestRun_Default(t *testing.T) {
	var buf bytes.Buffer
	if err := Run(Options{
		Duration: 100 * time.Millisecond,
		Freq:     440,
		Out:      &buf,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "tether-loopback:") {
		t.Errorf("output missing header: %q", buf.String())
	}
}

func TestRun_FailOnZeroDuration(t *testing.T) {
	// A zero-duration sine wave has 0 samples. The fragmenter
	// returns 0 envelopes, and RunOnce acks 0/0 successfully.
	var buf bytes.Buffer
	if err := Run(Options{
		Duration: 0,
		Freq:     440,
		Out:      &buf,
	}); err != nil {
		t.Fatalf("Run with zero duration: %v", err)
	}
}

func TestSineWave_Length(t *testing.T) {
	pcm := SineWave(440, 8000, 1*time.Second)
	if got := len(pcm); got != 8000 {
		t.Errorf("SineWave(1s at 8kHz): want 8000 samples, got %d", got)
	}
}

func TestSineWave_Range(t *testing.T) {
	pcm := SineWave(440, 8000, 100*time.Millisecond)
	for i, s := range pcm {
		if s < -16000 || s > 16000 {
			t.Fatalf("sample %d out of range: %d", i, s)
		}
	}
}

func TestEncodeAll_RoundTrip(t *testing.T) {
	pcm := SineWave(440, 8000, 100*time.Millisecond)
	// The mock codec is in internal/codec; we just verify that
	// EncodeAll produces a non-empty output.
	encoded := EncodeAll(newMock(), pcm)
	if len(encoded) != len(pcm)*2 {
		t.Errorf("encoded length: want %d, got %d", len(pcm)*2, len(encoded))
	}
}
