// Piper subprocess wrapper. See plan.md §6.5.
//
// The production binary (the real Piper TTS) is a separate
// process that reads text from stdin and writes raw 16-bit LE
// PCM to stdout. The wrapper spawns it, pipes Synthesize calls
// through, and decodes the byte stream back into a []float32.
//
// The wire protocol (defined by Piper) is:
//
//   - On startup, Piper writes "PIPER_READY <rate> <channels> <bits>\n"
//     on stderr.
//   - Caller sends "SYNTH <text>\n" on stdin.
//   - Piper writes "<byte_count>\n<byte_count bytes of PCM>" on
//     stdout, then "END\n" on stderr. The byte count is the size
//     of the raw int16 LE PCM, written as a decimal string.
//   - Caller sends "QUIT\n" on stdin to terminate the binary.
//
// The wrapper holds the subprocess open across multiple
// Synthesize calls; SynthesizeStream multiplexes per-sentence
// audio onto the output channel.
package tts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// PiperConfig configures the Piper subprocess.
type PiperConfig struct {
	// BinaryPath is the path to the piper binary. Required.
	BinaryPath string
	// VoicePath is the path to the .onnx voice model. Required.
	VoicePath string
	// UseGPU enables CUDA/CoreML execution when set. v1 is CPU-only.
	UseGPU bool
	// ExtraArgs is appended to the piper command line. Useful
	// for `--debug` or `--quiet` flags.
	ExtraArgs []string
	// StartupTimeout is how long to wait for the "PIPER_READY"
	// banner. Defaults to 5 s.
	StartupTimeout time.Duration
	// PerSynthTimeout caps the wall-clock time of a single
	// synthesis. Defaults to 30 s.
	PerSynthTimeout time.Duration
}

// Piper is the subprocess wrapper. Holds a single Piper process;
// all Synthesize calls are serialised through the mutex.
type Piper struct {
	cfg PiperConfig

	// impl is the live subprocess handle. nil on the stub
	// build (no `piper` tag).
	impl piperBackend
}

// piperBackend is the small interface the wrapper needs from the
// subprocess implementation. Both the real (piper_subprocess.go)
// and the stub (piper_stub.go) implement it.
type piperBackend interface {
	synthesize(ctx context.Context, text string) ([]float32, int, error)
	synthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error
	close() error
}

// NewPiper starts the piper subprocess and waits for the
// "PIPER_READY" banner. Returns an error if the binary is
// missing, the voice file is missing, or the binary fails to
// start.
//
// On a no-`piper`-tag build, NewPiper returns ErrUnsupported.
// The real implementation is in piper_subprocess.go.
func NewPiper(cfg PiperConfig) (*Piper, error) {
	if cfg.BinaryPath == "" {
		return nil, errors.New("tts: piper: empty BinaryPath")
	}
	if cfg.VoicePath == "" {
		return nil, errors.New("tts: piper: empty VoicePath")
	}
	if _, err := os.Stat(cfg.BinaryPath); err != nil {
		return nil, fmt.Errorf("tts: piper: binary %q: %w", cfg.BinaryPath, err)
	}
	if _, err := os.Stat(cfg.VoicePath); err != nil {
		return nil, fmt.Errorf("tts: piper: voice %q: %w", cfg.VoicePath, err)
	}
	impl, err := newPiperBackend(cfg)
	if err != nil {
		return nil, err
	}
	return &Piper{cfg: cfg, impl: impl}, nil
}

// SampleRate returns 22050 Hz, the canonical Piper rate.
func (p *Piper) SampleRate() int { return 22050 }

// Synthesize sends `text` to the piper subprocess and returns
// the produced PCM as mono float32 at 22050 Hz.
func (p *Piper) Synthesize(ctx context.Context, text string) ([]float32, int, error) {
	return p.impl.synthesize(ctx, text)
}

// SynthesizeStream consumes sentences from `in` and emits per-
// sentence PCM chunks on `out`. The piper subprocess is held
// open across sentences.
func (p *Piper) SynthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error {
	return p.impl.synthesizeStream(ctx, in, out)
}

// Close terminates the piper subprocess. Idempotent.
func (p *Piper) Close() error {
	return p.impl.close()
}

// ErrUnsupported is returned by Piper methods when the
// subprocess cannot be launched. (The wrapper itself is always
// compiled; the runtime dependency is the piper binary.)
var ErrUnsupported = errors.New("tts: piper: subprocess not available")
