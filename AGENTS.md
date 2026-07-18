# Tether — Agent Development Guide

This document is for AI coding agents (and humans) working on the Tether codebase. The project is at **v0.1.0** — the 9-phase implementation plan in [`plan.md`](plan.md) is complete and merged to `main`. All ten phase branches (`phase/0-tooling` … `phase/9-polish`) have been consolidated into `main` as PR #1.

This guide is **operational** (how to build, test, navigate, and not break things). For the **design** (why we made each decision), read [`research.md`](research.md). For the **phased plan and TDD discipline** (the contract for how new code is written), read [`plan.md`](plan.md). When those three documents conflict, the precedence is: `research.md` > `plan.md` > this file. Update the design first, then the plan, then this file.

---

## 1. Project overview

Tether is a portable, half-duplex, PTT voice-and-text messenger:

1. **M5 (ESP32-S3 + SX1262)** captures audio on PTT press, encodes it with Opus, fragments it over LoRa.
2. **RAK4631 bridge (nRF52840 + SX1262)** forwards LoRa packets to the PC over USB-Serial.
3. **`tetherd` (Go daemon on the PC)** reassembles, decodes, runs **Parakeet-TDT** STT, and dispatches text to the right **Matrix room** or **Forge session**.
4. Replies stream back: text → **Piper** TTS → Opus → LoRa → M5 speaker.
5. Multiple **conversations** (Matrix rooms and Forge sessions) appear as channels on the M5 with persistent history on LittleFS.

Two sibling systems are core dependencies:
* **[`jbutlerdev/forge`](../forge)** — durable AI agent sessions. The base station is a forge HTTP + SSE client.
* **mautrix-go appservice** — Tether is a Matrix puppet.

---

## 2. Source of truth: read these first

* **[`research.md`](research.md)** — the complete design. 1,000+ lines, locked decisions, packet protocol, multi-conversation model, STT/TTS pipeline. **If your code contradicts `research.md`, the research wins** — update the research first.
* **[`plan.md`](plan.md)** — the 9-phase, file-by-file, TDD-driven implementation plan. Currently used as the spec for the *next* thing to build, not a record of what was built. Read it to understand the TDD discipline and the test naming.
* **[`hardware.md`](hardware.md)** — BOM. ESP32-S3 pin mappings must be verified against the ThinkNode M5 schematic before being baked into firmware.
* **[`CHANGELOG.md`](CHANGELOG.md)** — what shipped in each release. Read the `[v0.1.0]` section before assuming a feature is missing.

Don't write any code that depends on a section of `research.md` you've skipped. If a section is unclear, ask.

---

## 3. Critical environment rules (read before doing anything)

### 3.1 The base station is a Go process — not a shell-out

* `tetherd` is a single Go binary (in `go/cmd/tetherd`) that **in-process** uses cgo (sherpa-onnx) and **subprocess pipes** (piper). Do not shell out from bash for hot-path work; do it from Go.
* `sherpa-onnx` (Parakeet STT) is invoked via cgo (`go/internal/stt/parakeet.go`) — never via the CLI from a bash script. The CLI is only for testing.
* `piper` is invoked as a subprocess for v1 (`go/internal/tts/piper.go`); the binary path is in `tetherd.toml`. Do not invoke it with `nix shell` or `sudo`.
* Go toolchain: **Go 1.25** (matches `go/go.mod`). CI installs `1.25` via `actions/setup-go@v5`.

### 3.2 The M5 firmware is C++/FreeRTOS on ESP-IDF

* **No Arduino.** ESP-IDF directly, version **v5.2** (matches the `espressif/idf:v5.2` CI container).
* All SPI activity (SD, SX1262, EPD) takes a single `spi_bus_mutex` (in `firmware/m5/components/spi_bus/`). The LoRa ISR is flag-setter only — heavy SPI work happens in the radio task. **Do not break this.** The pattern is load-bearing.
* Use the `micro_opus` component (vendored under `firmware/m5/components/micro_opus/`) for Opus encode. `esp-libopus` is a fallback.
* The PSRAM ring buffer (`firmware/m5/components/psram_ring/`) is single-producer / single-consumer — no mutex, just a memory barrier.
* Test infrastructure: a Linux host-side build of every component runs under `firmware/m5/test_host/` with a FreeRTOS shim. Use `idf.py build` for the on-target binary; the host-side tests live in `firmware/m5/test_host/` and are run by `pio test` (when wrapped) or via the CI workflow.

