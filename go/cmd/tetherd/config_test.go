package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tetherd.toml")
	content := `
[serial]
port = "/dev/ttyUSB0"
baud = 115200

[forge]
api_url = "http://forge.local:8080"
api_key = "test-key-123"

[stt]
model_dir = "/opt/models/parakeet"
threads = 8

[tts]
piper_binary = "/usr/bin/piper"
voice = "/opt/voices/amy.onnx"
chunk_chars = 120

[audio]
sink = "tether_out"
sample_rate = 16000
channels = 2

[conversations]
max_active = 8
history_per_conv = 25
default_volume = 0.8
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Serial.Port != "/dev/ttyUSB0" {
		t.Errorf("serial port: got %q", cfg.Serial.Port)
	}
	if cfg.Serial.Baud != 115200 {
		t.Errorf("baud: got %d", cfg.Serial.Baud)
	}
	if cfg.Forge.APIKey != "test-key-123" {
		t.Errorf("api key: got %q", cfg.Forge.APIKey)
	}
	if cfg.STT.Threads != 8 {
		t.Errorf("threads: got %d", cfg.STT.Threads)
	}
	if cfg.TTS.ChunkChars != 120 {
		t.Errorf("chunk_chars: got %d", cfg.TTS.ChunkChars)
	}
	if cfg.Audio.SampleRate != 16000 {
		t.Errorf("sample_rate: got %d", cfg.Audio.SampleRate)
	}
	if cfg.Conversations.MaxActive != 8 {
		t.Errorf("max_active: got %d", cfg.Conversations.MaxActive)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(\"\"): %v", err)
	}
	if cfg.Serial.Baud != 921600 {
		t.Errorf("default baud: got %d, want 921600", cfg.Serial.Baud)
	}
	if cfg.Audio.SampleRate != 8000 {
		t.Errorf("default sample_rate: got %d, want 8000", cfg.Audio.SampleRate)
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/tetherd.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
