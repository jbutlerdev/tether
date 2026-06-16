// Tests for the TTS mock. See plan.md §6.4.
package tts_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/tts"
)

// TestMockSynthesizer_Echoes: the mock returns a deterministic
// non-empty PCM buffer whose length is a stable function of the
// input text length. Two calls with the same text must yield
// identical bytes.
func TestMockSynthesizer_Echoes(t *testing.T) {
	m := tts.NewMock()
	ctx := context.Background()
	pcm1, sr1, err := m.Synthesize(ctx, "Hello, world")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm1) == 0 {
		t.Fatal("Synthesize returned empty PCM")
	}
	if sr1 == 0 {
		t.Fatal("Synthesize returned zero sample rate")
	}
	pcm2, sr2, err := m.Synthesize(ctx, "Hello, world")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if sr1 != sr2 {
		t.Errorf("sample rate drift: %d vs %d", sr1, sr2)
	}
	if len(pcm1) != len(pcm2) {
		t.Errorf("PCM length drift: %d vs %d", len(pcm1), len(pcm2))
	}
	for i := range pcm1 {
		if pcm1[i] != pcm2[i] {
			t.Errorf("PCM byte %d: %v vs %v", i, pcm1[i], pcm2[i])
			break
		}
	}
}

// TestMockSynthesizer_SampleRate: default is 22050 Hz.
func TestMockSynthesizer_SampleRate(t *testing.T) {
	m := tts.NewMock()
	if got := m.SampleRate(); got != 22050 {
		t.Errorf("SampleRate: want 22050, got %d", got)
	}
}

// TestMockSynthesizer_Empty: empty text still returns a (short)
// deterministic buffer.
func TestMockSynthesizer_Empty(t *testing.T) {
	m := tts.NewMock()
	pcm, sr, err := m.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize(\"\"): %v", err)
	}
	// Mock still returns a buffer (so callers can see the edge case).
	if sr != 22050 {
		t.Errorf("sample rate: want 22050, got %d", sr)
	}
	_ = pcm
}

// TestMockSynthesizer_Latency: Latency option delays Synthesize.
func TestMockSynthesizer_Latency(t *testing.T) {
	const d = 20 * time.Millisecond
	m := tts.NewMock(tts.MockOptionLatency(d))
	start := time.Now()
	if _, _, err := m.Synthesize(context.Background(), "hi"); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < d {
		t.Errorf("Synthesize returned too quickly: %v (want >= %v)", elapsed, d)
	}
}

// TestMockSynthesizer_Error: Err option makes Synthesize return the
// configured error.
func TestMockSynthesizer_Error(t *testing.T) {
	want := errors.New("mock: synth error")
	m := tts.NewMock(tts.MockOptionError(want))
	_, _, err := m.Synthesize(context.Background(), "hi")
	if !errors.Is(err, want) {
		t.Errorf("Synthesize: want %v, got %v", want, err)
	}
}

// TestMockSynthesizer_Stream: SynthesizeStream emits at least one
// chunk per input text and signals completion via the returned
// error == nil.
func TestMockSynthesizer_Stream(t *testing.T) {
	m := tts.NewMock()
	in := make(chan string, 4)
	out := make(chan []float32, 16)
	in <- "Hello."
	in <- "World."
	close(in)
	if err := m.SynthesizeStream(context.Background(), in, out); err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}
	close(out)
	var n int
	for range out {
		n++
	}
	if n < 2 {
		t.Errorf("SynthesizeStream emitted %d chunks, want >= 2", n)
	}
}

// TestMockSynthesizer_StreamPerTextHasSampleRate: every chunk in
// a stream run comes from a single synthesizer; the sample rate
// is stable.
func TestMockSynthesizer_StreamSampleRateStable(t *testing.T) {
	m := tts.NewMock()
	in := make(chan string, 1)
	out := make(chan []float32, 8)
	in <- "Once upon a time"
	close(in)
	if err := m.SynthesizeStream(context.Background(), in, out); err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}
	close(out)
	if m.SampleRate() != 22050 {
		t.Errorf("SampleRate: want 22050, got %d", m.SampleRate())
	}
}

// TestMockSynthesizer_StreamError: if the synthesizer is closed
// mid-stream, SynthesizeStream returns an error.
func TestMockSynthesizer_StreamError(t *testing.T) {
	m := tts.NewMock()
	// Closing the mock makes subsequent Synthesize calls return
	// an error, which SynthesizeStream should propagate.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	in := make(chan string, 1)
	out := make(chan []float32, 1)
	in <- "x"
	close(in)
	if err := m.SynthesizeStream(context.Background(), in, out); err == nil {
		t.Fatal("SynthesizeStream on closed mock: want error, got nil")
	}
}

// TestMockSynthesizer_StreamCtxCancel: canceling the context
// stops SynthesizeStream and returns ctx.Err().
func TestMockSynthesizer_StreamCtxCancel(t *testing.T) {
	m := tts.NewMock(tts.MockOptionLatency(5 * time.Second))
	in := make(chan string, 1)
	in <- "x" // never drained
	out := make(chan []float32, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := m.SynthesizeStream(ctx, in, out)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("SynthesizeStream on canceled ctx: want error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("SynthesizeStream did not abort promptly: %v", elapsed)
	}
}

// TestMockSynthesizer_Concurrent: race-detector clean.
func TestMockSynthesizer_Concurrent(t *testing.T) {
	m := tts.NewMock()
	ctx := context.Background()
	const N = 100
	var wg sync.WaitGroup
	var ok int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := m.Synthesize(ctx, "hello"); err != nil {
				t.Errorf("Synthesize: %v", err)
				return
			}
			atomic.AddInt64(&ok, 1)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&ok); got != N {
		t.Errorf("successful Synthesize count: want %d, got %d", N, got)
	}
}

// TestMockSynthesizer_ContextCancel: latency respects ctx.
func TestMockSynthesizer_ContextCancel(t *testing.T) {
	m := tts.NewMock(tts.MockOptionLatency(10 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _, err := m.Synthesize(ctx, "hi")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Synthesize on canceled ctx: want error, got nil")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Synthesize did not abort promptly: %v", elapsed)
	}
}

// TestMockSynthesizer_Close: Close is idempotent and Synthesize
// afterwards returns an error.
func TestMockSynthesizer_Close(t *testing.T) {
	m := tts.NewMock()
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
	if _, _, err := m.Synthesize(context.Background(), "hi"); err == nil {
		t.Error("Synthesize after Close: want error, got nil")
	}
}

// TestMockSynthesizer_TextLengthScaled: a longer text should
// produce a longer PCM buffer (the mock is proportional).
func TestMockSynthesizer_TextLengthScaled(t *testing.T) {
	m := tts.NewMock()
	short, _, err := m.Synthesize(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Synthesize(short): %v", err)
	}
	long, _, err := m.Synthesize(context.Background(), strings.Repeat("hi ", 50))
	if err != nil {
		t.Fatalf("Synthesize(long): %v", err)
	}
	if len(long) <= len(short) {
		t.Errorf("longer text produced shorter PCM: short=%d, long=%d", len(short), len(long))
	}
}

// Compile-time check.
var _ tts.Synthesizer = (*tts.Mock)(nil)
