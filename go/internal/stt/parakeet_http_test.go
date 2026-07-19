package stt

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParakeetHTTP_Transcribe verifies the HTTP STT client correctly
// sends a multipart WAV upload and parses the JSON response.
func TestParakeetHTTP_Transcribe(t *testing.T) {
	// Test server that mimics the OpenAI transcription endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path: got %q, want /v1/audio/transcriptions", r.URL.Path)
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no file in form: %v", err)
		}
		defer file.Close()
		wav, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read file: %v", err)
		}
		// Verify it's a WAV (RIFF header).
		if len(wav) < 12 || string(wav[:4]) != "RIFF" {
			t.Errorf("not a WAV: first 4 bytes = %q", string(wav[:4]))
		}
		// Verify the model field.
		if r.FormValue("model") != "parakeet-tdt-0.6b-v3" {
			t.Errorf("model: got %q", r.FormValue("model"))
		}
		// Return a JSON response.
		json.NewEncoder(w).Encode(map[string]string{"text": "hello world"})
	}))
	defer srv.Close()

	client, err := NewParakeetHTTP(ParakeetHTTPConfig{
		URL:   srv.URL,
		Model: "parakeet-tdt-0.6b-v3",
	})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}

	// 160 samples of silence at 8 kHz → will be resampled to 16 kHz.
	pcm := make([]float32, 160)
	text, err := client.Transcribe(context.Background(), pcm, 8000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text: got %q, want %q", text, "hello world")
	}
}

// TestParakeetHTTP_EmptyInput verifies that empty PCM returns empty
// text without making an HTTP request.
func TestParakeetHTTP_EmptyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not have made an HTTP request for empty input")
	}))
	defer srv.Close()

	client, err := NewParakeetHTTP(ParakeetHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}

	text, err := client.Transcribe(context.Background(), nil, 8000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "" {
		t.Errorf("text: got %q, want empty", text)
	}
}

// TestParakeetHTTP_ErrorStatus verifies that a non-200 response
// returns an error.
func TestParakeetHTTP_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	client, err := NewParakeetHTTP(ParakeetHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}

	pcm := make([]float32, 160)
	_, err = client.Transcribe(context.Background(), pcm, 16000)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// TestParakeetHTTP_EmptyURL verifies that an empty URL returns an error.
func TestParakeetHTTP_EmptyURL(t *testing.T) {
	_, err := NewParakeetHTTP(ParakeetHTTPConfig{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

// TestParakeetHTTP_Close verifies that Close is a no-op.
func TestParakeetHTTP_Close(t *testing.T) {
	client, err := NewParakeetHTTP(ParakeetHTTPConfig{URL: "http://localhost:9999"})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestParakeetHTTP_16kHzNoResample verifies that 16 kHz PCM is sent
// without resampling (the model's native rate).
func TestParakeetHTTP_16kHzNoResample(t *testing.T) {
	var gotSampleRate int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the WAV and check the sample rate in the header.
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		file, _, _ := r.FormFile("file")
		defer file.Close()
		wav, _ := io.ReadAll(file)
		if len(wav) >= 28 {
			// WAV sample rate is at offset 24, 4 bytes LE.
			gotSampleRate = int(wav[24]) | int(wav[25])<<8 | int(wav[26])<<16 | int(wav[27])<<24
		}
		json.NewEncoder(w).Encode(map[string]string{"text": "ok"})
	}))
	defer srv.Close()

	client, err := NewParakeetHTTP(ParakeetHTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewParakeetHTTP: %v", err)
	}

	pcm := make([]float32, 160)
	_, err = client.Transcribe(context.Background(), pcm, 16000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotSampleRate != 16000 {
		t.Errorf("WAV sample rate: got %d, want 16000", gotSampleRate)
	}
}

// Compile-time check.
var _ Transcriber = (*ParakeetHTTP)(nil)

// Ensure multipart is imported (used in the test server).
var _ = multipart.NewWriter
