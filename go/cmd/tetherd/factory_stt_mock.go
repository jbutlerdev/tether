//go:build production && !parakeet

package main

import "github.com/jbutlerdev/tether/go/internal/stt"

func newSTTImpl(_ STTConfig) (stt.Transcriber, error) {
	return stt.NewMock(), nil
}
