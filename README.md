# Tether

Asynchronous, half-duplex, push-to-talk (PTT) voice and text messenger that bridges a portable LoRa radio to a PC base station, and from there into **Matrix** rooms and **Forge** AI agent sessions.

<p align="center">
  <img src="docs/preview.png" alt="Tether — portable LoRa voice bridge into Matrix and Forge" width="700">
</p>

```
┌────────────────┐   LoRa (US915, SF11/BW125)   ┌─────────────────┐
│  ThinkNode M5  │ ◄─────────────────────────► │   RAK4631       │
│  (ESP32-S3 +   │   store-and-forward,        │   (nRF52840 +   │
│   SX1262)      │   Opus 16 kbps,             │   SX1262)       │
│  PTT, EPD,     │   per-chunk ACKs,           │   bridge fw     │
│  speaker+mic   │   AES-128-CTR               └────────┬────────┘
└────────────────┘                                       │ USB-Serial
                                                         ▼
                                              ┌─────────────────────┐
                                              │   tetherd (Go)      │
                                              │   • Parakeet-TDT STT│
                                              │   • Piper TTS       │
                                              │   • mautrix-go      │──── Matrix rooms
                                              │   • forge client    │──── Forge sessions
                                              │   • PulseAudio sink │
                                              │   • Bubbletea TUI   │
                                              └─────────────────────┘
```

Audio is captured on the M5, compressed with Opus @ 16 kbps, buffered in PSRAM, written to SD, fragmented over LoRa, reassembled on the PC, transcribed with **NVIDIA Parakeet-TDT**, and dispatched as text into the appropriate Matrix room or Forge session. Replies stream back the same way: text → Piper TTS → Opus → LoRa → speaker on the M5.

The system supports up to **16 simultaneous conversations** (Matrix rooms and/or Forge sessions), each appearing as a discrete "channel" on the M5 with its own scrollable history. Range is prioritized over speed (custom SF11/BW125/CR 4/8 preset). 2–5 km line-of-sight with stock antennas.

## Operator TUI

The base station runs a Bubbletea-powered TUI that surfaces the live
conversation list, RF link stats, model info, and battery state. The
keyboard bindings are `r` (replay last TTS), `m` (mute mic), and
`q` (quit).

```
┌─ Tether ─────────────────────────────────────────┐
│ Conversations (3 active, 2 unread)
│  ► Forge: build-fix   just now  ●2
│    Alice (Matrix)      14:28
│    Bob (Matrix)        13:55  ●1
├──────────────────────────────────────────────────┤
│ RF: SF11 BW125 SNR -8 dBm  TX 14mA
│ Models: parakeet-tdt 0.6b v2 (640 MB), piper amy
│ Quiescent: 12 mA   Battery: 3.92V  (78%)
├──────────────────────────────────────────────────┤
│ [r] Replay last  [m] Mute mic  [q] Quit
└──────────────────────────────────────────────────┘
```

See [`docs/CLI.md`](docs/CLI.md) for the full CLI reference.

## Status

**v0.1.0 shipped.** The 9-phase implementation plan in [`plan.md`](plan.md)
is complete; CI is green and `git tag -a v0.1.0` is cut. The system
runs end-to-end on the M5, the RAK4631 bridge, and a Linux / macOS
/ Windows base station. See [`CHANGELOG.md`](CHANGELOG.md) for the
release notes.

| Phase | Description | Status |
|---|---|---|
| 0 | Tooling & schemas | ✅ done |
| 1 | Go data plane (loopback) | ✅ done |
| 2 | RAK4631 bridge firmware | ✅ done |
| 3 | M5 FreeRTOS skeleton | ✅ done |
| 4 | EPD + multi-conversation | ✅ done |
| 5 | STT (Parakeet) + TTS (Piper) | ✅ done |
| 6 | Matrix appservice | ✅ done |
| 7 | Forge integration | ✅ done |
| 8 | Hardening (AES, watchdog, NVS, OTA, crash log) | ✅ done |
| 9 | Polish (TUI, docs, v2 hooks, release) | ✅ done |

## What this is NOT

* **Not a real-time voice radio.** Tether is store-and-forward at
  SF11/BW125: a 30-second voice message takes 3–6 minutes of airtime
  depending on fragmentation, and the listener does not hear the
  sender's audio in real time. If you want a push-button intercom,
  look at a ham repeater.

* **Not an IP radio.** LoRa is the transport. There is no IP
  addressing, no multicast, no in-band TCP. Every message is
  addressed to a single 16-byte conversation id and routed by the
  base station.

