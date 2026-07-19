//go:build !production

// wire_mock.go — mock wiring for the daemon (default build).
//
// When built without the `production` tag, the daemon uses in-process
// mocks for every dependency (STT, TTS, codec, forge, serial). This
// is the CI / dev build: the binary runs and the wiring is exercised
// by go test, but no real hardware or external services are needed.
//
// The production wiring (real serial, real Opus, real Parakeet, real
// Piper, real forge) lives in wire_prod.go (build tag `production`).

package main

import (
	"log/slog"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/forge"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// wireDependencies constructs all mock dependencies and returns a
// ready-to-run DaemonConfig. The `configPath` is ignored in mock mode.
func wireDependencies(_ string, logger *slog.Logger) (DaemonConfig, func(), error) {
	store := conv.NewMemStore()
	fc := forge.NewMockClient()
	bridge, _ := serial.NewLoopbackPair()
	cfg := DaemonConfig{
		Bridge:     bridge,
		Store:      store,
		Forge:      fc,
		STT:        stt.NewMock(),
		TTS:        tts.NewMock(),
		Codec:      codec.NewMock(),
		Logger:     logger,
		AckTimeout: 0, // use Sender default
		MaxRetry:   0, // use Sender default
		SenderID:   0x0002,
		TargetID:   0xFFFF,
	}
	cleanup := func() {
		fc.Close()
		bridge.Close()
	}
	return cfg, cleanup, nil
}

// defaultPreset returns the radio preset for mock mode (unused but
// kept for interface symmetry with wire_prod.go).
func defaultPreset() radio.Preset {
	return radio.Preset{
		SpreadingFactor: 11,
		BandwidthHz:     125000,
		CodingRate:      8,
		TxPowerDbm:      20,
		SyncWord:        0xF3,
	}
}
