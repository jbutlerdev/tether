// Package tts defines the text-to-speech interface and a mock
// implementation. The real Piper backend (subprocess pipe) lives
// in piper.go and is gated by the `piper` build tag.
//
// See plan.md §6.4 / §6.5.
package tts

import "context"

// Synthesizer is the abstract TTS engine. Implementations take
// English text and return raw mono PCM (float32 in [-1, 1]) at
// the engine's native sample rate.
type Synthesizer interface {
	// Synthesize returns the spoken form of text as a mono PCM
	// buffer at the engine's sample rate. Implementations may
	// block; pass a ctx to cancel.
	Synthesize(ctx context.Context, text string) (pcm []float32, sampleRate int, err error)

	// SynthesizeStream consumes sentences from `in` and emits
	// per-sentence PCM chunks on `out`. The call blocks until
	// `in` is closed (and all queued sentences have been
	// processed) or ctx is canceled. The caller's `out` channel
	// must be buffered enough to avoid deadlock; the mock uses
	// a blocking send.
	//
	// On normal completion returns nil. On ctx cancel, returns
	// ctx.Err().
	SynthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error

	// SampleRate returns the engine's native sample rate in Hz.
	// This is fixed for the lifetime of the engine.
	SampleRate() int

	// Close releases any resources held by the engine. Idempotent.
	Close() error
}
