// Tests for the mock STT transcriber. See plan.md §6.1.
package stt_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/stt"
)

// TestMockTranscriber_EchoesDeterministic: same PCM hash → same text.
// "Echoes filename" in the plan is a typo; the mock is deterministic
// over its input bytes. We use a stable hash → hex string.
func TestMockTranscriber_EchoesDeterministic(t *testing.T) {
	m := stt.NewMock()
	pcm := []float32{0.1, 0.2, -0.1, 0.4}
	ctx := context.Background()

	got1, err := m.Transcribe(ctx, pcm, 16000)
	if err != nil {
		t.Fatalf("Transcribe #1: %v", err)
	}
	got2, err := m.Transcribe(ctx, pcm, 16000)
	if err != nil {
		t.Fatalf("Transcribe #2: %v", err)
	}
	if got1 == "" {
		t.Fatal("Transcribe returned empty text")
	}
	if got1 != got2 {
		t.Errorf("Transcribe not deterministic: %q vs %q", got1, got2)
	}

	// Different input must produce a different (or at least
	// not-equal) string; we don't promise a particular hash.
	pcm2 := []float32{0.9, 0.8, -0.7}
	got3, err := m.Transcribe(ctx, pcm2, 16000)
	if err != nil {
		t.Fatalf("Transcribe #3: %v", err)
	}
	if got3 == got1 {
		t.Errorf("different input produced identical text: %q", got3)
	}
}

// TestMockTranscriber_Records: every Transcribe call is recorded.
func TestMockTranscriber_Records(t *testing.T) {
	m := stt.NewMock()
	ctx := context.Background()

	calls := [][]float32{
		{0.1, 0.2},
		{0.3, 0.4, 0.5},
	}
	for i, pcm := range calls {
		if _, err := m.Transcribe(ctx, pcm, 8000); err != nil {
			t.Fatalf("Transcribe #%d: %v", i, err)
		}
	}
	got := m.Calls()
	if len(got) != len(calls) {
		t.Fatalf("Calls: want %d, got %d", len(calls), len(got))
	}
	for i, c := range got {
		if len(c.PCM) != len(calls[i]) {
			t.Errorf("Calls[%d].PCM: want %d samples, got %d", i, len(calls[i]), len(c.PCM))
		}
		if c.SampleRate != 8000 {
			t.Errorf("Calls[%d].SampleRate: want 8000, got %d", i, c.SampleRate)
		}
	}
}

// TestMockTranscriber_SimulatesLatency: Latency option delays the
// call. The total wall-clock time must be at least the configured
// delay.
func TestMockTranscriber_SimulatesLatency(t *testing.T) {
	const delay = 20 * time.Millisecond
	m := stt.NewMock(stt.MockOptionLatency(delay))
	ctx := context.Background()

	start := time.Now()
	if _, err := m.Transcribe(ctx, []float32{0.1}, 16000); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < delay {
		t.Errorf("Transcribe returned too quickly: %v (want >= %v)", elapsed, delay)
	}
}

// TestMockTranscriber_SimulatesError: Err option makes Transcribe
// return the configured error.
func TestMockTranscriber_SimulatesError(t *testing.T) {
	want := errors.New("mock: simulated error")
	m := stt.NewMock(stt.MockOptionError(want))
	_, err := m.Transcribe(context.Background(), []float32{0.1}, 16000)
	if !errors.Is(err, want) {
		t.Errorf("Transcribe: want %v, got %v", want, err)
	}
}

// TestMockTranscriber_Empty: empty PCM still returns a deterministic
// text, not an error.
func TestMockTranscriber_Empty(t *testing.T) {
	m := stt.NewMock()
	got, err := m.Transcribe(context.Background(), nil, 16000)
	if err != nil {
		t.Fatalf("Transcribe(nil): %v", err)
	}
	if got == "" {
		t.Error("Transcribe(nil) returned empty text")
	}
}

// TestMockTranscriber_Close: Close is idempotent and Transcribe
// afterwards returns an error.
func TestMockTranscriber_Close(t *testing.T) {
	m := stt.NewMock()
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent.
	if err := m.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
	if _, err := m.Transcribe(context.Background(), []float32{0.1}, 16000); err == nil {
		t.Error("Transcribe after Close: want error, got nil")
	}
}

// TestMockTranscriber_Concurrent: race-detector clean with many
// goroutines calling Transcribe and inspecting Calls.
func TestMockTranscriber_Concurrent(t *testing.T) {
	m := stt.NewMock()
	ctx := context.Background()
	const N = 100
	var wg sync.WaitGroup
	var count int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Transcribe(ctx, []float32{0.1, 0.2}, 16000); err != nil {
				t.Errorf("Transcribe: %v", err)
				return
			}
			atomic.AddInt64(&count, 1)
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&count); got != N {
		t.Errorf("successful Transcribe count: want %d, got %d", N, got)
	}
	if got := len(m.Calls()); got != N {
		t.Errorf("Calls: want %d, got %d", N, got)
	}
}

// TestMockTranscriber_ContextCancel: latency delay respects ctx.
func TestMockTranscriber_ContextCancel(t *testing.T) {
	m := stt.NewMock(stt.MockOptionLatency(10 * time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, err := m.Transcribe(ctx, []float32{0.1}, 16000)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Transcribe on canceled ctx: want error, got nil")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Transcribe did not abort promptly: %v", elapsed)
	}
}

// Compile-time check that *Mock implements Transcriber.
var _ stt.Transcriber = (*stt.Mock)(nil)