### 3.3 The RAK4631 bridge firmware is C++ on PlatformIO + RadioLib

* `firmware/bridge/` — PlatformIO project, `platform = nordicnrf52`, `framework = arduino`, `lib_deps = jgromes/RadioLib`.
* Speaks a line-framed binary protocol (`firmware/bridge/src/frame.{h,cpp}`) over USB-Serial at 921 600 baud. Frame format: `0xAA 0x55 | type(1) | len(2 LE) | payload(len) | crc16(2 LE)`.
* No filesystem, no SD — pure pass-through. The Go side owns state.
* Test envs: `native` (runs on Linux without hardware) and `rak4631` (requires a physical RAK4631). CI runs `pio test -e native` only.

### 3.4 Two buttons + a GPS switch, no touchscreen

The M5 has exactly **two** physical buttons (not three — see the
[Meshtastic ELECROW-ThinkNode-M5 variant.h](https://raw.githubusercontent.com/meshtastic/firmware/refs/heads/develop/variants/esp32s3/ELECROW-ThinkNode-M5/variant.h),
which defines `PIN_BUTTON1=21` and `PIN_BUTTON2=14`). The third
"control" on the case is a *switch* (slider) for the GPS module, not
a button.

* **A (front, large, GPIO 21) = PTT** — push to record, release to
  enqueue + transmit.
* **B (side, GPIO 14) = Menu / cycle** — short press cycles to the
  next conversation; long-press enters the settings menu.
* **GPS slider (GPIO 10, digital input)** — senses the GPS toggle
  position; not a button.

The 1.54″ EPD is the only display. **Do not design any UX that
requires more inputs** — there isn't room. The state machine is in
`firmware/m5/components/ptt/`, the UI states are in
`firmware/m5/components/ui_state/`, and the GPIO map is in
`firmware/m5/components/board/include/board.h`.

### 3.4.1 Pin map is in one place

All M5 GPIO assignments live in
`firmware/m5/components/board/include/board.h` (a separate ESP-IDF
component called `board`). Do not hard-code GPIO numbers in
component code — `#include "board.h"` and reference the
`kPin…` constants. The header is the single source of truth and is
cross-checked against the Meshtastic variant.h.

### 3.4.2 Two hardware modifications are required

The Tether audio path needs 4 GPIOs for a shared full-duplex
I²S0 bus, but the M5 has only one natively free pin (GPIO 18).
Two mods are required to free the other three:

1. **GPS module removal** — desolder the Quectel L76K GPS module
   from the M5 board (LCC, 9.7×10.1 mm, 18 pins). *Frees GPIO 19
   and GPIO 20 for I²S0 WS / BCLK (also frees GPIO 10, 11, 13 as
   a side-effect).* Compared to the older v0.1.3 "GPS Always-On"
   hack, this also removes the ~25 mA continuous drain of the GPS
   being permanently powered, and leaves GPIO 12 (USB VBUS
   detect) **intact**.
2. **Buzzer removal** — desolder the SMD buzzer. *Frees GPIO 9
   for I²S0 DOUT (amp DIN).*

After the mods, the I²S0 bus is wired full-duplex (mic and amp
share BCLK/WS, separate data lines):

| Signal | GPIO | Source |
|---|---|---|
| WS (LRC) | 19 | freed by GPS removal (was GPS L76K RX) |
| BCLK | 20 | freed by GPS removal (was GPS L76K TX) |
| Mic SD (DIN) | 18 | natively free |
| Amp DIN (DOUT) | 9 | freed by buzzer removal |

The PCA9557 on Wire1 (GPIO 47/48) drives the LEDs, the e-ink
backlight, and the master peripheral power rail — see §3.4.3.

**Do not flash the firmware onto an unmodified M5.** The full
execution plan with tools, time, and verification steps is in
[`docs/HARDWARE-MODS.md`](docs/HARDWARE-MODS.md).

**Pins explicitly do-not-touch:** GPIO 33 and GPIO 34 are part of
the octal PSRAM bus. Driving them as general-purpose GPIO crashes
the PSRAM controller. The pin map in `board.h` avoids them by
design; if you add a new component, do not propose a pin map that
touches 33/34.

### 3.4.3 PCA9557 I/O expander is required

The PCA9557PW on Wire1 (I²C1, GPIO 47 SDA / 48 SCL, address 0x18)
is the canonical interface for:

- **Blue notification LED** (pin 1 of the expander)
- **Red power LED** (pin 3; hardware-OR'd with VBUS)
- **LED power rail** (pin 2)
- **Master peripheral power enable** (pin 4; eink + GPS + LoRa +
  sensor — LOW = unpowered)
- **E-ink backlight power** (pin 5)

The driver is in `firmware/m5/components/pca9557/`. Tether code
that wants to drive any of the above goes through
`tether::m5::Pca9557`, never through direct GPIO. The expander
also serves as the master power-gate for `power_mgmt.cpp` to
enter deep sleep, and as the recovery vector for `watchdog.cpp`
on LoRa fault.

### 3.5 The base station is Linux-preferred, but cross-platform

* **Linux:** primary target. PulseAudio null sink is the audio routing (see `go/internal/audio/pulse.go`).
* **Windows:** supported via VB-Cable.
* **macOS:** supported via BlackHole.
* Test all three if you touch the audio layer.

### 3.6 The protocol header is versioned

`proto/tether.proto` defines the in-memory `Envelope`/`Ack` structs. The **on-the-wire format is a fixed 34-byte binary header + payload** (research.md §8.1) with a CRC-16/CCITT-FALSE over the header; the ACK is a self-describing 28-byte payload (research.md §8.6). There is **no `protocol_version` byte on the wire** — the format itself is the version. The M5 firmware (C++ mirror in `firmware/m5/components/protocol`), the bridge firmware, and `tetherd` must all agree on the format. If you change the header layout, that is a coordinated break across all three; update `research.md` §8.1 first, then the Go codec (`go/pkg/protocol`), then the M5 C++ mirror, and re-run the fuzz test (`go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s`).

---

## 4. Repository layout (actual)

```
tether/
├── README.md             # project overview + preview image
├── AGENTS.md             # this file
├── CHANGELOG.md          # release notes
├── LICENSE               # MIT
├── hardware.md           # bill of materials
├── research.md           # design (source of truth)
├── plan.md               # phased TDD implementation plan
├── agent-loop.sh         # CI-style driver that ran the 10 phase branches
│
├── .github/
│   └── workflows/
│       ├── ci.yml              # go-test, go-lint, cpp-format, proto-verify, firmware-build-m5, firmware-test-bridge
│       └── firmware-build.yml  # m5-esp-idf-build, bridge-platformio-test
│
├── docker/               # dev.Dockerfile with ESP-IDF, PlatformIO, sherpa-onnx, piper
├── docs/                 # preview.png + ARCHITECTURE/TESTING/CLI/NVS references
├── proto/                # tether.proto + gen.sh + testdata/
│
├── go/                   # tetherd (Go daemon, module github.com/jbutlerdev/tether/go)
│   ├── go.mod            # requires Go 1.25
│   ├── .golangci.yml     # v2 schema: formatters + linters, 80% coverage gate
│   ├── cmd/
│   │   └── tetherd/      # main entry point
│   ├── pkg/
│   │   └── protocol/     # wire format (proto-generated + hand-written helpers)
│   │       └── protocolpb/  # protoc-generated *.pb.go (committed)
│   ├── internal/
│   │   ├── audio/        # Sink interface + file + PulseAudio
│   │   ├── codec/        # Opus, WAV, polyphase resampler
│   │   ├── conv/         # Conversation state machine + persistence
│   │   ├── crashlog/     # M5 crash-log parser (Go side)
│   │   ├── crypto/       # HKDF-SHA256 + AES-128-CTR helpers
│   │   ├── forge/        # HTTP + SSE client + voice↔forge pipeline
│   │   ├── loopback/     # loopback tool that drives Sender+Receiver in one process
│   │   ├── matrix/       # mautrix-go appservice
│   │   ├── nvs/          # NVS schema (host-side)
│   │   ├── power/        # battery model
│   │   ├── radio/        # Sender + Receiver state machines + mock
│   │   ├── scripts_test/ # CI smoke tests
│   │   ├── serial/       # RAK4631 bridge protocol (frame codec, loopback)
│   │   ├── stt/          # Parakeet-TDT (cgo) + Transcriber interface + mock
│   │   ├── tts/          # Piper subprocess + Synthesizer interface + mock
│   │   └── ui/           # bubbletea TUI
│   └── tools/
│       ├── tether-cli/        # operator CLI
│       ├── tether-loopback/   # end-to-end Phase 1 test
│       ├── tether-voice-test/ # end-to-end Phase 5 test
│       └── tether-matrix-test/# end-to-end Phase 6 test
│
├── firmware/
│   ├── m5/                       # ESP-IDF project for ThinkNode M5
│   │   ├── CMakeLists.txt
│   │   ├── sdkconfig.defaults    # ESP32-S3 + 8 MB PSRAM
│   │   ├── main/                 # main.cpp, idf_component.yml, Kconfig
│   │   ├── test_host/            # Linux host-side test harness
│   │   └── components/           # 20+ components (see §4.2)
│   └── bridge/                   # PlatformIO project for RAK4631
│       ├── platformio.ini
│       ├── src/                  # main.cpp, frame.{h,cpp}, lora.{h,cpp}, serial_link.{h,cpp}
│       └── test/                 # native + on-device tests
│
├── scripts/                       # cover.sh, ci.sh, fetch-models.sh, format-cpp.sh
│
├── logs/                          # agent-loop.sh per-phase logs (gitignored)
├── .phase-state                   # agent-loop.sh state file (gitignored)
├── .agent-loop.lock               # agent-loop.sh pid lock (gitignored)
└── .gitignore
```

### 4.1 Go internal package responsibilities

| Package | Purpose |
|---|---|
| `go/internal/serial` | USB-Serial framing + loopback transport for the RAK4631 bridge. 100% coverage. |
| `go/internal/radio` | Sender + Receiver state machines (per-chunk ACK, cumulative bitmap, retry budget, race-detector tested). |
| `go/internal/codec` | Opus encode/decode wrappers, WAV header, polyphase resampler (8 ↔ 16 ↔ 22.05 kHz). |
| `go/internal/conv` | Conversation state machine + per-conv history ring + persistence interface. |
| `go/internal/stt` | `Transcriber` interface, Parakeet-TDT int8 via sherpa-onnx cgo (build-tag gated), mock, WER benchmark. |
| `go/internal/tts` | `Synthesizer` interface, Piper subprocess wrapper (length-prefixed protocol), mock, intelligibility benchmark. |
| `go/internal/audio` | `Sink` interface, file WAV writer, PulseAudio sink. |
| `go/internal/matrix` | mautrix-go appservice, room_id → conv_id mapping, `UI_UPDATE` sync to M5. |
| `go/internal/forge` | HTTP + SSE client, session UUID → conv_id mapping, voice↔forge pipeline with streaming TTS, `tether forge` CLI subcommand. |
| `go/internal/loopback` | `RunOnce` tool that drives a Sender + auto-ACK helper in one process — used by `tether-loopback` for end-to-end tests. |
| `go/internal/crypto` | HKDF-SHA256 (RFC 5869 test vectors) + AES-128-CTR + per-conversation key derivation. |
| `go/internal/crashlog` | M5 crash-log parser (the M5 firmware writes LittleFS crash files; this package parses them). |
| `go/internal/nvs` | Host-side NVS schema (mirrors the M5's C++ bindings in `firmware/m5/components/nvs/`). |
| `go/internal/power` | Battery model (deep-sleep / light-sleep / active states). |
| `go/internal/ui` | bubbletea TUI for the operator. |
| `go/internal/scripts_test` | CI smoke tests for the `scripts/` directory. |
| `go/pkg/protocol` | Wire format: `Envelope` (proto-generated), `Header` helpers, `Fragment` / `Reassemble`, `AckBitmap` (32-bit rolling window), `CRC-16/CCITT`. Includes the `FuzzEnvelopeDecode` fuzzer. |

### 4.2 M5 firmware components (under `firmware/m5/components/`)

| Component | Purpose |
|---|---|
| `protocol` | On-target C++ mirror of the wire format (CRC, header encode/decode). |
| `spi_bus` | `SpiBus` singleton + `spi_bus_mutex`; per-CS `spi_device_handle_t`. SCK=16, MOSI=15, MISO=7 (from `board.h`). |
| `i2s_mic` | INMP441 I2S RX. **Shared I2S0 full-duplex bus**: WS=19, BCLK=20, DIN=18. Requires the GPS-removal + buzzer-removal mods. |
| `i2s_amp` | MAX98357A I2S TX. **Shared I2S0 full-duplex bus**: WS=19, BCLK=20, DOUT=9. Same handle as i2s_mic (single full-duplex I2S0). |
| `pca9557` | Wire1 I2C1 driver for the on-board PCA9557PW expander. LEDs, e-ink backlight, master peripheral power-rail. |
| `lora_sx1262` | SX1262 driver wrapper (channel, preset, CAD, TX, RX). |
| `sd_card` | LittleFS mount + POSIX file API. |
| `i2s_mic` | INMP441 I2S master RX, DMA 4×256 samples. |
| `i2s_amp` | MAX98357A I2S master TX + sine-wave beep generator. |
| `opus_enc` | micro-opus wrapper, 8 kHz / 16 kbps / 20 ms frames. |
| `psram_ring` | SPSC ring buffer (8 MB PSRAM, no mutex). |
| `littlefs_vfs` | Typed file API over the LittleFS mount. |
| `epd` | 1.54″ EPD controller + screen renderers (idle / recording / queued / TX / TTS / settings / low-battery). Golden-image tests. |
| `board` | Pin map for the ThinkNode M5 (ELECROW). One header, one source of truth. See `include/board.h`. |
| `buttons` | GPIO IRQ + debounce + long-press detection. The M5 has **2 physical buttons** (A=PTT, B=Menu); the third M5 control is a GPS *switch* on a different physical pad. |
| `ptt` | PTT state machine (idle → recording → queued → transmitting → acked/failed).
| `conv_db` | Conversation DB in LittleFS (up to 16 convs, 50-message history ring each). |
| `conv_manager` | Task: handles incoming `UI_UPDATE` from base, syncs conv list. |
| `ui_state` | UI state machine (current conv, scroll position, partial-refresh rate limiter). |
| `audio_capture` | Task: drains I2S DMA → Opus encoder → PSRAM ring. |
| `storage_flush` | Task: drains PSRAM ring → LittleFS. |
| `radio_task` | Task: reads pending messages from LittleFS, transmits, handles ACKs. |
| `watchdog` | Task: feeds all task WDTs every 500 ms. |
| `power_mgmt` | Task: enters light/deep sleep when idle. |
| `aes_link` | HKDF-SHA256 + per-conversation key + SX1262 `setEncryption`. |
| `crash_log` | On-panic, dump backtrace to LittleFS; upload to base on boot. |
| `ota` | USB flash path with SHA-256 image verification. (LoRa-OTA is a v2 stub.) |
| `RadioLib` (vendored) | Underlying SX1262 driver. |
| `esp_littlefs` (vendored) | LittleFS component. |
| `micro_opus` (vendored) | libopus wrapper. |

---

## 5. Build & test commands

```bash
# ── Go daemon ────────────────────────────────────────────────────────
cd go
go mod download
go build -o tetherd ./cmd/tetherd
GOWORK=off go test -race -coverprofile=cover.out -covermode=atomic ./...
bash scripts/cover.sh cover.out 80         # 80% coverage gate (enforced in CI)
GOWORK=off go vet ./...
GOWORK=off go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s  # protocol fuzzer

# ── M5 firmware (ESP-IDF) ───────────────────────────────────────────
cd firmware/m5
. /opt/esp/idf/export.sh                   # ESP-IDF v5.2 env
idf.py set-target esp32s3
idf.py build
# LilyGO T3-S3 MVSR variant (see docs/VARIANTS.md):
idf.py -DSDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.t3s3_mvsr" reconfigure
idf.py build
idf.py -p /dev/ttyUSB0 flash monitor

# ── Bridge firmware (PlatformIO) ─────────────────────────────────────
cd firmware/bridge
pio test -e native                         # native env runs on Linux
pio run                                    # build for the rak4631 env
pio run -t upload                          # upload to a physical RAK4631

# ── Models ───────────────────────────────────────────────────────────
./scripts/fetch-models.sh                  # pulls Parakeet-TDT 0.6B v2 int8 + Piper voices
```

### 5.1 CI matrix (`.github/workflows/ci.yml` + `firmware-build.yml`)

| Job | What it does |
|---|---|
| `go-test (race + coverage)` | `go test -race -coverprofile=cover.out -covermode=atomic ./...`; `bash scripts/cover.sh cover.out 80`. Go 1.25. |
| `go-lint` | `golangci/golangci-lint-action@v7`, `version: v2.12.2` (built with Go 1.25). |
| `cpp-format` | `clang-format --dry-run --Werror` on every `*.cpp`/`*.h`/`*.hpp`, excluding `.pio/` and `build/`. |
| `proto-verify` | `protoc v28.0` + `protoc-gen-go v1.36.6`, then `git diff --exit-code proto/ go/pkg/protocol/`. |
| `firmware-build-m5` | `espressif/idf:v5.2` container, sources `/opt/esp/idf/export.sh` in the same shell as `idf.py build`. |
| `firmware-test-bridge` | `pio test -e native` on `firmware/bridge/`. |
| `m5-esp-idf-build` | Same as `firmware-build-m5` but in the `firmware-build.yml` workflow. |
| `bridge-platformio-test` | Same as `firmware-test-bridge` but in the `firmware-build.yml` workflow. |

All 8 must pass before a PR can merge.

---

## 6. Coding conventions

### 6.1 Go (`go/`)

* `gofmt`, `goimports`, `golangci-lint v2.12.2` (see `go/.golangci.yml`).
* Errors wrap with `fmt.Errorf("...: %w", err)`. Use `errors.Is` / `errors.As`.
* Concurrency: goroutines + channels; explicit context propagation. **No goroutine leaks** — every goroutine must have a clear shutdown path (return on `<-ctx.Done()`, on closed stop channel, etc.).
* No global state. Config flows in via the constructor.
* Logging: `log/slog` (stdlib). Structured fields, not formatted strings.
* All public types/methods documented. `revive` enforces `exported` and `error-strings` rules.
* All public-package symbols tested (mock for external deps; 80% coverage gate).

### 6.2 C++ (firmware)

* Allman braces, 4-space indent, `clang-format` with the Espressif default.
* C++17. No exceptions in ISR context. No dynamic allocation in ISR.
* FreeRTOS primitives only for inter-task communication. No `std::thread`.
* Header guards `#pragma once`. Forward-declare where possible.
* ESP-IDF logging: `ESP_LOGI`, `ESP_LOGW`, `ESP_LOGE` with a per-module tag.
* `//nolint:revive` / `//nolint:staticcheck` / `//nolint:govet` are fine for deliberate exceptions. Always include a brief justification.

### 6.3 Wire format

* `proto/tether.proto` is canonical. `proto/gen.sh` regenerates both Go and C++ code.
* The generated `go/pkg/protocol/protocolpb/*.pb.go` is **committed** to the repo so that builds do not require protoc locally. **Never hand-edit generated code.**
* Wire format is **little-endian**, **CRC-16/CCITT-FALSE** for header checksums (poly 0x1021, init 0xFFFF, no reflect, no xorout).

---

## 7. Common tasks

### 7.1 Adding a new conversation kind

1. Add the kind to the `ConvKind` enum in `proto/tether.proto` and regenerate.
2. Add the Go mapping (in `go/internal/matrix/room_to_conv.go` or `go/internal/forge/session_to_conv.go`).
3. Update the M5 EPD screens in `firmware/m5/components/epd/src/screens.cpp` to show the new kind.
4. Add a test vector in `proto/testdata/`.
5. Run `bash proto/gen.sh` and commit the regenerated files.
6. Push; CI must stay green.

### 7.2 Changing the LoRa preset

**Don't**, unless you have a measurement to justify it. The current preset is **SF11 / BW125 / CR 4/8** — chosen for range over speed. If you change it:

1. Update `firmware/m5/components/lora_sx1262/` and `firmware/bridge/src/lora.cpp`.
2. Update the airtime table in `research.md` §6.3.
3. Verify the link budget in `research.md` §6.4 still works for the new preset.
4. Update the tests in `go/internal/radio/` and the bridge tests.

### 7.3 Adding a new TTS voice

1. Drop the `.onnx` + `.onnx.json` into `/var/lib/tether/piper-voices/`.
2. Update `tetherd.toml` to point at it.
3. Re-run `go test ./internal/tts/ -tags=piper` with the new voice.
4. Document the intelligibility benchmark in `docs/TTS-EVAL.md`.

### 7.4 Touching the SPI mutex pattern

**Don't break it.** The pattern in `firmware/m5/components/spi_bus/` is load-bearing. The LoRa ISR is flag-setter only because any SPI work in the ISR will deadlock with an in-flight SD write. If you need more state from the radio in the ISR, add it to the deferred task — don't put it in the handler.

### 7.5 Touching the protocol

If you change a packet field, **update `research.md` §8.1/§8.6 first** (the wire format is the source of truth), then the Go codec (`go/pkg/protocol` header.go / ack.go), then the M5 C++ mirror (`firmware/m5/components/protocol`). The fixed header has no version byte — a layout change is a coordinated break across the M5 firmware, bridge firmware, and `tetherd`, or you get garbage. Regenerate the protobuf (the in-memory structs), commit, and re-run the fuzz test (`go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s`).

### 7.6 Running the per-phase TDD loop again

If you want to apply the TDD discipline to a new feature (post-v0.1.0):

```bash
# One-shot, single phase
./agent-loop.sh -p 0

# Specific phases
./agent-loop.sh -p 3 -p 5

# Resume from a checkpoint
./agent-loop.sh --from 4

# Preview without running
./agent-loop.sh --dry-run -p 2

# See progress
./agent-loop.sh --status
```

`agent-loop.sh` invokes `pi -p --model minimax-anthropic/MiniMax-M3` with a TDD prompt per phase, runs the test/lint/coverage gates, and re-prompts on failure (up to `--max-attempts`). See its own header comment for the full TDD contract.

---

## 8. Testing

* **Go:** table-driven tests + `testing/quick` for property-based tests. Every package has `_test.go` next to its `*.go`. Use the `Radio`, `Transcriber`, `Synthesizer`, `Client`, `Sink` interfaces (not the concrete impls) so tests run without real hardware / network.
* **Coverage gate:** `bash scripts/cover.sh cover.out 80` must pass. Add tests, never delete code, to meet it.
* **Fuzz testing:** `go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s` on the protocol package — 60s is the CI minimum.
* **Property tests:** `testing/quick` for round-trip invariants (fragment/reassemble, ACK bitmap wraparound, HKDF).
* **M5 (host-side):** every component has a Linux build under `firmware/m5/test_host/`. Run via the test harness.
* **M5 (on-target):** `idf.py monitor` for serial logs. Real hardware tests on a desk before claiming "works".
* **Bridge:** `pio test -e native` for unit; bench rig for integration.
* **End-to-end:** `go run ./tools/tether-loopback` and `tether-voice-test` are the fastest feedback loops.

Tests live next to the code they test. No separate `tests/` directory.

---

## 9. Things that have bitten us / will bite you

* **Don't compile sherpa-onnx from source on the base station.** Use the prebuilt release tarball (the `fetch-models.sh` script pulls the int8 model + `sherpa-onnx` C-API). Compilation takes 30+ minutes and rarely works the first time.
* **Don't use `sudo` inside the M5's NVS scripts.** The M5 has no privilege model.
* **The SX1262 BUSY pin must be polled before every SPI transaction.** RadioLib handles this, but if you bypass RadioLib, you'll hang the bus.
* **LittleFS on SD card is slow on first mount.** First boot after flashing can take 2–3 seconds to mount. Don't put it in the critical path.
* **Piper subprocess pipe can stall if you don't drain it.** Read in a goroutine; block-write is fine. The `piper_subprocess.go` test for force-kill on hang exists for a reason.
* **mautrix-go appservice registrations are persistent on the homeserver.** Removing a registration requires a manual homeserver-side delete. Test in a throwaway homeserver.
* **Forge sessions idle out at 30 minutes.** A "session expired" indicator on the M5 is in `research.md` §12.7 — don't drop messages silently.
* **Parakeet-TDT 0.6B v2 is non-streaming in ONNX.** The full clip must arrive before STT begins. This is OK for store-and-forward but is the reason forge text deltas are buffered until sentence boundary.
* **EPD partial refreshes leave ghosting.** After ~50 partials, do a full refresh. The UI state machine in `firmware/m5/components/ui_state/` handles this.
* **Always clone the envelope before mutating in the Sender.** The original `Sender.Run` had a race where two Senders sharing an `envs` slice would race on the RETRANSMIT bit. Fixed in commit `8a86273` — see the comment in `go/internal/radio/sender.go:140`.
* **Test harness context churn kills ACK turnaround.** The original `autoAck` helper created a fresh 50ms context per Receive call; under `-race -count=N` that added enough overhead to exceed the sender's retry budget. Use a single long-lived context with `t.Cleanup` cancellation. Same lesson applies to any test helper that pumps a channel.
* **`protoc-gen-go @latest` will drift away from the committed `.pb.go`.** The `proto-verify` CI job pins **protoc v28.0** and **protoc-gen-go v1.36.6** to match. Bump them together.
* **golangci-lint v1.x is built with Go 1.24.** It refuses to lint modules targeting Go 1.25. We use v2.12.2 (built with Go 1.25). When you bump Go, bump golangci-lint too.
* **The ESP-IDF v5.2 I2S `std_slot_config_t` no longer has `left_align` or `big_endian`.** Use `bit_shift = true` for left-align / MSB-first.

---

## 10. When in doubt

* Read `research.md`. Most "what should this do?" questions are answered there.
* Read `plan.md`. Most "what tests should I write?" questions are answered there.
* Read `hardware.md`. Most "what's the M5 capable of?" questions are answered there.
* Look at how **`jbutlerdev/forge`** structures things. We follow the same conventions (workspace layout, AGENTS.md style, doc-as-source-of-truth, `slog` for logging, `cgo` for C bindings).
* If still unclear, **ask the human**.

---

## 11. Related projects

* **[`../forge`](../forge)** — sibling project. Rust + pi-mono + PostgreSQL. Tether's forge client lives at `go/internal/forge/`.
* **[`../pi-mono`](../pi-mono)** — the agent runtime forge wraps. Tether doesn't depend on it directly, but understanding its event stream helps with the forge SSE integration.
* **[mautrix-go](https://github.com/mautrix/go)** — Matrix framework. Tether's appservice uses `maunium.net/go/mautrix`. The [registering appservices](https://docs.mau.fi/bridges/general/registering-appservices.html) doc is the canonical reference.
* **[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)** — Parakeet runtime. The [NeMo transducer models page](https://k2-fsa.github.io/sherpa/onnx/pretrained_models/offline-transducer/nemo-transducer-models.html) is the canonical reference.
* **[piper1-gpl](https://github.com/OHF-Voice/piper1-gpl)** — TTS. Subprocess pipe for v1.
* **[RadioLib](https://github.com/jgromes/RadioLib)** — LoRa driver. Used on both M5 and bridge.
* **[mautrix-gmessages](https://github.com/mautrix/gmessages)** — Google Messages bridge. We don't bundle it, but its architecture is the reference pattern for any future bridge integration.
