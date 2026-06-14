# Tether — Agent Development Guide

This document is for AI coding agents (and humans) working on the Tether codebase. Read it before making changes. The repository is **pre-implementation** — there is no source code yet, only `research.md` (the source-of-truth design) and `hardware.md` (the bill of materials). When you start writing code, follow the phased plan in `research.md` §17.

---

## 1. Project overview

Tether is a portable, half-duplex, PTT voice-and-text messenger:

1. **M5 (ESP32-S3 + SX1262)** captures audio on PTT press, encodes it with Opus, fragments it over LoRa.
2. **RAK4631 bridge (nRF52840 + SX1262)** forwards LoRa packets to the PC over USB-Serial.
3. **`tetherd` (Go daemon on the PC)** reassembles, decodes, runs **Parakeet-TDT** STT, and dispatches text to the right **Matrix room** or **Forge session**.
4. Replies stream back: text → **Piper** TTS → Opus → LoRa → M5 speaker.
5. Multiple **conversations** (Matrix rooms and Forge sessions) appear as channels on the M5 with persistent history on LittleFS.

Two sibling systems are core dependencies:
* **[`jbutlerdev/forge`](../forge)** — durable AI agent sessions. The base station is a forge HTTP client.
* **mautrix-go appservice** — Tether is a Matrix puppet.

---

## 2. Source of truth: read these first

* **[`research.md`](research.md)** — the complete design. 1,000+ lines, locked decisions, packet protocol, multi-conversation model, STT/TTS pipeline, phased implementation plan. **If something in your code contradicts `research.md`, the research wins** — update the research first, then the code.
* **[`hardware.md`](hardware.md)** — BOM. ESP32-S3 pin mappings must be verified against the ThinkNode M5 schematic before being baked into firmware.

Don't write any code that depends on a section of `research.md` you've skipped. If a section is unclear, ask.

---

## 3. Critical environment rules (read before doing anything)

### 3.1 The base station is a Go process — not a shell-out

* `tetherd` is a single Go binary that **in-process** uses cgo (sherpa-onnx) and **subprocess pipes** (piper). Do not shell out from bash for hot-path work; do it from Go.
* `sherpa-onnx` (Parakeet STT) is invoked via cgo — never via the CLI from a bash script. The CLI is only for testing.
* `piper` is invoked as a subprocess for v1; the binary path is in `tetherd.toml`. Do not invoke it with `nix shell` or `sudo`.

### 3.2 The M5 firmware is C++/FreeRTOS on ESP-IDF

* **No Arduino.** ESP-IDF directly, with the FreeRTOS task model described in `research.md` §7.
* All SPI activity (SD, SX1262, EPD) takes a single `spi_bus_mutex`. The LoRa ISR is flag-setter only — heavy SPI work happens in the radio task.
* Use `micro-opus` or `esp-libopus` for Opus encode.
* The PSRAM ring buffer is single-producer / single-consumer — no mutex, just a memory barrier.

### 3.3 The RAK4631 bridge firmware is C++ on PlatformIO + RadioLib

* Speaks a line-framed binary protocol over USB-Serial at 921 600 baud.
* No filesystem, no SD — pure pass-through. The Go side owns state.

### 3.4 Three buttons, no touchscreen

The M5 has exactly three physical buttons: **A = PTT**, **B = Next**, **C = Prev** (plus long-press combos for cancel and settings). The 1.54″ EPD is the only display. **Do not design any UX that requires more inputs** — there isn't room.

### 3.5 The base station is Linux-preferred, but cross-platform

* Linux is the primary target. PulseAudio null sink is the audio routing.
* Windows is supported via VB-Cable. macOS via BlackHole. Test all three if you touch the audio layer.

---

## 4. Repository layout (planned)

When code lands, this is the target layout. Don't create directories outside this without discussion.

```
tether/
├── README.md          # project overview + preview
├── AGENTS.md          # this file
├── hardware.md        # BOM
├── research.md        # source of truth
├── docs/
│   └── preview.png    # README preview image
│
├── proto/             # wire format definitions (shared Go + C++)
├── go/                # tetherd (Go daemon)
│   ├── cmd/tetherd/   # entry point
│   ├── internal/
│   │   ├── serial/    # RAK4631 bridge protocol
│   │   ├── radio/     # packet fragmentation + ACK state machine
│   │   ├── codec/     # Opus encode/decode wrappers
│   │   ├── stt/       # Parakeet via sherpa-onnx cgo
│   │   ├── tts/       # Piper subprocess pipe
│   │   ├── matrix/    # mautrix-go appservice
│   │   ├── forge/     # HTTP + SSE client
│   │   ├── audio/     # PulseAudio / VB-Cable sink
│   │   └── conv/      # conversation state machine
│   ├── pkg/protocol/  # wire format (shared with firmware)
│   └── tetherd.toml   # config
├── firmware/
│   ├── m5/            # ESP-IDF project for ThinkNode M5 (C++)
│   └── bridge/        # PlatformIO project for RAK4631 (C++)
└── scripts/           # provisioning, model fetch, OTA, etc.
```

When you create a new package or module, put it where it belongs in this tree. Don't invent new top-level directories without asking.

---

## 5. Build & test commands

These don't exist yet — they're the target. As code lands, the actual commands go here.

```bash
# Go daemon
cd go
go mod download
go build -o tetherd ./cmd/tetherd
go test ./...
go vet ./...

# M5 firmware (ESP-IDF)
cd firmware/m5
idf.py set-target esp32s3
idf.py build
idf.py -p /dev/ttyUSB0 flash monitor

# Bridge firmware (PlatformIO)
cd firmware/bridge
pio run
pio run -t upload

# Fetch models
./scripts/fetch-models.sh
```

---