* **Not a mobile phone.** Tether has a 1.54″ EPD and three
  buttons. There is no touchscreen, no on-screen keyboard, no
  notification center. UX is deliberately minimal so the firmware
  fits in PSRAM and survives week-long field deployments.

* **Not a Matrix server.** Tether is a Matrix appservice *client* —
  it puppet-s a user via a real homeserver (Synapse, Dendrite, …).
  You need to bring your own homeserver.

* **Not a replacement for a proper field radio.** Range is 2–5 km
  line-of-sight with stock antennas. Hills, trees, and walls cut
  this dramatically. Tether is a "camp-radio-to-PC" link, not a
  long-haul HF system.

* **Not end-to-end encrypted (yet).** The link between the M5 and
  the base station is AES-128-CTR-encrypted with HKDF-derived
  per-conversation keys. The bridge firmware is a USB pass-through
  and does not terminate encryption. **Matrix / Forge replies
  are plaintext.** Megolm E2EE is on the v2 roadmap — see
  [`research.md`](research.md) §15 and the `// v2:` hooks in
  `internal/matrix/appservice.go`.

## Quick start

### Prerequisites

* **Linux** (preferred), macOS, or Windows 10+ for the base station.
* **Go 1.25+** (`go version`).
* **ESP-IDF v5.2+** for the M5 firmware (`idf.py --version`).
* **PlatformIO** for the RAK4631 bridge firmware (`pio --version`).
* **PulseAudio** on Linux for the audio sink. macOS users get
  BlackHole; Windows users get VB-Cable. The audio sink is
  optional — `tetherd -no-audio` skips it.

### Clone and build

```bash
git clone https://github.com/jbutlerdev/tether
cd tether

# Build the daemon and CLI tools.
cd go
go build -o tetherd      ./cmd/tetherd
go build -o tether-forge ./cmd/tether-forge
go build -o tether-loopback ./tools/tether-loopback
cd ..
```

### Configure

```bash
# Tetherd reads /etc/tether/tetherd.toml by default; for a
# single-user install, the example config in go/tetherd.toml
# works out of the box.
cp go/tetherd.toml go/tetherd.local.toml
$EDITOR go/tetherd.local.toml     # matrix + forge + serial settings
```

The minimum required fields are:

```toml
[serial]
port = "/dev/ttyACM0"             # RAK4631 USB-Serial

[matrix]
homeserver = "https://matrix.example.com"
appservice_id = "tether"
as_token = "..."
hs_token = "..."

[forge]
base_url = "https://forge.example.com"
api_key = "..."
```

### Provision models

```bash
# Pulls parakeet-tdt-0.6b-v2-int8 (640 MB) and a Piper voice.
./scripts/fetch-models.sh
```

### Run

```bash
./go/tetherd -config go/tetherd.local.toml
```

On a successful start the TUI appears. Press `q` to quit, `r` to
replay the last TTS, `m` to toggle the mic mute.

### Smoke tests

```bash
# 1-second radio loopback. Confirms the data plane.
./go/tether-loopback

# Voice pipeline. Transcribes a known WAV and re-synthesises it.
./go/tether-voice-test -in go/tools/tether-voice-test/testdata/hello_8k.wav \
                       -out /tmp/tether-voice-test-out.wav

# Matrix appservice. End-to-end with the in-memory mock.
./go/tether-matrix-test -v
```

### Build the firmware

```bash
# M5 firmware.
cd firmware/m5
idf.py set-target esp32s3
idf.py build
idf.py -p /dev/ttyUSB0 flash monitor

# RAK4631 bridge firmware.
cd firmware/bridge
pio run
pio run -t upload --upload-port /dev/ttyACM1
```

## Troubleshooting

### `tetherd` exits with `serial: open /dev/ttyACM0: permission denied`

You are not in the `dialout` group (Linux) or do not have USB-Serial
permissions. Fix:

```bash
sudo usermod -aG dialout $USER
newgrp dialout
# Or, on a single session:
sudo chmod a+rw /dev/ttyACM0
```

### `tetherd` exits with `matrix: as_token rejected`

The appservice registration on the homeserver is missing or has been
rotated. Re-register:

```bash
# From a Matrix admin account, run:
curl -X POST -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d @appservice-registration.yaml \
  https://matrix.example.com/_synapse/admin/v1/register_appservice
```

`appservice-registration.yaml` ships in the daemon's config dir; see
`docs/ARCHITECTURE.md` for the exact fields.

### Parakeet STT prints `model not found`

The model directory is empty or missing. Re-run `./scripts/fetch-models.sh`,
or download the model manually:

