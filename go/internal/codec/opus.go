// Codec wrapper. See plan.md §2.8.
//
// In Phase 1 we ship a Mock (identity codec) used by unit tests
// and the Phase 1 loopback tool. The real Opus encoder (libopus
// via cgo) lands in Phase 5.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FrameSize is the number of samples per Opus frame at 8 kHz.
// 160 samples = 20 ms at 8 kHz, the canonical Opus frame duration.
const FrameSize = 160

// SampleRate is the canonical Tether sample rate (8 kHz).
const SampleRate = 8000

// Opus is the abstract codec interface.
type Opus interface {
	// Encode takes a PCM frame of exactly FrameSize int16 samples
	// (mono) and returns the encoded bytes. The length varies
	// depending on the codec (Opus VBR produces 10–60 bytes per
	// frame at 16 kbps).
	Encode(pcm []int16) (opus []byte, err error)

	// Decode is the inverse of Encode.
	Decode(opus []byte) (pcm []int16, err error)

	// FrameSize returns the expected input frame size in samples.
	FrameSize() int

	// SampleRate returns the codec's sample rate in Hz.
	SampleRate() int

	// Close releases any resources held by the codec. The Mock
	// is a no-op; the real Opus libopus wrapper frees state.
	Close() error
}

// Mock is an identity codec: Encode converts int16 → little-endian
// bytes; Decode is the inverse. The Mock is used by unit tests and
// the Phase 1 loopback tool.
type Mock struct{}

// NewMock returns a fresh Mock codec.
func NewMock() *Mock {
	return &Mock{}
}

// FrameSize returns FrameSize.
func (m *Mock) FrameSize() int { return FrameSize }

// SampleRate returns SampleRate.
func (m *Mock) SampleRate() int { return SampleRate }

// Encode converts a PCM frame to bytes. The input must be exactly
// FrameSize int16 samples; otherwise an error is returned.
func (m *Mock) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, nil
	}
	if len(pcm) != FrameSize {
		return nil, fmt.Errorf("codec: encode: got %d samples, want %d", len(pcm), FrameSize)
	}
	out := make([]byte, 2*len(pcm))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
	}
	return out, nil
}

// Decode is the inverse of Encode. The input byte count must be
// an even multiple of 2 (i.e., a whole number of int16 samples).
func (m *Mock) Decode(opus []byte) ([]int16, error) {
	if len(opus) == 0 {
		return nil, nil
	}
	if len(opus)%2 != 0 {
		return nil, errors.New("codec: decode: odd byte count")
	}
	out := make([]int16, len(opus)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(opus[2*i:]))
	}
	return out, nil
}

// Close is a no-op for the mock.
func (m *Mock) Close() error { return nil }

// Compile-time check.
var _ Opus = (*Mock)(nil)
