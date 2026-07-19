// Parakeet-TDT 0.6B v2 via sherpa-onnx. See plan.md §6.2.
//
// This file is the cgo integration. It is only compiled when the
// `parakeet` build tag is set and the system has:
//
//   - sherpa-onnx C library at /usr/local/lib (override via
//     CGO_LDFLAGS)
//   - sherpa-onnx headers at /usr/local/include
//   - onnxruntime at /usr/local/lib
//
// To enable:
//
//	sudo apt install libonnxruntime-dev   # or download release
//	cd /tmp && wget https://github.com/k2-fsa/sherpa-onnx/releases/download/.../sherpa-onnx-...tar.bz2
//	tar xjf sherpa-onnx-*.tar.bz2
//	sudo cp sherpa-onnx-*/lib/* /usr/local/lib/
//	sudo cp -r sherpa-onnx-*/include/sherpa-onnx /usr/local/include/
//	cd go && go test -tags parakeet ./internal/stt/
//
//go:build parakeet

package stt

/*
#cgo LDFLAGS: -L/usr/local/lib -lsherpa-onnx-c-api -lonnxruntime
#cgo CFLAGS: -I/usr/local/include
#include <sherpa-onnx/c-api/c-api.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"
)

// parakeetHandle wraps the C recognizer and the result string we
// must free after copying into Go.
type parakeetHandle struct {
	rec *C.SherpaOnnxOfflineRecognizer
}

// newParakeetImpl loads the model from cfg.ModelDir and returns
// a *Parakeet ready to Transcribe.
func newParakeetImpl(cfg ParakeetConfig) (*Parakeet, error) {
	tokens := filepath.Join(cfg.ModelDir, "tokens.txt")
	encoder := filepath.Join(cfg.ModelDir, "encoder.int8.onnx")
	decoder := filepath.Join(cfg.ModelDir, "decoder.int8.onnx")
	joiner := filepath.Join(cfg.ModelDir, "joiner.int8.onnx")

	cTokens := C.CString(tokens)
	cEncoder := C.CString(encoder)
	cDecoder := C.CString(decoder)
	cJoiner := C.CString(joiner)
	cModelType := C.CString("nemo_transducer")
	cDecoding := C.CString("greedy_search")
	defer C.free(unsafe.Pointer(cTokens))
	defer C.free(unsafe.Pointer(cEncoder))
	defer C.free(unsafe.Pointer(cDecoder))
	defer C.free(unsafe.Pointer(cJoiner))
	defer C.free(unsafe.Pointer(cModelType))
	defer C.free(unsafe.Pointer(cDecoding))

	numThreads := cfg.NumThreads
	if numThreads <= 0 {
		numThreads = 1
	}

	// Build the config. Field layout matches sherpa-onnx/c-api.h
	// (the inner model_config struct is reused for all model
	// types; we just set the transducer sub-fields).
	config := C.SherpaOnnxOfflineRecognizerConfig{}
	config.model_config.debug = 0
	config.model_config.num_threads = C.int(numThreads)
	config.model_config.provider = C.CString("cpu")
	defer C.free(unsafe.Pointer(config.model_config.provider))
	config.model_config.tokens = cTokens
	config.model_config.model_type = cModelType
	config.model_config.transducer.encoder = cEncoder
	config.model_config.transducer.decoder = cDecoder
	config.model_config.transducer.joiner = cJoiner
	config.decoding_method = cDecoding

	rec := C.SherpaOnnxCreateOfflineRecognizer(&config)
	if rec == nil {
		return nil, errors.New("stt: parakeet: SherpaOnnxCreateOfflineRecognizer returned NULL")
	}
	p := &Parakeet{cfg: cfg}
	p.handle = &parakeetHandle{rec: rec}
	return p, nil
}

// transcribeImpl runs the recognizer on the (already 16 kHz)
// mono PCM buffer using the stream-based offline API:
//
//	CreateOfflineStream → AcceptWaveform → Decode → GetResult
//
// The sherpa-onnx API is synchronous; we still honour ctx by
// aborting before the call.
func transcribeImpl(ctx context.Context, p *Parakeet, mono []float32) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	handle, ok := p.handle.(*parakeetHandle)
	if !ok || handle == nil || handle.rec == nil {
		return "", errors.New("stt: parakeet: not initialised")
	}
	if len(mono) == 0 {
		return "", nil
	}

	stream := C.SherpaOnnxCreateOfflineStream(handle.rec)
	if stream == nil {
		return "", errors.New("stt: parakeet: CreateOfflineStream returned NULL")
	}
	defer C.SherpaOnnxDestroyOfflineStream(stream)

	// Feed the audio samples.
	C.SherpaOnnxAcceptWaveformOffline(
		stream,
		C.int(16000), // sample rate
		(*C.float)(unsafe.Pointer(&mono[0])),
		C.int(len(mono)),
	)

	// Decode.
	C.SherpaOnnxDecodeOfflineStream(handle.rec, stream)

	// Get the result.
	res := C.SherpaOnnxGetOfflineStreamResult(stream)
	if res == nil {
		return "", errors.New("stt: parakeet: GetOfflineStreamResult returned NULL")
	}
	text := C.GoString(res.text)
	if text == "" {
		return "", nil
	}
	return fmt.Sprintf("%s", text), nil
}

// closeImpl releases the recognizer.
func closeImpl(p *Parakeet) error {
	handle, ok := p.handle.(*parakeetHandle)
	if !ok || handle == nil || handle.rec == nil {
		return nil
	}
	C.SherpaOnnxDestroyOfflineRecognizer(handle.rec)
	handle.rec = nil
	return nil
}