```bash
mkdir -p /var/lib/tether/parakeet-tdt-0.6b-v2-int8
cd /var/lib/tether/parakeet-tdt-0.6b-v2-int8
curl -fLO https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8.tar.bz2
tar -xjf sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8.tar.bz2
```

### Tether M5 boots into a boot loop

The watchdog detected a crash before user code started. Pull the
crash log off the SD card:

```bash
# Mount the SD card on a Linux host.
mount /dev/sdX1 /mnt/m5
cat /mnt/m5/crash.log
# The first line is the reset reason; subsequent lines are
# backtraces from the offending task.
```

The reset-reason code is documented in `firmware/m5/components/watchdog/`.
Most boot loops are caused by a corrupt NVS partition — the recovery
is to reflash with `idf.py erase_flash` followed by `idf.py flash`.

### `tether-loopback` reports `acked=N/M`

The loopback harness reports a per-packet loss rate above 5 % as a
failure. Common causes:

* **Stale `cov.out`** in the project root — run `git clean -fdx cov.out`.
* **Wrong SF preset** — `radio.go` pins SF11/BW125/CR 4/8; do not
  change it without re-running the airtime math in
  [`research.md`](research.md) §6.1.
* **Mismatched `protocol_version`** — both the daemon and the bridge
  must agree. `git status` should be clean before reporting a bug.

### TUI is garbled / shows box-drawing junk

The terminal is not UTF-8. Fix:

```bash
export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8
```

Windows Terminal and modern macOS Terminal.app are fine. Old
`cmd.exe` and `screen` on a serial console are not.

### CI fails with `coverage 79.8% is below the 80% gate`

You added a function without tests. Run `go test -coverprofile=cov.out
-covermode=atomic ./...` and inspect `cov.out` for uncovered lines.
Adding tests is the right fix; **do not delete code to raise
coverage**. See [`docs/TESTING.md`](docs/TESTING.md).

## Repository layout

```
tether/
├── README.md          # this file
├── CHANGELOG.md       # release notes (started at v0.1.0)
├── AGENTS.md          # working guide for AI agents
├── hardware.md        # bill of materials
├── research.md        # complete design (the source of truth — start here)
├── plan.md            # 9-phase TDD implementation plan
├── docs/
│   ├── preview.png    # README preview image
│   ├── CLI.md         # CLI reference
│   ├── ARCHITECTURE.md
│   ├── TESTING.md
│   ├── NVS.md
│   └── TTS-EVAL.md
├── proto/             # shared protocol definitions
├── scripts/           # provisioning, model fetch, OTA
├── go/                # tetherd (Go daemon) + CLI tools
│   ├── cmd/tetherd/
│   ├── cmd/tether-forge/
│   ├── tools/         # tether-loopback, tether-voice-test, tether-matrix-test
│   ├── internal/      # serial, radio, codec, stt, tts, matrix, forge, conv, ui, …
│   └── pkg/protocol/  # wire format (shared with firmware)
└── firmware/
    ├── m5/            # ESP-IDF project for ThinkNode M5 (C++)
    └── bridge/        # PlatformIO project for RAK4631 (C++)
```

## Development

* **Build everything:** `cd go && go build ./... && go test ./...`
* **Run Go tests:** `cd go && GOWORK=off go test -race ./...`
* **Coverage gate:** `cd go && GOWORK=off bash ../scripts/cover.sh`
* **Build M5 firmware:** `cd firmware/m5 && idf.py build`
* **Host tests for the M5 components:** `cd firmware/m5/test_host && cmake -S . -B build && cmake --build build && ctest --test-dir build --output-on-failure`
* **Build bridge firmware:** `cd firmware/bridge && pio run`
* **Bridge tests:** `cd firmware/bridge && pio test -e native`
* **Format C++:** `bash scripts/format-cpp.sh`
* **Lint Go:** `cd go && golangci-lint run --config go/.golangci.yml`

See [`AGENTS.md`](AGENTS.md) for the full working guide (conventions,
common tasks, gotchas) and [`plan.md`](plan.md) for the implementation
plan that this release closes out.

## License

MIT — see [`LICENSE`](LICENSE).

## Related

* **[jbutlerdev/forge](https://github.com/jbutlerdev/forge)** — durable AI agent sessions (Rust + pi-mono)
* **[mautrix-go](https://github.com/mautrix/go)** — Matrix framework used for the appservice
* **[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)** — STT runtime for Parakeet
* **[piper1-gpl](https://github.com/OHF-Voice/piper1-gpl)** — TTS runtime
* **[RadioLib](https://github.com/jgromes/RadioLib)** — LoRa driver for both M5 and bridge
