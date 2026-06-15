// Package stt defines the speech-to-text interface and a mock
// implementation. The real Parakeet-TDT backend (sherpa-onnx cgo)
// lives in parakeet.go and is gated by the `parakeet` build tag.
//
// See plan.md §6.1 / §6.2.
package stt

import "context"

// Transcriber is the abstract speech-to-text engine. Implementations
// take raw PCM mono audio (float32 in [-1, 1]) at the given sample
// rate and return the recognised text. The sample rate is passed
// because the M5 captures at 8 kHz, Parakeet is trained on 16 kHz,
// and we resample internally as needed.
type Transcriber interface {
	// Transcribe returns the recognised text for the given PCM
	// buffer. Implementations may block; pass a ctx to cancel.
	Transcribe(ctx context.Context, pcm []float32, sampleRate int) (text string, err error)

	// Close releases any resources held by the engine. Idempotent.
	Close() error
}
