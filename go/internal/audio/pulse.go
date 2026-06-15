// PulseAudio sink stub. See plan.md §6.8.
//
// This file is the PulseAudio implementation, gated by the
// `pulseaudio` build tag. The base station on Linux uses a real
// PulseAudio null sink for TTS playback. Without the build tag
// (or without the cgo dependency available) the file compiles to
// an empty stub that always returns an "unsupported" error so
// tests that don't use PulseAudio still pass.
//
// To enable: `go build -tags pulseaudio`. Requires libpulse-
// simple-dev installed and CGO_ENABLED=1.
package audio

import "errors"

// NewPulseSink is a stub that returns ErrUnsupported when the
// `pulseaudio` build tag is not set. The real implementation
// (pulse_cgo.go under the same tag) overrides this.
//
// On Linux, the production daemon uses NewPulseSink to open a
// connection to a PulseAudio null sink named "tether-tts". The
// real impl writes mono 16-bit LE PCM at the given sample rate
// and blocks on overflow.
func NewPulseSink(_ string, _ int) (Sink, error) {
	return nil, errors.New("audio: pulseaudio sink requires -tags pulseaudio")
}
