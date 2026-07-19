//go:build production && !opus

package main

import "github.com/jbutlerdev/tether/go/internal/codec"

func newCodecImpl() (codec.Opus, error) {
	return codec.NewMock(), nil
}
