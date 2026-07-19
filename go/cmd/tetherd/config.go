// config.go — tetherd configuration (TOML).
//
// The config schema matches research.md §13.5. The daemon loads it
// from tetherd.toml (path overridable with --config). In production
// mode (build tag `production`) the daemon reads the config and wires
// real implementations; in mock mode (default) the config is ignored
// and all mocks are used.
package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Config is the tetherd configuration loaded from tetherd.toml.
type Config struct {
	Serial        SerialConfig        `toml:"serial"`
	Forge         ForgeConfig         `toml:"forge"`
	STT           STTConfig           `toml:"stt"`
	TTS           TTSConfig           `toml:"tts"`
	Audio         AudioConfig         `toml:"audio"`
	Conversations ConversationsConfig `toml:"conversations"`
}

// SerialConfig is the USB-serial port to the RAK4631 bridge.
type SerialConfig struct {
	Port string `toml:"port"`
	Baud int    `toml:"baud"`
}

// ForgeConfig is the forge HTTP + SSE client.
type ForgeConfig struct {
	APIURL string `toml:"api_url"`
	APIKey string `toml:"api_key"`
}

// STTConfig is the Parakeet-TDT STT engine.
type STTConfig struct {
	ModelDir string `toml:"model_dir"`
	Threads  int    `toml:"threads"`
}

// TTSConfig is the Piper TTS engine.
type TTSConfig struct {
	PiperBinary string `toml:"piper_binary"`
	Voice       string `toml:"voice"`
	ChunkChars  int    `toml:"chunk_chars"`
}

// AudioConfig is the virtual audio routing.
type AudioConfig struct {
	Sink        string `toml:"sink"`
	SampleRate  int    `toml:"sample_rate"`
	Channels    int    `toml:"channels"`
}

// ConversationsConfig is the conversation limits.
type ConversationsConfig struct {
	MaxActive      int     `toml:"max_active"`
	HistoryPerConv int     `toml:"history_per_conv"`
	DefaultVolume  float64 `toml:"default_volume"`
}

// LoadConfig reads and parses a TOML config file. Returns a Config
// with defaults applied for any missing fields.
func LoadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("tetherd: config file not found: %s", path)
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("tetherd: parse config: %w", err)
	}
	return cfg, nil
}

// defaultConfig returns a Config with sane defaults.
func defaultConfig() *Config {
	return &Config{
		Serial: SerialConfig{
			Port: "/dev/ttyACM0",
			Baud: 921600,
		},
		Forge: ForgeConfig{
			APIURL: "http://localhost:8080",
		},
		STT: STTConfig{
			Threads: 4,
		},
		TTS: TTSConfig{
			ChunkChars: 80,
		},
		Audio: AudioConfig{
			Sink:       "tether_in",
			SampleRate: 8000,
			Channels:   1,
		},
		Conversations: ConversationsConfig{
			MaxActive:      16,
			HistoryPerConv: 50,
			DefaultVolume:  0.6,
		},
	}
}
