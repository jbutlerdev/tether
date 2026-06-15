// Internal test helpers for the stt package. Tests in this file
// use `package stt` (not `stt_test`) so they can poke at the
// unexported Parakeet struct fields.
package stt

import "testing"

// NewStubParakeet returns a *Parakeet that bypasses the build-
// tagged cgo/stub dispatch. This lets the stub tests exercise
// the resample-and-return-error path even when the real cgo
// backend is not compiled.
//
// On the no-tag build, the result is a *Parakeet whose handle
// is nil; Transcribe will run the resample and then call
// transcribeImpl, which returns ErrUnsupported.
//
// On the `parakeet` build, the result is a *Parakeet whose
// handle is a real *parakeetHandle. Transcribe will call into
// sherpa-onnx and either succeed (when the model is present) or
// fail with a model-not-found error.
//
// Exported so external test files (package stt_test) can use it.
func NewStubParakeet(t *testing.T) *Parakeet {
	t.Helper()
	// We use the unexported struct literal so we don't go
	// through NewParakeet (which would attempt the cgo init
	// and fail without sherpa-onnx installed).
	return &Parakeet{cfg: ParakeetConfig{ModelDir: "stub"}}
}
