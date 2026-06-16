// Parakeet stub: returns ErrUnsupported when the real cgo
// backend (parakeet_cgo.go) is not compiled. This file is the
// *default* — the real backend is gated by the `parakeet` build
// tag and lives in parakeet_cgo.go.

//go:build !parakeet

package stt

import "context"

// newParakeetImpl returns an "unsupported" error. Tests gated by
// the `parakeet` tag use the cgo version; the production build
// (no tag) compiles to this stub so the binary does not need
// libonnxruntime or libsherpa-onnx.
//
// Callers that want a fully functional Parakeet must build with
// `-tags parakeet` and have the sherpa-onnx C library installed
// at /usr/local/lib (or wherever CGO_LDFLAGS points).
func newParakeetImpl(_ ParakeetConfig) (*Parakeet, error) {
	return nil, ErrUnsupported
}

// transcribeImpl returns ErrUnsupported. The cgo version runs
// the actual inference.
func transcribeImpl(_ context.Context, _ *Parakeet, _ []float32) (string, error) {
	return "", ErrUnsupported
}

// closeImpl is a no-op for the stub.
func closeImpl(_ *Parakeet) error { return nil }
