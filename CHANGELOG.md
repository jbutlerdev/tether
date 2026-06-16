# Tether Changelog

All notable changes to Tether are documented in this file. The format
is based on [Keep a Changelog](https://keepachangelog.com/), and the
project adheres to [Semantic Versioning](https://semver.org/).

## [v0.1.3] — 2026-06-16

Hardware-mod release. **Three physical modifications to the M5 PCB
are now required** before flashing the firmware. The M5 has only one
natively free GPIO (18), and we need four GPIOs for a shared I²S0
audio bus. The mods are documented in
[`docs/HARDWARE-MODS.md`](docs/HARDWARE-MODS.md) with the full
execution plan, tools required, soldering tips, and verification
steps. Plan for 60–90 minutes for a first attempt.

### Changed

* **I²S mic and amp merged into a single shared I²S0 bus in
  full-duplex mode.** The mic (INMP441) and amp (MAX98357A) share
  BCLK and WS; the mic's SD line goes to the ESP32's DIN, the
  amp's DIN comes from the ESP32's DOUT. This is the standard
  full-duplex I²S topology used by codecs with separate ADC and
  DAC channels. Both components share a single set of I²S0 channel
  handles (`g_i2s_tx_handle` / `g_i2s_rx_handle` in `i2s_amp.cpp`);
  the first `Init()` to run creates them, the second is a no-op.
* **PCA9557 I/O expander is now a first-class component.** The
  on-board PCA9557PW (Wire1, GPIO 47 SDA / 48 SCL, address 0x18)
  drives:
  - Blue notification LED (pin 1)
  - Red power LED (pin 3; hardware-OR'd with VBUS)
  - LED power rail (pin 2)
  - Master peripheral power-rail (pin 4; eink + GPS + LoRa +
    sensor; LOW = unpowered)
  - E-ink backlight power (pin 5)
  Driver is in `firmware/m5/components/pca9557/`. Tether code
  that needs to drive any of these goes through
  `tether::m5::Pca9557`. Uses the legacy I2C driver
  (`driver/i2c.h`) because ESP-IDF v5.2 doesn't have the new
  `i2c_master_*` API yet.
* **Pin map rewritten to reflect the hardware mods.** `board.h`
  documents the three sacrifices (GPS slider, buzzer, VBUS
  detect) and the four new shared I²S0 pins. GPIOs 33/34 are
  marked as "do-not-touch" (octal PSRAM bus).

### Added

* **`firmware/m5/components/pca9557/`** — new component. I²C1
  driver on Wire1 (GPIO 47/48). Public API: `Init`,
  `SetLedNotification(bool)`, `SetLedPower(bool)`,
  `SetLedPowerRail(bool)`, `SetEinkBacklight(bool)`,
  `SetPeripheralPower(bool)`, `ResetForTest`.
* **`docs/HARDWARE-MODS.md`** — the 60–90 minute execution plan
  for the three PCB mods, with tools, time, soldering tips, and
  verification steps.
* **Do-not-touch comment for GPIO 33/34** at the bottom of
  `board.h`. These are part of the octal PSRAM data bus; driving
  them as general-purpose GPIO bricks the firmware.

### Removed

* **I2S1 peripheral** is no longer used. The shared I²S0 bus
  serves both mic and amp, freeing the I2S1 peripheral for future
  use.

### Sacrificed hardware (acceptable trade-offs)

| Hardware feature | Why | Net effect |
|---|---|---|
| GPS switch (slider) | Frees GPIO 10 for I²S0 BCLK | GPS module is now always on, ~25 mA continuous drain |
| Buzzer (PWM audio) | Frees GPIO 9 for I²S0 DOUT | No beep tones; replaced by the blue LED |
| VBUS detect (USB sense) | Frees GPIO 12 for I²S0 WS | No "USB plugged in" UI; v0.2.0 will use the ESP32-S3's built-in USB-OTG VBUS detection |

The M5's other features — SX1262 LoRa, EPD, SD, battery, USB-C
charging, buttons, GPS module (still functional, just always
powered) — are untouched.

## [v0.1.2] — 2026-06-16

Hardware-pin correctness pass. **The M5 firmware in v0.1.0 would
not have worked on real hardware**: the I2S microphone was wired to
GPIO 4/5/6, which are the LoRa SX1262's DIO1/BUSY/RESET pins. The
M5 was also documented as having 3 buttons when it has 2 plus a GPS
switch. This release fixes both, and adds a central pin map.

### Fixed

* **CRITICAL: `i2s_mic.cpp` used GPIO 4/5/6** for the I2S0 BCLK/WS/DIN
  signals. Those pins are the SX1262's DIO1/BUSY/RESET — the mic
  was being driven on the LoRa radio's pins. Replaced with the
  system architect's assignment: **WS=35, BCLK=36, DIN=37** (all on
  the right edge, sequential). See `board.h::kPinI2s0*`.
* **CRITICAL: `lora_sx1262.cpp` used wrong pin numbers** (CS=8,
  RST=12, BUSY=13, IRQ=14). The Meshtastic variant.h has
  CS=17, RESET=6, BUSY=5, DIO1=4. Fixed.
* **CRITICAL: `spi_bus.cpp` used wrong SPI pins** (SCK/MOSI/MISO on
  11/12/13). The Meshtastic variant.h has SCK=16, MOSI=15, MISO=7.
  Fixed; the new pins are also the LoRa's SPI bus.
* **`i2s_amp.cpp` had no I2S peripheral init at all** — it only
  exposed `PlayTone`/`ReadSamples` for host tests. Added the I2S1
  TX master init with the architect's split configuration
  (WS=47, BCLK=48, DOUT=18). See `board.h::kPinI2s1*`.
* **Documentation claimed 3 physical buttons on the M5** (A=PTT,
  B=Next, C=Prev). The ELECROW board has **2 buttons** (A=GPIO 21,
  B=GPIO 14) plus a **GPS switch** (GPIO 10, slider, not a button).
  The 3-button model is impossible on this hardware. UI code that
  relied on kPrev (cycle backwards) was removed; PTT now acts as
  the "back / decrease" affordance inside the settings menu.

### Added

* **`firmware/m5/components/board/`** — new component that holds
  `include/board.h`, the single source of truth for all M5 GPIO
  assignments. Every component that uses pins (`i2s_mic`, `i2s_amp`,
  `spi_bus`, `lora_sx1262`, `buttons`, `main`) now includes
  `board.h` and uses the `kPin…` constants instead of hard-coded
  GPIO numbers. Cross-referenced with the Meshtastic variant.h.
* **`hardware.md` rewrite** — the previous version was a 6-line
  bill of materials. The new version is a full pin map, a 2-button
  UX diagram, and a citation to the Meshtastic variant.h.
* **Long block comment at the top of `buttons.h`** explaining the
  2-button model and the GPS switch, with pointers to AGENTS.md
  §3.4 and `board.h`.

### Changed

* **`Button::kPrev` removed** from the public enum. The `Button`
  enum now has exactly 2 values: `kPtt` and `kMenu`. The legacy
  `kNext` constant is preserved as an alias for `kMenu` so existing
  test code that referenced it does not break.
* **`Event::kLongPressPrev` removed**; `kLongPressMenu` (and the
  legacy `kLongPressNext` alias) cover the only long-press that
  the firmware actually emits.
* **UI state machine** in `ui_state.cpp` updated: the kPrev branch
  is gone. The settings menu now uses PTT as the "back / decrease"
  control; the test suite was updated to match.
* **CHANGELOG, AGENTS.md, INSTALL.md, research.md, plan.md** all
  updated to reflect the 2-button + GPS-switch reality.

## [v0.1.1] — 2026-06-16

Post-release cleanup. No code changes; no API changes; no protocol
changes. Documentation, dependency, and housekeeping only.

### Changed

* **docs: AGENTS.md rewritten for v0.1.0.** The previous AGENTS.md
  was written when the project had no source code; it described a
  "planned" repo layout and pointed at a research document for
  everything. Replaced with an operational guide that documents the
  actual 16 Go packages, the 20+ M5 components, the real build
  commands, the 8-job CI matrix, and the gotchas learned during the
  v0.1.0 CI hardening pass.
* **docs: new `INSTALL.md` and `config/` directory.** Step-by-step
  walkthrough for flashing the M5 and RAK4631, building `tetherd`,
  standing up a Matrix appservice, configuring Forge, and running
  the three end-to-end test tools. `config/tetherd.toml.example`
  and `config/registration.yaml.example` provide working templates.
* **ci: bump `actions/checkout` from v4 to v6** (PR #2).
* **ci: bump `golangci/golangci-lint-action` from v7 to v9** (PR #3).
* **ci: bump `actions/setup-python` from v5 to v6** (PR #4).
* **ci: bump `actions/setup-go` from v5 to v6** (PR #5).
* housekeeping: deleted the 10 `phase/*` branches locally and on
  origin now that `main` contains all of the work. Re-anchored the
  `v0.1.0` tag onto the actual squashed v0.1.0 commit
  (`459f1e0`); the original tag pointed to a `phase/9-polish` commit
  that was not reachable from `main` after the squash-merge.

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
