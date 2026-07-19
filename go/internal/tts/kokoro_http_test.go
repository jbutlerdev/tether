package tts

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestKokoroHTTP_Synthesize verifies the HTTP TTS client correctly
// sends a JSON request and decodes the PCM response.
func TestKokoroHTTP_Synthesize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path: got %q, want /v1/audio/speech", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type: got %q", r.Header.Get("Content-Type"))
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req["model"] != "kokoro" {
			t.Errorf("model: got %v", req["model"])
		}
		if req["input"] != "hello" {
			t.Errorf("input: got %v", req["input"])
		}
		if req["voice"] != "af_heart" {
			t.Errorf("voice: got %v", req["voice"])
		}
		if req["response_format"] != "pcm" {
			t.Errorf("response_format: got %v", req["response_format"])
		}
		// Return 100 samples of int16 LE PCM.
		pcm := make([]byte, 200)
		for i := 0; i < 100; i++ {
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(i*100))
		}
		w.Write(pcm)
	}))
	defer srv.Close()

	client, err := NewKokoroHTTP(KokoroHTTPConfig{
		URL:   srv.URL,
		Voice: "af_heart",
	})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}

	pcm, sr, err := client.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if sr != KokoroSampleRate {
		t.Errorf("sample rate: got %d, want %d", sr, KokoroSampleRate)
	}
	if len(pcm) != 100 {
		t.Fatalf("pcm length: got %d, want 100", len(pcm))
	}
	// Verify the first sample.
	if pcm[0] != 0 {
		t.Errorf("pcm[0]: got %f, want 0", pcm[0])
	}
	// Verify the second sample.
	expected := float32(100) / 32768.0
	if pcm[1] < expected-0.001 || pcm[1] > expected+0.001 {
		t.Errorf("pcm[1]: got %f, want ~%f", pcm[1], expected)
	}
}

// TestKokoroHTTP_EmptyText verifies that empty text returns empty PCM.
func TestKokoroHTTP_EmptyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not have made an HTTP request for empty text")
	}))
	defer srv.Close()

	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}

	pcm, _, err := client.Synthesize(context.Background(), "")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if pcm != nil {
		t.Errorf("pcm: got %d samples, want nil", len(pcm))
	}
}

// TestKokoroHTTP_ErrorStatus verifies that a non-200 response returns an error.
func TestKokoroHTTP_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}

	_, _, err = client.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

// TestKokoroHTTP_EmptyURL verifies that an empty URL returns an error.
func TestKokoroHTTP_EmptyURL(t *testing.T) {
	_, err := NewKokoroHTTP(KokoroHTTPConfig{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

// TestKokoroHTTP_DefaultVoice verifies the default voice is af_heart.
func TestKokoroHTTP_DefaultVoice(t *testing.T) {
	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: "http://localhost:9999"})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}
	if client.cfg.Voice != "af_heart" {
		t.Errorf("default voice: got %q, want af_heart", client.cfg.Voice)
	}
}

// TestKokoroHTTP_SampleRate verifies the sample rate is 24000.
func TestKokoroHTTP_SampleRate(t *testing.T) {
	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: "http://localhost:9999"})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}
	if client.SampleRate() != 24000 {
		t.Errorf("SampleRate: got %d, want 24000", client.SampleRate())
	}
}

// TestKokoroHTTP_Close verifies that Close is a no-op.
func TestKokoroHTTP_Close(t *testing.T) {
	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: "http://localhost:9999"})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestKokoroHTTP_SynthesizeStream verifies the streaming interface.
func TestKokoroHTTP_SynthesizeStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 50 samples of silence.
		w.Write(make([]byte, 100))
	}))
	defer srv.Close()

	client, err := NewKokoroHTTP(KokoroHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewKokoroHTTP: %v", err)
	}

	in := make(chan string, 2)
	out := make(chan []float32, 2)

	in <- "first"
	in <- "second"
	close(in)

	ctx := context.Background()
	if err := client.SynthesizeStream(ctx, in, out); err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}

	// Should receive 2 chunks.
	count := 0
	for {
		select {
		case pcm := <-out:
			if len(pcm) != 50 {
				t.Errorf("chunk %d: got %d samples, want 50", count, len(pcm))
			}
			count++
		default:
			if count != 2 {
				t.Errorf("received %d chunks, want 2", count)
			}
			return
		}
	}
}

// Ensure io is imported.
var _ = io.EOF

// Compile-time check.
var _ Synthesizer = (*KokoroHTTP)(nil)
