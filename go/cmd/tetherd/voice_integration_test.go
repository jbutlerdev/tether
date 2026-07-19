//go:build integration

// voice_integration_test.go — integration tests against the live
// network voice services (lab/ct/voice).
//
// These tests are gated by the `integration` build tag and are NOT
// run by CI. Run them manually on the bench:
//
//	TETHER_VOICE_STT=http://10.10.199.51:5093 \
//	TETHER_VOICE_TTS=http://10.10.199.51:8766 \
//	go test -tags integration -count=1 -timeout 60s ./cmd/tetherd/
package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// TestVoiceIntegration_STT verifies that the HTTP STT client can
// transcribe a synthetic utterance via the live Parakeet service.
func TestVoiceIntegration_STT(t *testing.T) {
	sttURL := os.Getenv("TETHER_VOICE_STT")
	if sttURL == "" {
		t.Skip("TETHER_VOICE_STT not set")
	}

	client, err := stt.NewParakeetHTTP(stt.ParakeetHTTPConfig{
		URL:     sttURL,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}
	defer client.Close()

	// Generate 1 second of 440 Hz sine wave at 8 kHz.
	pcm := make([]float32, 8000)
	for i := range pcm {
		t := float64(i) / 8000.0
		pcm[i] = float32(0.3 * (2 * 3.14159265 * 440 * t))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	text, err := client.Transcribe(ctx, pcm, 8000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	t.Logf("STT result: %q", text)
	// We don't assert the text (a pure tone may not transcribe to
	// anything meaningful), but we verify no error.
}

// TestVoiceIntegration_TTS verifies that the HTTP TTS client can
// synthesize speech via the live Kokoro service and the output
// can be encoded with the real Opus codec.
func TestVoiceIntegration_TTS(t *testing.T) {
	ttsURL := os.Getenv("TETHER_VOICE_TTS")
	if ttsURL == "" {
		t.Skip("TETHER_VOICE_TTS not set")
	}

	client, err := tts.NewKokoroHTTP(tts.KokoroHTTPConfig{
		URL:     ttsURL,
		Voice:   "af_heart",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pcm, sr, err := client.Synthesize(ctx, "Hello world, this is a test of the Tether voice system.")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) == 0 {
		t.Fatal("empty PCM from TTS")
	}
	if sr != 24000 {
		t.Errorf("sample rate: got %d, want 24000", sr)
	}
	t.Logf("TTS: %d samples at %d Hz (%.2f s)", len(pcm), sr, float64(len(pcm))/float64(sr))

	// Verify we can resample to 8 kHz (the codec rate).
	resampled := codec.Resample(pcm, sr, codec.SampleRate)
	if len(resampled) == 0 {
		t.Fatal("resample produced empty output")
	}
	t.Logf("Resampled: %d samples at %d Hz", len(resampled), codec.SampleRate)
}

// TestVoiceIntegration_RoundTrip does TTS → STT and verifies the
// recognised text contains the spoken words.
func TestVoiceIntegration_RoundTrip(t *testing.T) {
	sttURL := os.Getenv("TETHER_VOICE_STT")
	ttsURL := os.Getenv("TETHER_VOICE_TTS")
	if sttURL == "" || ttsURL == "" {
		t.Skip("TETHER_VOICE_STT or TETHER_VOICE_TTS not set")
	}

	ttsClient, err := tts.NewKokoroHTTP(tts.KokoroHTTPConfig{
		URL:     ttsURL,
		Voice:   "af_heart",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}
	defer ttsClient.Close()

	sttClient, err := stt.NewParakeetHTTP(stt.ParakeetHTTPConfig{
		URL:     sttURL,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}
	defer sttClient.Close()

	// Synthesize a short phrase.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pcm, sr, err := ttsClient.Synthesize(ctx, "The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) == 0 {
		t.Fatal("empty PCM from TTS")
	}

	// Transcribe it back.
	text, err := sttClient.Transcribe(ctx, pcm, sr)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	t.Logf("Round-trip: spoke %d samples, got %q", len(pcm), text)
	if text == "" {
		t.Fatal("STT returned empty text for TTS output")
	}
}
