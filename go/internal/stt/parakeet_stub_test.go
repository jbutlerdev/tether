// Tests for the Parakeet stub. These run on the default (no
// `parakeet` tag) build. They exercise the public API: the stub
// returns ErrUnsupported for NewParakeet/Transcribe and the
// resampler is exercised on the way to the error.
//
// With `-tags parakeet`, this file is still compiled (no build
// tag here) but the stub file parakeet_stub.go is NOT compiled;
// the cgo file parakeet_cgo.go is. The same API surface is
// exercised by the parakeet_test.go file (which IS gated by
// the tag).
package stt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/stt"
)

// TestParakeet_Stub_NewParakeet: NewParakeet returns
// ErrUnsupported (or, in the real build, a real recognizer) on
// an empty model dir; never panics.
func TestParakeet_Stub_NewParakeet(t *testing.T) {
	_, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: ""})
	if err == nil {
		t.Error("NewParakeet(\"\"): want error, got nil")
	}
	if !errors.Is(err, stt.ErrUnsupported) && !strings.Contains(err.Error(), "ModelDir") {
		t.Logf("NewParakeet err: %v (acceptable)", err)
	}
}

// TestParakeet_Stub_Transcribe: Transcribe exercises the
// resample path then surfaces the stub's ErrUnsupported.
func TestParakeet_Stub_Transcribe(t *testing.T) {
	// We can't construct a *Parakeet via NewParakeet on the
	// stub, so reach for an internal one via the test helper.
	p := stt.NewStubParakeet(t)
	pcm := make([]float32, 8000) // 1 s at 8 kHz
	for i := range pcm {
		pcm[i] = 0.5
	}
	_, err := p.Transcribe(context.Background(), pcm, 8000)
	if !errors.Is(err, stt.ErrUnsupported) {
		// The real build may not return ErrUnsupported; just
		// require *some* error if the model isn't there.
		if err == nil {
			t.Error("Transcribe: want error, got nil")
		}
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestParakeet_Stub_TranscribeEmpty: an empty PCM buffer
// short-circuits to ("", nil) before the impl is called.
func TestParakeet_Stub_TranscribeEmpty(t *testing.T) {
	p := stt.NewStubParakeet(t)
	got, err := p.Transcribe(context.Background(), nil, 16000)
	if err != nil {
		t.Fatalf("Transcribe(nil): %v", err)
	}
	if got != "" {
		t.Errorf("Transcribe(nil): want empty string, got %q", got)
	}
}

// TestParakeet_Stub_TranscribeInvalidRate: a non-positive sample
// rate returns an error before the impl.
func TestParakeet_Stub_TranscribeInvalidRate(t *testing.T) {
	p := stt.NewStubParakeet(t)
	_, err := p.Transcribe(context.Background(), []float32{0.1}, 0)
	if err == nil {
		t.Error("Transcribe(0Hz): want error, got nil")
	}
}

// TestParakeet_Stub_TranscribeClosed: Transcribe on a closed
// Parakeet returns an error.
func TestParakeet_Stub_TranscribeClosed(t *testing.T) {
	p := stt.NewStubParakeet(t)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := p.Transcribe(context.Background(), []float32{0.1}, 16000)
	if err == nil {
		t.Error("Transcribe on closed: want error, got nil")
	}
}

// TestParakeet_Stub_CloseIdempotent: Close twice is allowed.
func TestParakeet_Stub_CloseIdempotent(t *testing.T) {
	p := stt.NewStubParakeet(t)
	if err := p.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close #2: %v", err)
	}
}
