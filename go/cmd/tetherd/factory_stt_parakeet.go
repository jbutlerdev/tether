//go:build production && parakeet

package main

import (
	"github.com/jbutlerdev/tether/go/internal/stt"
)

func newSTTImpl(cfg STTConfig) (stt.Transcriber, error) {
	return stt.NewParakeet(stt.ParakeetConfig{
		ModelDir:   cfg.ModelDir,
		NumThreads: cfg.Threads,
	})
}
