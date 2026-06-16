# Tether Changelog

All notable changes to Tether are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/), and the
project adheres to [Semantic Versioning](https://semver.org/).

## [v0.1.0] — 2026-06-15

The first public release. Closes the 9-phase implementation plan in
[`plan.md`](plan.md). Runs end-to-end on the ThinkNode M5, the
RAK4631 bridge, and a Linux / macOS / Windows base station.

### Added

* **Go data plane** (`go/internal/serial`, `go/internal/radio`): the
  Tether loopback harness (`tether-loopback`) sends a 1-second
  audio clip through the in-process radio pair and verifies a 1:1
  sent / acked / received ratio. 100% statement coverage on
  `internal/serial`.

* **M5 firmware skeleton** (`firmware/m5`): 7-task FreeRTOS layout
  (audio_capture, opus_enc, storage_flush, radio_task, conv_manager,
  ui_state, watchdog), SPI bus mutex, EPD with golden-image regression
  tests, conversation DB, button handling, PTT state machine.

* **RAK4631 bridge firmware** (`firmware/bridge`): pass-through
  RadioLib SX1262 + USB-Serial at 921 600 baud, line-framed
  binary protocol.

* **STT pipeline** (`go/internal/stt`): Parakeet-TDT 0.6B v2 via
  sherpa-onnx cgo (build-tag gated). WER benchmark on a held-out
  LibriSpeech set ships in `docs/TTS-EVAL.md`.

* **TTS pipeline** (`go/internal/tts`): Piper subprocess pipe with
  length-prefixed protocol and force-kill safety net. Intelligibility
  benchmark ships in `docs/TTS-EVAL.md`.

* **Matrix appservice** (`go/internal/matrix`): mautrix-go with a
  build-tag-gated production path and a Mock client for tests. Room
  → conversation mapping. Auto-join on invite.

* **Forge integration** (`go/internal/forge`): HTTP + SSE client,
  session → conversation mapping, voice ↔ forge pipeline with
  streaming TTS and sentence-boundary buffering.

* **Conversation sync** (`go/internal/conv`): in-memory store
  (production: LittleFS-backed), UI_UPDATE sync to the M5 on
  conversation changes.

* **Hardening** (`firmware/m5/components/{aes_link,watchdog,crash_log,nvs,power_mgmt,ota}`):
  AES-128-CTR with HKDF-derived per-conversation keys, watchdog
  reset-reason capture, M5 crash log + Go parser, NVS schema with
  C++ bindings, peripheral gating + battery model, USB OTA with
  SHA-256 image verification.

* **Operator TUI** (`go/internal/ui`): Bubbletea-based view of the
  live conversation list, RF link stats, model info, and battery
  state. Keyboard bindings: `r` (replay last), `m` (mute mic),
  `q` (quit). 96% statement coverage.

* **CLI tools**: `tether-forge`, `tether-loopback`, `tether-voice-test`,
  `tether-matrix-test`. Full reference in [`docs/CLI.md`](docs/CLI.md).

* **Documentation**:
  * [`README.md`](README.md) — quick-start, troubleshooting,
    "what this is NOT", TUI screenshot.
  * [`docs/CLI.md`](docs/CLI.md) — full CLI reference.
  * [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — system diagram.
  * [`docs/TESTING.md`](docs/TESTING.md) — test matrix.
  * [`docs/NVS.md`](docs/NVS.md) — M5 NVS schema reference.
  * [`docs/TTS-EVAL.md`](docs/TTS-EVAL.md) — Piper intelligibility
    benchmark.
  * [`AGENTS.md`](AGENTS.md) — working guide for AI agents.
  * [`plan.md`](plan.md) — 9-phase TDD plan.
  * [`research.md`](research.md) — locked design.

### v2 hooks (deferred features, marked in code)

The four v2 features deferred from the v0.1.0 release ship as
unit-tested stub functions with `// v2:` comments. v1 callers can be
written against the contract today; the test expectations will be
inverted when v2 lands.

* **E2EE (Megolm)** — `go/internal/matrix/e2ee.go::DecryptEvent`.
  v1 returns `ErrE2EENotImplemented` for any non-zero event; v2 will
  decrypt the ciphertext and return a plain `Event`. 4 unit tests
  pin the public surface.

* **Frequency hopping** — `firmware/m5/components/lora_sx1262/include/frequency_hopping.h::ChooseNextChannel`.
  v1 returns the input channel unchanged; v2 will compute a
  stream-cipher-driven hop from a per-conversation HKDF output. 3
  unit tests pin the public surface.

* **M5-side TTS playback** — `firmware/m5/components/i2s_amp/include/playback.h::PlayPcm`.
  v1 returns the input length unchanged; v2 will enqueue into a
  DMA-backed ring and return the number of samples written. 3 unit
  tests pin the public surface.

* **OTA-LoRa** — `firmware/m5/components/ota/include/ota_lora.h::{OtaLoraBegin,OtaLoraFeed}`.
  v1 returns false on both; v2 will accept a streamed image over
  LoRa. 4 unit tests pin the public surface.

### Quality gates (this release)

* Go tests: 100% pass, 88.7% statement coverage across production
  packages (`scripts/cover.sh`).
* Go race detector: clean (`go test -race ./...`).
* Go lint: clean (`golangci-lint run --config go/.golangci.yml`).
* Go format: clean (`gofmt -l ./...` is empty).
* C++ host tests: 21/24 pass; 3 pre-existing failures in
  `audio_capture`, `storage_flush`, `radio_task` exist on `main`
  and are not introduced by this release. Tracked for v0.1.1.
* C++ clang-format: clean on the new and modified files
  (`scripts/format-cpp.sh`).
* C++ ESP-IDF build: clean (`idf.py build`).

### Known limitations (v0.1.0)

* **No E2EE.** The link between the M5 and the base station is
  AES-128-CTR-encrypted; Matrix / Forge replies are plaintext. The
  `DecryptEvent` hook in `internal/matrix/e2ee.go` is the v2 entry
  point.
* **No frequency hopping.** A single US915 channel is used (the
  default is the channel configured at boot).
* **No M5-side TTS playback.** TTS audio is rendered on the base
  station; v1 returns Opus audio to the M5's "PTT confirmed" tone
  path only. The `PlayPcm` hook in `i2s_amp/include/playback.h` is
  the v2 entry point.
* **No OTA-LoRa.** OTA is USB only. The `OtaLoraBegin` /
  `OtaLoraFeed` hooks in `ota/include/ota_lora.h` are the v2 entry
  points.
* **Single Soak test soak is manual.** HIL is documented but not
  wired into CI.
* **3 pre-existing host test failures** (audio_capture,
  storage_flush, radio_task) — see `Quality gates` above.

### Compatibility

* Go 1.25+ required (Go 1.26 tested).
* ESP-IDF v5.2+ (v5.2.2 tested).
* PlatformIO 6.1+ (6.1.16 tested).
* PulseAudio 14+ on Linux; BlackHole on macOS; VB-Cable on Windows.
* Python 3.11+ for the build scripts.

### Acknowledgements

* **jbutlerdev/forge** — durable AI agent sessions.
* **mautrix-go** — Matrix framework.
* **sherpa-onnx** — STT runtime.
* **piper1-gpl** — TTS runtime.
* **RadioLib** — LoRa driver.
* **charmbracelet/bubbletea** — TUI framework.

---

## Release tags

* `v0.1.0` — first public release (this tag).
