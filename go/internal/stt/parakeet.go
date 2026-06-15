// Parakeet-TDT 0.6B v2 STT engine. See plan.md §6.2.
//
// This file provides the public type `Parakeet` and its config.
// The actual sherpa-onnx cgo integration lives in parakeet_cgo.go
// under the `parakeet` build tag. Without the tag, this file
// provides a stub that returns an "unsupported" error so the
// production build does not need libonnxruntime or libsherpa-onnx.
//
// Run with the real backend:
//
//	cd go && go test -tags parakeet ./internal/stt/
package stt

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jbutlerdev/tether/go/internal/codec"
)

// ParakeetConfig configures the Parakeet-TDT engine.
type ParakeetConfig struct {
	// ModelDir is the directory containing the sherpa-onnx model
	// files (encoder.int8.onnx, decoder.int8.onnx,
	// joiner.int8.onnx, tokens.txt).
	ModelDir string

	// NumThreads is the number of CPU threads the ONNX runtime
	// will use. 0 means "let the runtime decide" (1 in our stub).
	NumThreads int
}

// Parakeet is the public type. The actual cgo fields live in
// parakeet_cgo.go under the parakeet build tag; without that
// tag the handle stays nil. The mutex serialises Transcribe
// calls — the underlying ONNX session is not safe for concurrent
// use.
type Parakeet struct {
	cfg ParakeetConfig

	// mu serialises Transcribe. The sherpa-onnx API has no
	// documented concurrent-invocation guarantee.
	mu sync.Mutex

	// closed is read on every Transcribe; atomic for the fast
	// path.
	closed bool

	// handle holds the cgo recognizer (or nil for the stub).
	// Typed as `any` so the cgo struct does not leak into the
	// stub build.
	handle any
}

// NewParakeet loads the model from cfg.ModelDir. When built
// without the `parakeet` tag, it always returns an
// ErrUnsupported error. With the tag, it initialises a real
// sherpa-onnx OfflineRecognizer.
func NewParakeet(cfg ParakeetConfig) (*Parakeet, error) {
	if cfg.ModelDir == "" {
		return nil, errors.New("stt: parakeet: empty ModelDir")
	}
	return newParakeetImpl(cfg)
}

// ParakeetSampleRate is the rate the model expects. Parakeet-TDT
// 0.6B v2 is trained on 16 kHz audio.
const ParakeetSampleRate = 16000

// Transcribe resamples pcm to 16 kHz (if needed) and runs the
// model. The result is the recognised text in lowercase, trimmed.
//
// The Parakeet cgo implementation is in parakeet_cgo.go. Without
// the `parakeet` tag, Transcribe returns ErrUnsupported.
func (p *Parakeet) Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error) {
	if p.closed {
		return "", errors.New("stt: parakeet: transcribe on closed")
	}
	if len(pcm) == 0 {
		return "", nil
	}
	// Resample to 16 kHz if necessary. The resampler is a
	// pure-Go polyphase filter from internal/codec.
	var mono []float32
	if sampleRate == ParakeetSampleRate {
		mono = pcm
	} else if sampleRate > 0 {
		mono = codec.Resample(pcm, sampleRate, ParakeetSampleRate)
	} else {
		return "", fmt.Errorf("stt: parakeet: invalid sample rate %d", sampleRate)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return transcribeImpl(ctx, p, mono)
}

// Close releases the model. Idempotent.
func (p *Parakeet) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return closeImpl(p)
}

// ErrUnsupported is returned by Parakeet.Transcribe when the
// `parakeet` build tag is not set. Production code that uses the
// real Parakeet must build with `-tags parakeet` and have
// sherpa-onnx and onnxruntime available.
var ErrUnsupported = errors.New("stt: parakeet: not built with -tags parakeet; install sherpa-onnx and rebuild")
