// parakeet_http.go — HTTP client for the OpenAI-compatible Parakeet
// STT service (lab/ct/voice, :5093).
//
// Instead of running sherpa-onnx in-process via cgo, this client
// POSTs the audio to a network service that exposes the OpenAI
// transcription API:
//
//	POST /v1/audio/transcriptions
//	  multipart/form-data: file=<wav>, model=<name>, response_format=json
//	→ {"text": "recognised text"}
//
// This is the preferred production path: the lab voice container
// has the model loaded, GPU acceleration, and micro-batching — all
// of which are better than running the model in the tetherd process.
// No cgo, no build tags, no model files on the base station.

package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
)

// ParakeetHTTPConfig configures the HTTP STT client.
type ParakeetHTTPConfig struct {
	// URL is the base URL of the Parakeet STT service,
	// e.g. "http://10.10.199.51:5093". Required.
	URL string

	// Model is the model name to pass to the service. Empty
	// means "use the service's default".
	Model string

	// Timeout is the per-request timeout. Defaults to 30 s.
	Timeout time.Duration

	// HTTPClient is the http.Client to use. If nil, a default
	// client with the configured timeout is created.
	HTTPClient *http.Client
}

// ParakeetHTTP is an HTTP-based Transcriber that talks to the
// OpenAI-compatible Parakeet STT service. It implements the
// Transcriber interface.
type ParakeetHTTP struct {
	cfg    ParakeetHTTPConfig
	client *http.Client
}

// NewParakeetHTTP creates an HTTP STT client. The URL must be
// non-empty.
func NewParakeetHTTP(cfg ParakeetHTTPConfig) (*ParakeetHTTP, error) {
	if cfg.URL == "" {
		return nil, errors.New("stt: parakeet-http: empty URL")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &ParakeetHTTP{cfg: cfg, client: client}, nil
}

// ParakeetHTTPSampleRate is the rate the service expects. The
// Parakeet model is trained on 16 kHz audio. The service accepts
// any sample rate (it resamples internally via ffmpeg), but
// sending 16 kHz WAV skips the ffmpeg path for lower latency.
const ParakeetHTTPSampleRate = 16000

// Transcribe encodes pcm as a 16 kHz mono WAV, POSTs it to the
// service, and returns the recognised text. If the pcm is not
// at 16 kHz, it is resampled first.
func (p *ParakeetHTTP) Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error) {
	if len(pcm) == 0 {
		return "", nil
	}
	// Resample to 16 kHz if necessary.
	var mono []float32
	if sampleRate == ParakeetHTTPSampleRate {
		mono = pcm
	} else if sampleRate > 0 {
		mono = codec.Resample(pcm, sampleRate, ParakeetHTTPSampleRate)
	} else {
		return "", fmt.Errorf("stt: parakeet-http: invalid sample rate %d", sampleRate)
	}

	// Encode as WAV (16-bit PCM, 16 kHz, mono).
	wav, err := encodeWAV(mono, ParakeetHTTPSampleRate)
	if err != nil {
		return "", fmt.Errorf("stt: parakeet-http: encode wav: %w", err)
	}

	// Build multipart form.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	filePart, err := w.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("stt: parakeet-http: create form file: %w", err)
	}
	if _, err := filePart.Write(wav); err != nil {
		return "", fmt.Errorf("stt: parakeet-http: write wav: %w", err)
	}
	if p.cfg.Model != "" {
		if err := w.WriteField("model", p.cfg.Model); err != nil {
			return "", fmt.Errorf("stt: parakeet-http: write model: %w", err)
		}
	}
	if err := w.WriteField("response_format", "json"); err != nil {
		return "", fmt.Errorf("stt: parakeet-http: write format: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("stt: parakeet-http: close multipart: %w", err)
	}

	// POST.
	url := strings.TrimRight(p.cfg.URL, "/") + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", fmt.Errorf("stt: parakeet-http: new request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("stt: parakeet-http: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stt: parakeet-http: service returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse JSON response.
	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("stt: parakeet-http: decode response: %w", err)
	}
	return strings.TrimSpace(result.Text), nil
}

// Close is a no-op (the HTTP client has no persistent state).
func (p *ParakeetHTTP) Close() error { return nil }

// Compile-time check.
var _ Transcriber = (*ParakeetHTTP)(nil)

// encodeWAV encodes mono float32 PCM as a 16-bit PCM WAV file
// at the given sample rate.
func encodeWAV(pcm []float32, sampleRate int) ([]byte, error) {
	// Convert float32 to int16.
	int16pcm := make([]int16, len(pcm))
	for i, s := range pcm {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		int16pcm[i] = int16(s * 32767)
	}

	// WAV header (44 bytes) + PCM data.
	dataLen := len(int16pcm) * 2
	var buf bytes.Buffer
	// RIFF header.
	buf.WriteString("RIFF")
	writeUint32(&buf, uint32(36+dataLen))
	buf.WriteString("WAVE")
	// fmt chunk.
	buf.WriteString("fmt ")
	writeUint32(&buf, 16) // chunk size
	writeUint16(&buf, 1)  // PCM format
	writeUint16(&buf, 1)  // mono
	writeUint32(&buf, uint32(sampleRate))
	writeUint32(&buf, uint32(sampleRate)*2) // byte rate
	writeUint16(&buf, 2)                    // block align
	writeUint16(&buf, 16)                   // bits per sample
	// data chunk.
	buf.WriteString("data")
	writeUint32(&buf, uint32(dataLen))
	for _, s := range int16pcm {
		writeUint16(&buf, uint16(s))
	}
	return buf.Bytes(), nil
}

func writeUint32(w *bytes.Buffer, v uint32) {
	w.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

func writeUint16(w *bytes.Buffer, v uint16) {
	w.Write([]byte{byte(v), byte(v >> 8)})
}
