//go:build production

// wire_prod.go — production wiring for the daemon.
//
// When built with `-tags production`, the daemon wires real
// implementations:
//
//   - serial: go.bug.st/serial -> bridge frame protocol -> radio.Radio
//   - codec:  real Opus via libopus cgo (requires -tags opus)
//   - STT:    network Parakeet via HTTP ([voice] stt_url) — preferred;
//             falls back to cgo Parakeet (requires -tags parakeet)
//   - TTS:    network Kokoro via HTTP ([voice] tts_url) — preferred;
//             falls back to Piper subprocess
//   - forge:  real HTTP + SSE client (requires -tags forge)
//
// Build: go build -tags production,opus,forge ./cmd/tetherd
//
// If a real implementation can't be constructed (missing library,
// missing model, serial port not found), the daemon falls back to the
// mock for that component and logs a warning — this makes the binary
// runnable on any machine even if the full toolchain isn't installed,
// at the cost of degraded functionality.

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/forge"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// wireDependencies constructs production (or fallback-mock)
// dependencies from the config file and returns a ready-to-run
// DaemonConfig.
func wireDependencies(configPath string, logger *slog.Logger) (DaemonConfig, func(), error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return DaemonConfig{}, nil, err
	}

	// Serial transport (real).
	var bridge radio.Radio
	var bridgeCleanup func()
	port, err := serial.OpenSerialPort(cfg.Serial.Port, cfg.Serial.Baud)
	if err != nil {
		logger.Warn("tetherd: serial port open failed, using loopback", "err", err, "port", cfg.Serial.Port)
		a, _ := serial.NewLoopbackPair()
		bridge = a
		bridgeCleanup = func() { a.Close() }
	} else {
		transport := serial.NewTransport(serial.TransportConfig{
			Port: port,
			LogHandler: func(line string) {
				logger.Info("bridge", "log", line)
			},
		})
		if err := transport.Init(context.Background(), defaultPreset()); err != nil {
			logger.Warn("tetherd: serial transport init failed, using loopback", "err", err)
			port.Close()
			a, _ := serial.NewLoopbackPair()
			bridge = a
			bridgeCleanup = func() { a.Close() }
		} else {
			bridge = transport
			bridgeCleanup = func() { transport.Close() }
		}
	}

	// Codec (real Opus if built with -tags opus, else mock).
	codecInstance := newCodec(logger)

	// STT: prefer network voice service, fall back to build-tag impl.
	sttInstance := newSTT(*cfg, logger)

	// TTS: prefer network voice service, fall back to Piper subprocess.
	ttsInstance := newTTS(*cfg, logger)

	// Forge (real HTTP if built with -tags forge, else mock).
	forgeClient := newForge(cfg.Forge, logger)

	// Conversation store (in-memory for v1).
	store := conv.NewMemStore()

	dc := DaemonConfig{
		Bridge:     bridge,
		Store:      store,
		Forge:      forgeClient,
		STT:        sttInstance,
		TTS:        ttsInstance,
		Codec:      codecInstance,
		Logger:     logger,
		AckTimeout: 0, // use Sender default (2s)
		MaxRetry:   0, // use Sender default (5)
		SenderID:   0x0002,
		TargetID:   0xFFFF,
	}
	cleanup := func() {
		bridgeCleanup()
		forgeClient.Close()
		codecInstance.Close()
	}
	return dc, cleanup, nil
}

// defaultPreset returns the LoRa preset (SF11/BW125/CR4/8, 20 dBm,
// sync 0xF3 — research.md §6.1).
func defaultPreset() radio.Preset {
	return radio.Preset{
		SpreadingFactor: 11,
		BandwidthHz:     125000,
		CodingRate:      8,
		TxPowerDbm:      20,
		SyncWord:        0xF3,
	}
}

// Codec factory (build-tag dispatched).

func newCodec(logger *slog.Logger) codec.Opus {
	c, err := newCodecImpl()
	if err != nil {
		logger.Warn("tetherd: codec init failed, using mock", "err", err)
		return codec.NewMock()
	}
	return c
}

// STT factory.
//
// When [voice] stt_url is configured, use the HTTP client (network
// Parakeet service). Otherwise fall back to the build-tag dispatched
// impl (cgo Parakeet with -tags parakeet, or Mock).

func newSTT(cfg Config, logger *slog.Logger) stt.Transcriber {
	if cfg.Voice.STTURL != "" {
		s, err := stt.NewParakeetHTTP(stt.ParakeetHTTPConfig{
			URL:   cfg.Voice.STTURL,
			Model: cfg.Voice.STTModel,
		})
		if err != nil {
			logger.Warn("tetherd: voice STT init failed, using mock", "err", err)
			return stt.NewMock()
		}
		logger.Info("tetherd: using network STT", "url", cfg.Voice.STTURL)
		return s
	}
	s, err := newSTTImpl(cfg.STT)
	if err != nil {
		logger.Warn("tetherd: STT init failed, using mock", "err", err)
		return stt.NewMock()
	}
	return s
}

// TTS factory.
//
// When [voice] tts_url is configured, use the HTTP client (network
// Kokoro service). Otherwise fall back to Piper subprocess.

func newTTS(cfg Config, logger *slog.Logger) tts.Synthesizer {
	if cfg.Voice.TTSURL != "" {
		s, err := tts.NewKokoroHTTP(tts.KokoroHTTPConfig{
			URL:   cfg.Voice.TTSURL,
			Voice: cfg.Voice.Voice,
		})
		if err != nil {
			logger.Warn("tetherd: voice TTS init failed, using mock", "err", err)
			return tts.NewMock()
		}
		logger.Info("tetherd: using network TTS", "url", cfg.Voice.TTSURL, "voice", cfg.Voice.Voice)
		return s
	}
	if cfg.TTS.PiperBinary == "" {
		logger.Warn("tetherd: no piper binary or voice TTS configured, using mock TTS")
		return tts.NewMock()
	}
	s, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: cfg.TTS.PiperBinary,
		VoicePath:  cfg.TTS.Voice,
	})
	if err != nil {
		logger.Warn("tetherd: piper init failed, using mock", "err", err)
		return tts.NewMock()
	}
	return s
}

// Forge factory (build-tag dispatched).

func newForge(cfg ForgeConfig, logger *slog.Logger) forge.Client {
	c := newForgeImpl(cfg.APIURL)
	if cfg.APIKey != "" {
		if _, err := c.Login(context.Background(), cfg.APIKey); err != nil {
			logger.Warn("tetherd: forge login failed, using mock", "err", err)
			return forge.NewMockClient()
		}
	} else {
		logger.Warn("tetherd: no forge API key configured, using mock")
		return forge.NewMockClient()
	}
	return c
}

func init() {
	if _, err := os.Stat("/dev"); err != nil {
		os.Stderr.Write([]byte("tetherd: no /dev directory — serial transport will fail\n"))
	}
}
