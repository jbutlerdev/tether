// Mock implementation of the Synthesizer interface. See plan.md §6.4.
//
// The mock is deterministic: identical text produces identical
// bytes (a small hashed-and-shaped buffer). It supports optional
// artificial latency and an optional injected error for tests
// that need to exercise error paths.
package tts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// defaultSampleRate is the TTS engine's native rate. Piper also
// runs at 22050 Hz by default, so the mock matches.
const defaultSampleRate = 22050

// samplesPerChar is how many PCM samples the mock emits per input
// character. Roughly mimics Piper's ~10 ms per character at 22050
// Hz (≈ 220 samples/char), but rounded for stability.
const samplesPerChar = 220

// Mock is a deterministic in-process stand-in for a real TTS
// engine. The output for a given text is a short repeating
// pattern derived from the SHA-256 of the text, scaled to look
// like speech (sine-modulated noise with the hash as the seed).
type Mock struct {
	mu sync.Mutex

	// closed is read by every Synthesize; atomic for fast path.
	closed atomic.Bool

	// latency delays Synthesize by this much (or until ctx is done).
	latency atomic.Int64 // nanoseconds
	// err is the error to return from Synthesize. nil = no error.
	err atomic.Pointer[error]
}

// MockOption configures a Mock at construction time.
type MockOption func(*Mock)

// MockOptionLatency sets an artificial delay applied to every
// Synthesize call. Honoured only when > 0.
func MockOptionLatency(d time.Duration) MockOption {
	return func(m *Mock) { m.latency.Store(int64(d)) }
}

// MockOptionError makes every Synthesize call return the given
// error. Pass nil to clear.
func MockOptionError(err error) MockOption {
	return func(m *Mock) {
		var p *error
		if err != nil {
			e := err
			p = &e
		}
		m.err.Store(p)
	}
}

// NewMock returns a fresh Mock.
func NewMock(opts ...MockOption) *Mock {
	m := &Mock{}
	m.err.Store((*error)(nil))
	for _, o := range opts {
		o(m)
	}
	return m
}

// SampleRate returns 22050 Hz (Piper's default).
func (m *Mock) SampleRate() int { return defaultSampleRate }

// Synthesize returns a deterministic PCM buffer whose length is
// proportional to the input text length.
func (m *Mock) Synthesize(ctx context.Context, text string) ([]float32, int, error) {
	if m.closed.Load() {
		return nil, 0, errors.New("tts: synthesize on closed mock")
	}
	if d := time.Duration(m.latency.Load()); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if p := m.err.Load(); p != nil && *p != nil {
		return nil, 0, *p
	}
	// Length: at least one frame, and one samplesPerChar per char
	// of text so longer text → longer audio.
	n := len(text) * samplesPerChar
	if n == 0 {
		n = samplesPerChar
	}
	return synthBuffer(text, n), defaultSampleRate, nil
}

// SynthesizeStream consumes sentences from `in` and emits one
// PCM chunk per sentence on `out`. It blocks until `in` is closed
// and all queued sentences have been processed, or until ctx is
// canceled.
func (m *Mock) SynthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case text, ok := <-in:
			if !ok {
				return nil
			}
			pcm, _, err := m.Synthesize(ctx, text)
			if err != nil {
				return err
			}
			// Blocking send so the consumer can pace us.
			select {
			case out <- pcm:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Close releases resources. Idempotent.
func (m *Mock) Close() error {
	m.closed.Store(true)
	return nil
}

// synthBuffer produces a deterministic pseudo-speech PCM buffer
// for the given text and length. The signal is a sum of a few
// sine partials whose frequencies are derived from the SHA-256
// of the text, scaled to stay in [-1, 1]. The result is
// recognisably *not* silence (so STT/transmission tests can tell
// the difference between "got audio" and "got silence") but is
// obviously synthetic.
func synthBuffer(text string, n int) []float32 {
	out := make([]float32, n)
	sum := sha256.Sum256([]byte(text))
	// Use 4 bytes from the digest to seed 4 partial frequencies
	// in [80, 400] Hz (voice formant range).
	partials := [4]float64{
		80 + float64(sum[0])/255*320,
		80 + float64(sum[1])/255*320,
		80 + float64(sum[2])/255*320,
		80 + float64(sum[3])/255*320,
	}
	// Use 4 more bytes for amplitude scaling.
	amps := [4]float64{
		0.25 + float64(sum[4])/255*0.25,
		0.25 + float64(sum[5])/255*0.25,
		0.25 + float64(sum[6])/255*0.25,
		0.25 + float64(sum[7])/255*0.25,
	}
	var maxAmp float64
	for i := range out {
		t := float64(i) / float64(defaultSampleRate)
		var s float64
		for k := 0; k < 4; k++ {
			s += amps[k] * math.Sin(2*math.Pi*partials[k]*t)
		}
		out[i] = float32(s)
		if a := math.Abs(s); a > maxAmp {
			maxAmp = a
		}
	}
	// Normalise to 0.8 peak so the signal is recognisable but
	// doesn't clip.
	if maxAmp > 0 {
		scale := 0.8 / maxAmp
		for i := range out {
			out[i] = float32(float64(out[i]) * scale)
		}
	}
	// Silence the unused sha256 input in the linter.
	_ = binary.LittleEndian
	return out
}
