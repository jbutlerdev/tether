// Real-model tests for the Parakeet transcriber. See plan.md §6.2.
//
// These tests require a Parakeet-TDT 0.6B v2 int8 model installed
// at the path pointed to by $TETHER_MODELS/parakeet-tdt. The
// tests are gated by the `parakeet` build tag so the production
// build (no sherpa-onnx) does not compile this file.
//
// Run with:
//
//	cd go && go test -tags parakeet ./internal/stt/
//
// To fetch the model: scripts/fetch-models.sh
//
//go:build parakeet

package stt_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/stt"
)

// parakeetModelDir returns the directory containing the Parakeet
// model files, or skips the test if the model is not present.
func parakeetModelDir(tb testing.TB) string {
	tb.Helper()
	root := os.Getenv("TETHER_MODELS")
	if root == "" {
		root = "/var/lib/tether"
	}
	// The fetch script names the directory
	// sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8.
	candidates := []string{
		filepath.Join(root, "parakeet-tdt", "sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8"),
		filepath.Join(root, "parakeet-tdt"),
		filepath.Join(root, "sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	tb.Skipf("Parakeet model not found under %s; run scripts/fetch-models.sh to install", root)
	return ""
}

// loadTestWAV reads a 16-bit mono PCM WAV from the package's
// testdata directory and returns it as a float32 in [-1, 1] slice
// at the original sample rate.
func loadTestWAV(t *testing.T, name string) ([]float32, int) {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("testdata/%s not present (regenerate via scripts/gen-test-audio.sh): %v", name, err)
	}
	defer f.Close()
	// Use the audio package's WAV reader via the in-memory sink
	// trick: write nothing, parse the header.
	//
	// Simpler: do a minimal WAV parse here.
	var hdr [44]byte
	if _, err := f.Read(hdr[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		t.Fatalf("%s: not a WAV file", name)
	}
	sampleRate := int(hdr[24]) | int(hdr[25])<<8 | int(hdr[26])<<16 | int(hdr[27])<<24
	if sampleRate == 0 {
		t.Fatalf("%s: zero sample rate", name)
	}
	// Read the rest of the file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read full file: %v", err)
	}
	pcmBytes := data[44:]
	out := make([]float32, len(pcmBytes)/2)
	for i := range out {
		lo := uint16(pcmBytes[2*i])
		hi := uint16(pcmBytes[2*i+1])
		u := lo | hi<<8
		// Sign-extend int16.
		s := int16(u)
		out[i] = float32(s) / 32768.0
	}
	return out, sampleRate
}

// TestParakeet_Hello: a recorded "hello world" clip is transcribed
// to (approximately) "hello world".
func TestParakeet_Hello(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	pcm, sr := loadTestWAV(t, "hello_8k.wav")
	got, err := p.Transcribe(context.Background(), pcm, sr)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(got)), "hello") {
		t.Errorf("Transcribe: want 'hello' in %q", got)
	}
}

// TestParakeet_Digits: a recorded digits clip contains all five
// expected digits.
func TestParakeet_Digits(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	pcm, sr := loadTestWAV(t, "digits_8k.wav")
	got, err := p.Transcribe(context.Background(), pcm, sr)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	lower := strings.ToLower(got)
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(lower, want) {
			t.Errorf("Transcribe: want %q in %q", want, got)
		}
	}
}

// TestParakeet_Latency: 5 s of audio transcribes in < 5 s.
func TestParakeet_Latency(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	// 5 s of 1 kHz sine at 16 kHz, the model's native rate.
	const sampleRate = 16000
	const dur = 5
	pcm := make([]float32, sampleRate*dur)
	for i := range pcm {
		pcm[i] = float32(0.5 * (1.0 + 0.0)) // silence
	}
	start := time.Now()
	_, err = p.Transcribe(context.Background(), pcm, sampleRate)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Transcribe 5s audio took %v; want < 5s", elapsed)
	}
}

// TestParakeet_Concurrent: race-detector clean with 2 goroutines.
func TestParakeet_Concurrent(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	const sampleRate = 16000
	pcm := make([]float32, sampleRate) // 1 s silence

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := p.Transcribe(context.Background(), pcm, sampleRate)
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent Transcribe: %v", err)
		}
	}
}

// TestParakeet_Resample_8kTo16k: 8 kHz input is internally
// resampled to 16 kHz before inference. The test runs a 1 s
// sine at 8 kHz and confirms the call returns successfully.
func TestParakeet_Resample_8kTo16k(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	const inRate = 8000
	const dur = 1
	pcm := make([]float32, inRate*dur)
	for i := range pcm {
		pcm[i] = float32(0.5)
	}
	_, err = p.Transcribe(context.Background(), pcm, inRate)
	if err != nil {
		t.Errorf("Transcribe(8k): %v", err)
	}
}

// TestParakeet_Empty: empty PCM returns "", nil.
func TestParakeet_Empty(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()
	got, err := p.Transcribe(context.Background(), nil, 16000)
	if err != nil {
		t.Fatalf("Transcribe(nil): %v", err)
	}
	if got != "" {
		t.Errorf("Transcribe(nil): want empty string, got %q", got)
	}
}

// TestParakeet_Close: Close then Transcribe returns error.
func TestParakeet_Close(t *testing.T) {
	dir := parakeetModelDir(t)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		t.Fatalf("NewParakeet: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent.
	if err := p.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
	if _, err := p.Transcribe(context.Background(), []float32{0.1}, 16000); err == nil {
		t.Error("Transcribe after Close: want error, got nil")
	}
}

// TestParakeet_BadConfig: a missing model directory returns an
// error at NewParakeet time.
func TestParakeet_BadConfig(t *testing.T) {
	_, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: "/nonexistent"})
	if err == nil {
		t.Error("NewParakeet with bad model dir: want error, got nil")
	}
}

// Helper so the test file uses codec if a future test wants to
// generate audio on the fly.
var _ = codec.Resample
