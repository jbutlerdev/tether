// kokoro_http.go — HTTP client for the OpenAI-compatible Kokoro
// TTS service (lab/ct/voice, :8766).
//
// Instead of running Piper as a subprocess, this client POSTs text
// to a network service that exposes the OpenAI TTS API:
//
//	POST /v1/audio/speech
//	  {"model":"kokoro", "input":"text", "voice":"af_heart",
//	   "response_format":"pcm", "speed":1.0}
//	→ raw 16-bit LE PCM bytes (24 kHz mono)
//
// This is the preferred production path: the lab voice container
// has the model loaded with GPU acceleration. No subprocess, no
// binary path, no model files on the base station.

package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// KokoroHTTPConfig configures the HTTP TTS client.
type KokoroHTTPConfig struct {
	// URL is the base URL of the Kokoro TTS service,
	// e.g. "http://10.10.199.51:8766". Required.
	URL string

	// Voice is the voice ID. Defaults to "af_heart".
	Voice string

	// Speed is the playback speed multiplier. 1.0 = normal.
	Speed float64

	// Timeout is the per-request timeout. Defaults to 30 s.
	Timeout time.Duration

	// HTTPClient is the http.Client to use. If nil, a default
	// client with the configured timeout is created.
	HTTPClient *http.Client
}

// KokoroHTTP is an HTTP-based Synthesizer that talks to the
// OpenAI-compatible Kokoro TTS service. It implements the
// Synthesizer interface.
type KokoroHTTP struct {
	cfg    KokoroHTTPConfig
	client *http.Client
}

// KokoroSampleRate is the native sample rate of the Kokoro model
// (24 kHz).
const KokoroSampleRate = 24000

// NewKokoroHTTP creates an HTTP TTS client. The URL must be
// non-empty.
func NewKokoroHTTP(cfg KokoroHTTPConfig) (*KokoroHTTP, error) {
	if cfg.URL == "" {
		return nil, errors.New("tts: kokoro-http: empty URL")
	}
	if cfg.Voice == "" {
		cfg.Voice = "af_heart"
	}
	if cfg.Speed == 0 {
		cfg.Speed = 1.0
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &KokoroHTTP{cfg: cfg, client: client}, nil
}

// SampleRate returns KokoroSampleRate (24000 Hz).
func (k *KokoroHTTP) SampleRate() int { return KokoroSampleRate }

// Synthesize POSTs text to the service and returns the PCM as
// mono float32 at 24 kHz. The response_format is "pcm" (raw 16-bit
// LE), which avoids WAV header parsing and ffmpeg encoding.
func (k *KokoroHTTP) Synthesize(ctx context.Context, text string) ([]float32, int, error) {
	if text == "" {
		return nil, KokoroSampleRate, nil
	}

	// Build JSON request.
	reqBody, err := json.Marshal(map[string]any{
		"model":           "kokoro",
		"input":           text,
		"voice":           k.cfg.Voice,
		"response_format": "pcm",
		"speed":           k.cfg.Speed,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("tts: kokoro-http: marshal request: %w", err)
	}

	// POST.
	url := strings.TrimRight(k.cfg.URL, "/") + "/v1/audio/speech"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, fmt.Errorf("tts: kokoro-http: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("tts: kokoro-http: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("tts: kokoro-http: service returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Read raw 16-bit LE PCM.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("tts: kokoro-http: read response: %w", err)
	}
	if len(raw)%2 != 0 {
		return nil, 0, errors.New("tts: kokoro-http: odd byte count in PCM response")
	}

	// Convert int16 LE → float32.
	pcm := make([]float32, len(raw)/2)
	for i := range pcm {
		s := int16(binary.LittleEndian.Uint16(raw[2*i:]))
		pcm[i] = float32(s) / 32768.0
	}
	return pcm, KokoroSampleRate, nil
}

// SynthesizeStream consumes sentences from `in` and emits per-
// sentence PCM chunks on `out`. Each sentence is a separate HTTP
// request to the TTS service.
func (k *KokoroHTTP) SynthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case text, ok := <-in:
			if !ok {
				return nil
			}
			pcm, _, err := k.Synthesize(ctx, text)
			if err != nil {
				return err
			}
			select {
			case out <- pcm:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Close is a no-op (the HTTP client has no persistent state).
func (k *KokoroHTTP) Close() error { return nil }

// Compile-time check.
var _ Synthesizer = (*KokoroHTTP)(nil)
