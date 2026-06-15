// Mock implementation of the Transcriber interface. See plan.md §6.1.
//
// The mock is deterministic: identical PCM bytes always produce the
// same text (a short hex digest of the buffer). It supports optional
// artificial latency and an optional injected error for tests that
// need to exercise the dispatcher's error path.
package stt

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Call is one recorded invocation of Mock.Transcribe.
type Call struct {
	PCM        []float32
	SampleRate int
	At         time.Time
}

// Mock is a deterministic in-process stand-in for a real STT engine.
type Mock struct {
	// mu protects the slice.
	mu    sync.Mutex
	calls []Call

	// closed is read by every Transcribe; atomic for fast path.
	closed atomic.Bool

	// latency delays Transcribe by this much (or until ctx is done).
	latency atomic.Int64 // nanoseconds
	// err is the error to return from Transcribe. nil = no error.
	err atomic.Pointer[error]
}

// MockOption configures a Mock at construction time.
type MockOption func(*Mock)

// MockOptionLatency sets an artificial delay applied to every
// Transcribe call. Honoured only when > 0.
func MockOptionLatency(d time.Duration) MockOption {
	return func(m *Mock) { m.latency.Store(int64(d)) }
}

// MockOptionError makes every Transcribe call return the given
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

// Transcribe records the call and returns a deterministic hex digest
// of the PCM buffer (or the configured error, if any). An optional
// artificial latency is applied.
func (m *Mock) Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error) {
	if m.closed.Load() {
		return "", errors.New("stt: transcribe on closed mock")
	}
	// Capture the buffer for the recorded call. We copy the input
	// because the caller may reuse the slice.
	pcmCopy := make([]float32, len(pcm))
	copy(pcmCopy, pcm)
	m.mu.Lock()
	m.calls = append(m.calls, Call{PCM: pcmCopy, SampleRate: sampleRate, At: time.Now()})
	m.mu.Unlock()

	// Optional latency.
	if d := time.Duration(m.latency.Load()); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if p := m.err.Load(); p != nil && *p != nil {
		return "", *p
	}

	// Deterministic digest.
	sum := sha256.Sum256(float32sToBytes(pcm))
	// Truncate to 16 hex chars for readability.
	return hex.EncodeToString(sum[:])[:16], nil
}

// Close releases resources. Idempotent. Subsequent Transcribe calls
// return an error.
func (m *Mock) Close() error {
	m.closed.Store(true)
	return nil
}

// Calls returns a snapshot of the recorded calls. The PCM slice in
// each Call is a copy and safe to retain.
func (m *Mock) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Call, len(m.calls))
	copy(out, m.calls)
	return out
}

// float32sToBytes serialises a []float32 to its little-endian byte
// representation. We hash bytes (not the float values) so the digest
// is byte-stable across runs and platforms.
func float32sToBytes(s []float32) []byte {
	out := make([]byte, 4*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint32(out[4*i:], mathFloat32bits(v))
	}
	return out
}