## 6. Coding conventions

### Go (`go/`)

* Standard `gofmt`, `goimports`, `golangci-lint`.
* Errors wrap with `fmt.Errorf("...: %w", err)`. Use `errors.Is` / `errors.As`.
* Concurrency: goroutines + channels; explicit context propagation. No goroutine leaks — every goroutine must have a clear shutdown path.
* No global state. Config flows in via the constructor.
* Logging: `log/slog` (stdlib). Structured fields, not formatted strings.
* All public types/methods documented.

### C++ (firmware)

* Allman braces, 4-space indent, `clang-format` with the Espressif default.
* C++17. No exceptions in ISR context. No dynamic allocation in ISR.
* FreeRTOS primitives only for inter-task communication. No `std::thread`.
* Header guards `#pragma once`. Forward-declare where possible.
* ESP-IDF logging: `ESP_LOGI`, `ESP_LOGW`, `ESP_LOGE` with a per-module tag.

### Wire format

* `proto/` holds the canonical definitions (likely `protobuf` or plain `.h` + `.go`).
* The same definition is generated into both Go and C++. **Never hand-write a struct that mirrors a proto field** — generate it.
* Wire format is **little-endian**, **CRC-16/CCITT** for header checksums.

---

## 7. Common tasks

### 7.1 Adding a new conversation kind

1. Update the `Conversation` struct in `research.md` §9 and the Go equivalent in `go/internal/conv/`.
2. Add the mapping in `go/internal/matrix/` or `go/internal/forge/`.
3. Update the M5 EPD screens in `firmware/m5/ui/` to show the new kind.
4. Add a test vector to `proto/testdata/`.

### 7.2 Changing the LoRa preset

Don't, unless you have a measurement to justify it. The current preset (SF11/BW125/CR 4/8) was chosen for a reason — see `research.md` §6.1. If you change it, document the airtime impact in `research.md` and verify the link budget math.

### 7.3 Adding a new TTS voice

1. Drop the `.onnx` + `.onnx.json` into `/var/lib/tether/piper-voices/`.
2. Update `tetherd.toml` to point at it.
3. Re-run the TTS loopback test (`research.md` §16 phase 3).

### 7.4 Touching the SPI mutex pattern

The pattern in `research.md` §7.4 is **load-bearing**. The LoRa ISR is flag-setter only because any SPI work in the ISR will deadlock with an in-flight SD write. If you need more state from the radio in the ISR, add it to the deferred task — don't put it in the handler.

### 7.5 Touching the protocol

If you change a packet field, version it. Bump `protocol_version` in the header (currently not present — add it). The M5 firmware and the base station must agree on the version, or you get garbage.

---

## 8. Testing

* **Go:** table-driven tests, `testify/assert`. Loopback tests in `go/internal/serial/` are the fastest feedback loop.
* **M5:** `idf.py monitor` for serial logs. Real hardware tests on a desk before claiming "works".
* **Bridge:** `pio test` for unit, real bench test for integration.
* **End-to-end:** follow the 13-phase test plan in `research.md` §16.

Tests live next to the code they test (`foo.go` + `foo_test.go`). No separate `tests/` directory.

---

## 9. Things that have bitten us / will bite you

* **Don't compile sherpa-onnx from source on the base station.** Use the prebuilt release tarball. Compilation takes 30+ minutes and rarely works the first time.
* **Don't use `sudo` inside the M5's NVS scripts.** The M5 has no privilege model.
* **The SX1262 BUSY pin must be polled before every SPI transaction.** RadioLib handles this, but if you bypass RadioLib, you'll hang the bus.
* **LittleFS on SD card is slow on first mount.** First boot after flashing can take 2–3 seconds to mount. Don't put it in the critical path.
* **Piper subprocess pipe can stall if you don't drain it.** Read in a goroutine; block-write is fine.
* **mautrix-go appservice registrations are persistent on the homeserver.** Removing a registration requires a manual homeserver-side delete. Test in a throwaway homeserver.
* **Forge sessions idle out at 30 minutes.** A "session expired" indicator on the M5 is in `research.md` §12.7 — don't drop messages silently.
* **Parakeet-TDT 0.6B v2 is non-streaming in ONNX.** The full clip must arrive before STT begins. This is OK for store-and-forward but is the reason forge text deltas are buffered until sentence boundary.
* **EPD partial refreshes leave ghosting.** After ~50 partials, do a full refresh. The UI state machine in `research.md` §9.3 handles this.

---

## 10. When in doubt

* Read `research.md`. Most "what should this do?" questions are answered there.
* Read `hardware.md`. Most "what's the M5 capable of?" questions are answered there.
* Look at how **`jbutlerdev/forge`** structures things. We follow the same conventions (workspace layout, AGENTS.md style, doc-as-source-of-truth).
* If still unclear, **ask the human** — the project is in pre-implementation, and locking down questions now is cheap; locking them down after code lands is expensive.

---

## 11. Related projects

* **[`../forge`](../forge)** — sibling project. Rust + pi-mono + PostgreSQL. Tether's forge client lives here.
* **[`../pi-mono`](../pi-mono)** — the agent runtime forge wraps. Tether doesn't depend on it directly, but understanding its event stream helps with the forge SSE integration.
* **mautrix-go** — Matrix framework. The appservice pattern is documented in their [docs](https://docs.mau.fi/bridges/general/registering-appservices.html).
* **sherpa-onnx** — Parakeet runtime. The [NeMo transducer models page](https://k2-fsa.github.io/sherpa/onnx/pretrained_models/offline-transducer/nemo-transducer-models.html) is the canonical reference.
* **piper1-gpl** — TTS. Subprocess pipe for v1.
* **RadioLib** — LoRa driver. Used on both M5 and bridge.
