# Tether — Install & Quickstart

This document walks you from a fresh checkout to a working Tether
deployment: the **ThinkNode M5** on the radio, the **RAK4631 bridge**
over USB, and the **`tetherd` base-station daemon** on a Linux / macOS /
Windows PC, hooked up to **Matrix** and **Forge**.

> **v0.1.0 status.** All the building blocks are merged to `main` and
> pass CI. The end-to-end `tetherd` binary is **not yet a single
> buildable command** in v0.1.0 — it's the work for v0.2.0 (see
> [§9](#9-whats-not-in-v010)). Until then, you have three
> production-ready end-to-end tools to validate each subsystem, and
> the per-package libraries are usable from your own code:
> `tether-loopback` (data plane), `tether-voice-test` (STT/TTS), and
> `tether-matrix-test` (Matrix). This document tells you how to run
> all of them.

---

## Contents

1. [What you need](#1-what-you-need)
2. [Quickstart (10 minutes)](#2-quickstart-10-minutes)
3. [Hardware setup](#3-hardware-setup)
4. [Building the M5 firmware](#4-building-the-m5-firmware)
5. [Building the bridge firmware](#5-building-the-bridge-firmware)
6. [Building the base-station binaries](#6-building-the-base-station-binaries)
7. [Configuring Matrix](#7-configuring-matrix)
8. [Configuring Forge](#8-configuring-forge)
9. [What's not in v0.1.0](#9-whats-not-in-v010)
10. [End-to-end tests](#10-end-to-end-tests)
11. [Troubleshooting](#11-troubleshooting)
12. [Next steps](#12-next-steps)

---

## 1. What you need

### Hardware

| Item | Notes |
|---|---|
| **ThinkNode M5** | ESP32-S3 + Semtech SX1262 + 1.54″ EPD + 3 buttons. The stock M5 ships with the firmware you'll replace. |
| **RAK4631** (or compatible) | nRF52840 + SX1262 core module. Must expose a USB-Serial port (the RAK4631 does natively). |
| **USB-C cables** | One for each board. |
| **915 MHz antennas** | One per board. **Don't power the SX1262 without an antenna attached.** |
| **PC** | Linux (primary), macOS, or Windows. |

### Per-OS prerequisites

The Tether project ships its own `go.mod` and ignores any parent
`go.work` (`GOWORK=off`). All tools below are installable in user
space; no `sudo` is required for the build steps.

| Tool | Linux | macOS | Windows |
|---|---|---|---|
| **Git** | distro package | `brew install git` | <https://git-scm.com> |
| **Go 1.25+** | <https://go.dev/dl> | `brew install go@1.25` | <https://go.dev/dl> |
| **ESP-IDF v5.2** | see [§4.1](#41-esp-idf-v52) | see [§4.1](#41-esp-idf-v52) | see [§4.1](#41-esp-idf-v52) |
| **PlatformIO Core 6.x** | `pipx install platformio` | `brew install platformio` | `pip install platformio` |
| **PulseAudio / VB-Cable / BlackHole** | `apt install pulseaudio` | `brew install blackhole-2ch` | VB-Cable installer |
| **`sherpa-onnx`** runtime | prebuilt tarball (see [§6.1](#61-parakeet-tdt-stt)) | same | same |
| **`piper`** binary | prebuilt release (see [§6.1](#61-parakeet-tdt-stt)) | same | same |

### Accounts

* **Matrix homeserver** — your own Synapse, or a public server
  (`matrix.org` is fine for testing). The `tether` user you'll create
  needs to be an **appservice** (see [§7](#7-configuring-matrix)).
* **Forge** — `https://forge.example.com` for a hosted instance, or
  self-host from [`jbutlerdev/forge`](../forge). You need an **API
  token** with `forge:write` scope.

---

## 2. Quickstart (10 minutes)

This gets you to "the data plane works in-process" without flashing
any hardware. It only needs Go 1.25+.

```bash
# Clone
git clone https://github.com/jbutlerdev/tether.git
cd tether

# Build the loopback harness
GOWORK=off go build -o tether-loopback ./go/tools/tether-loopback

# Run it
./tether-loopback
#   tether-loopback: ok sent=15 acked=15 received=15 in 1.03s
```

The tool spins up a `radio.Sender` and a `radio.Receiver` in one
process, sends a 1-second synthetic audio clip through the in-process
radio pair, and asserts a 1:1 sent / acked / received ratio. **This
is the fastest feedback loop in the project.** Use it to verify your
Go toolchain before touching any hardware.

> **Expected output:** a single summary line on stdout, exit code 0.
> Anything else, see [§11](#11-troubleshooting).

---

## 3. Hardware setup

### 3.1 ThinkNode M5

1. Attach the 915 MHz antenna **before** plugging in power.
2. Plug in a USB-C cable. The M5 enumerates as a serial device:
   - Linux: `/dev/ttyUSB0` or `/dev/ttyACM0`
   - macOS: `/dev/tty.usbmodem*`
   - Windows: `COM3` (or higher)
3. The 1.54″ EPD will show **"Tether ready"** once you've flashed the
   Tether firmware (see [§4](#4-building-the-m5-firmware)). Until
   then it shows whatever the stock firmware was running.
4. The M5 has **two physical buttons** (not three) and a third
   *control* which is a *switch* (slider) for the GPS module, not a
   button. The pin map is in
   [`firmware/m5/components/board/include/board.h`](firmware/m5/components/board/include/board.h).
   The Meshtastic variant.h for this board — the source of truth for
   the wiring — is at
   <https://raw.githubusercontent.com/meshtastic/firmware/refs/heads/develop/variants/esp32s3/ELECROW-ThinkNode-M5/variant.h>.
   - **A (front, large, GPIO 21) = PTT** — push to record, release
     to enqueue + transmit.
   - **B (side, GPIO 14) = Menu / cycle** — short press cycles to
     the next conversation; long-press enters the settings menu.
   - **GPS slider (GPIO 10, digital input)** — senses the GPS
     toggle position. Wired to a different physical pad than SD CS
     (GPIO 10) despite the meshtastic header's `GPS_SWITH=10`
     reusing the same GPIO number. See `board.h::kPinGpsSwitch`
     and `kPinSdCs` for the comment explaining this.

### 3.2 RAK4631 bridge

1. Attach the 915 MHz antenna **before** plugging in power.
2. Plug in a USB-C cable to the RAK4631. It enumerates as a serial
   device (same naming as above).
3. The bridge is pure pass-through — no display, no buttons. The blue
   LED on the nRF52840 blinks on every successfully received packet
   from the M5.

### 3.3 PC

Any reasonably modern machine works. The Parakeet-TDT STT model
sits in RAM at ~600 MB; the Piper TTS model sits in another ~60 MB.
On a headless server, route audio through a PulseAudio **null sink**
(default in `config/tetherd.toml.example`).

---

## 4. Building the M5 firmware

### 4.1 ESP-IDF v5.2

The M5 firmware targets **ESP-IDF v5.2**. The CI workflow uses the
official `espressif/idf:v5.2` Docker image; the manual install is
similar.

**Linux / macOS (manual install):**

```bash
# Dependencies (Ubuntu/Debian)
sudo apt-get install git wget flex bison gperf python3 python3-pip \
     python3-venv cmake ninja-build ccache libffi-dev libssl-dev \
     dfu-util libusb-1.0-0

# Get ESP-IDF
mkdir -p ~/esp
cd ~/esp
git clone --recursive --branch v5.2 https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3

# Source the env (do this in every shell you use for idf.py)
. ~/esp/esp-idf/export.sh
```

**Windows:** use the [ESP-IDF v5.2 offline installer](https://dl.espressif.com/dl/esp-idf/)
or WSL2. The WSL2 path is identical to the Linux path above.

### 4.2 Build

```bash
cd tether/firmware/m5
. ~/esp/esp-idf/export.sh     # ignore if you're using the Docker image
idf.py set-target esp32s3
idf.py build
```

Expected output: `tether-m5.bin` is produced under `build/`. The
binary is ~350 KB; the M5's app partition is 1 MB.

### 4.3 Configure the master key

The M5 needs the same 16-byte (32-hex-char) master key as the base
station. This is the per-deployment PSK that drives the HKDF
derivation of per-conversation AES-128-CTR keys.

```bash
# Generate a random 32-hex-char PSK (do this once per deployment)
python3 -c "import secrets; print(secrets.token_hex(16))"
# → e.g. 4f8c2e1a0b9d3f7e6a5c4b3d2e1f0a8c

# Burn it into the M5's NVS partition
cd tether/firmware/m5
. ~/esp/esp-idf/export.sh
python3 -m nvs_partition_gen generate nvs.bin nvs.csv 32768
# Where nvs.csv contains:
#   key,type,encoding,value
#   tether,namespace,,
#   master_key,data,hex2bin,4f8c2e1a0b9d3f7e6a5c4b3d2e1f0a8c
python3 -m esptool --chip esp32s3 -p /dev/ttyUSB0 \
    write_flash 0x9000 nvs.bin
```

Put the same value in the base station's `tetherd.toml` (see
[§6.3](#63-configuration)).

### 4.4 Flash

```bash
cd tether/firmware/m5
. ~/esp/esp-idf/export.sh
idf.py -p /dev/ttyUSB0 flash monitor
#   Press Ctrl-] to exit monitor.
```

What you should see on the EPD within ~3 seconds of boot:

```
   Tether ready
   v0.1.0
   [no conversations]
```

What you should see on the serial monitor:

```
I (412) tether:    tether-m5 v0.1.0 (commit 459f1e0)
I (412) tether:    sx1262: detected (version 0x12)
I (412) tether:    littlefs: mounted (4531 files)
I (412) tether:    conv_db: opened (0 conversations)
I (412) tether:    ptt: idle
```

If the EPD shows "**radio fault**" or the monitor shows "**sx1262:
not detected**", see [§11](#11-troubleshooting).

---

## 5. Building the bridge firmware

### 5.1 PlatformIO

```bash
# Linux / macOS
pipx install platformio
# or
python3 -m pip install --user platformio

# Verify
pio --version
#   PlatformIO Core, version 6.1.x
```

### 5.2 Build & flash

```bash
cd tether/firmware/bridge
pio run                          # build for the rak4631 env
pio run -t upload                # upload to the connected RAK4631
```

Expected output: `Connecting.........` followed by `Hash of data
verified.`, then a `Leaving...` and `Hard resetting via RTS pin...`.

### 5.3 Verify

The bridge is silent when idle. Open a serial monitor on the same
port (any baud rate; the bridge uses 921 600 internally but the
monitor doesn't decode the binary frames — you just want to confirm
the device is responsive). You should see CPU activity when the M5
transmits a packet.

---

## 6. Building the base-station binaries

### 6.1 Parakeet-TDT STT

```bash
# Tether ships a one-shot downloader. Set TETHER_MODELS to override
# the default /var/lib/tether destination.
TETHER_MODELS=$HOME/.local/share/tether ./scripts/fetch-models.sh
```

This pulls the **Parakeet-TDT 0.6B v2 int8** model (~600 MB) into
`$TETHER_MODELS/parakeet-tdt/`. **Do not compile sherpa-onnx from
source** — the runtime is shipped as a prebuilt C-API library and
sherpa-onnx is consumed via cgo.

### 6.2 Piper TTS

The v0.1.0 daemon talks to Piper as a subprocess, not a library. You
need the **`piper1-gpl`** binary on `$PATH` (or the path in
`tetherd.toml`).

```bash
# Linux x86_64
curl -fL https://github.com/OHF-Voice/piper1-gpl/releases/download/v1.2.0/piper_amd64.tar.gz | tar xz -C /usr/local
ln -s /usr/local/piper/piper /usr/local/bin/piper

# macOS (Apple Silicon)
curl -fL https://github.com/OHF-Voice/piper1-gpl/releases/download/v1.2.0/piper_arm64.tar.gz | tar xz -C /usr/local

# Windows
# → download piper_windows.zip, unzip, add to PATH
```

The `scripts/fetch-models.sh` script drops the default voice
(`en_US-lessac-medium`) into `$TETHER_MODELS/piper-voices/`. For
other voices, see [`docs/TTS-EVAL.md`](docs/TTS-EVAL.md).

### 6.3 Configuration

```bash
# Copy the example configs
sudo mkdir -p /etc/tether
sudo cp config/tetherd.toml.example        /etc/tether/tetherd.toml
sudo cp config/registration.yaml.example   /etc/tether/registration.yaml
sudo chmod 600 /etc/tether/registration.yaml   # contains secrets

# Edit /etc/tether/tetherd.toml and /etc/tether/registration.yaml
# and fill in your master_key_hex, Matrix homeserver, Forge API token.
$EDITOR /etc/tether/tetherd.toml
$EDITOR /etc/tether/registration.yaml
```

The two files are documented in their own headers. The Matrix
registration is filled in by hand (see [§7](#7-configuring-matrix));
the Forge token is issued by your forge admin.

### 6.4 Build

```bash
cd tether/go
GOWORK=off go build -o /usr/local/bin/tetherd ./cmd/tetherd
GOWORK=off go build -o /usr/local/bin/tether-forge ./cmd/tether-forge
GOWORK=off go build -o /usr/local/bin/tether-loopback ./tools/tether-loopback
GOWORK=off go build -o /usr/local/bin/tether-voice-test ./tools/tether-voice-test
GOWORK=off go build -o /usr/local/bin/tether-matrix-test ./tools/tether-matrix-test
```

> **Note:** `tetherd` is not yet a single binary in v0.1.0 — see
> [§9](#9-whats-not-in-v010). The other four tools are.

### 6.5 systemd (Linux)

```ini
# /etc/systemd/system/tetherd.service
[Unit]
Description=Tether base-station daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=tether
Group=tether
Environment=TETHER_CONFIG=/etc/tether/tetherd.toml
ExecStart=/usr/local/bin/tetherd
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/tether /var/log/tether
StateDirectory=tether
LogsDirectory=tether

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd --system --home /var/lib/tether --shell /usr/sbin/nologin tether
sudo chown -R tether:tether /var/lib/tether
sudo systemctl daemon-reload
sudo systemctl enable --now tetherd
sudo journalctl -u tetherd -f
```

---

## 7. Configuring Matrix

Tether is an **appservice** (sometimes called a "bridge"). It runs as
a long-lived HTTP listener that the homeserver pokes on every relevant
event. Concretely:

1. **Stand up a homeserver.** The simplest path for testing is the
   official Synapse Docker image:
   ```bash
   docker run -d --name synapse \
     -p 8008:8008 -p 8448:8448 \
     -v /var/lib/synapse:/data \
     -e SYNAPSE_SERVER_NAME=yourserver.tld \
     -e SYNAPSE_REPORT_STATS=no \
     matrixdotorg/synapse:latest generate
   docker run -d --name synapse --restart unless-stopped \
     -p 8008:8008 -p 8448:8448 \
     -v /var/lib/synapse:/data \
     matrixdotorg/synapse:latest
   ```
   For production, follow the [Synapse install
   guide](https://element-hq.github.io/synapse/latest/setup/installation.html).

2. **Edit `/etc/tether/registration.yaml`.** Replace
   `yourserver.tld` with your actual homeserver's server name.
   Generate strong `as_token` and `hs_token`:
   ```bash
   python3 -c "import secrets; print(secrets.token_urlsafe(48))"
   ```

3. **Register the appservice with the homeserver.** This is a
   one-shot, homeserver-side action that must be done by a server
   admin. Append the contents of `registration.yaml` to
   `/etc/tether/registration.yaml` on the homeserver host and add
   `app_service_config_files: ["/etc/tether/registration.yaml"]` to
   `homeserver.yaml`, then restart Synapse. (On managed homeservers
   like matrix.org, this isn't possible — use a self-hosted
   homeserver for Tether.)

4. **The puppet user.** After registration, the homeserver will
   accept events to/from `@tether:yourserver.tld`. The daemon
   dynamically creates `@tether_<user>:yourserver.tld` puppets on
   demand when a Matrix user is first mapped to a conversation.

> **Removing an appservice is a homeserver-side delete.** Tether
> doesn't have a deregistration API. If you change the registration,
> restart both Synapse and `tetherd`.

---

## 8. Configuring Forge

1. **Get an API token.** If you're self-hosting forge, follow
   [`jbutlerdev/forge`'s admin guide](../forge) to issue a
   `forge:write` token. If you're using a hosted instance, the forge
   admin issues it for you.

2. **Set the URL and token in `tetherd.toml`**:
   ```toml
   [forge]
   base_url = "https://forge.example.com"
   api_token = "forge_live_..."
   default_profile = "coder"
   ```

3. **Verify with the `tether-forge` CLI:**
   ```bash
   tether-forge list
   #   no forge sessions yet
   tether-forge create --profile coder
   #   created session: 0192f3b1-...  (use this ID in /tether)
   ```

---

## 9. What's not in v0.1.0

The 10 phase branches in [`plan.md`](plan.md) are all merged. What
ships in v0.1.0:

* ✅ M5 firmware (8-task FreeRTOS layout, EPD, PTT, conv DB, AES,
  Parakeet STT, Piper TTS, crash log, OTA-USB).
* ✅ RAK4631 bridge firmware (RadioLib + USB-Serial pass-through).
* ✅ Go data plane: 16 internal packages (`serial`, `radio`, `codec`,
  `conv`, `stt`, `tts`, `audio`, `matrix`, `forge`, `loopback`,
  `crypto`, `crashlog`, `nvs`, `power`, `ui`, `scripts_test`).
* ✅ Wire format + generated code in `go/pkg/protocol/`.
* ✅ Three end-to-end test tools (`tether-loopback`, `tether-voice-test`,
  `tether-matrix-test`).
* ✅ One operator CLI (`tether-forge list/create/rename/say/delete`).
* ✅ 8-job CI (Go test/lint, cpp-format, proto-verify, M5 + bridge
  build/test).
* ✅ **80% coverage gate** enforced on every PR.

What does **not** ship in v0.1.0 (planned for v0.2.0):

* 🚧 **`tetherd` main binary.** The daemon is implemented as
  per-package libraries and the three end-to-end test tools, but the
  unified "wire it all together" entry point is v0.2.0. To validate
  the system today, run the three test tools — they exercise
  everything except the persistent on-disk state.
* 🚧 **E2EE (encrypted Matrix rooms).** v0.1.0 reads encrypted
  messages as opaque envelopes; the `DecryptEvent` symbol exists as
  a v2 hook stub.
* 🚧 **LoRa OTA.** USB OTA works; LoRa-delivered image updates are
  v2.
* 🚧 **Frequency hopping.** A v2 hook stub is in place; the
  `research.md` §6.5 design is the spec.

For the v0.2.0 contract, see the `## v0.2.0` section at the bottom
of [`plan.md`](plan.md).

---

## 10. End-to-end tests

The three test tools are **the most important things to run after a
fresh install**. Each is a one-shot CLI: no daemon, no config files,
no homeserver.

### 10.1 `tether-loopback` — the data plane (10 s)

```bash
GOWORK=off go run ./go/tools/tether-loopback
#   tether-loopback: ok sent=15 acked=15 received=15 in 1.03s
```

Asserts a 1:1 sent/acked/received ratio through the in-process radio
pair. This validates Go toolchain, the protocol encoder, the ACK
state machine, and the reassembler. It does **not** touch the M5,
the bridge, or the USB-Serial port.

### 10.2 `tether-voice-test` — the STT/TTS pipeline (1 min)

```bash
GOWORK=off go run ./go/tools/tether-voice-test \
    -in scripts/testdata/hello.wav \
    -out /tmp/response.wav
#   tether-voice-test: ok (stt=ok "hello world" 1.23s  tts=ok /tmp/response.wav 1.43s)
```

Asserts that Parakeet-TDT correctly transcribes the WAV file and
Piper correctly synthesizes the response. This validates
`sherpa-onnx` (Parakeet), the `piper` subprocess, the audio sink,
and the codec stack. It does **not** touch Matrix, Forge, or LoRa.

### 10.3 `tether-matrix-test` — the Matrix integration (5 s)

```bash
GOWORK=off go run ./go/tools/tether-matrix-test
#   tether-matrix-test: ok (sent 1 message, received 1 echo, latency 24ms)
```

Asserts the appservice round-trips a message through a mock homeserver
to a mock sender and back. This validates the `mautrix-go` plumbing
and the conv-id mapping. It does **not** touch a real homeserver.

### 10.4 Coverage report

```bash
cd go
GOWORK=off go test -race -coverprofile=cover.out -covermode=atomic ./...
bash scripts/cover.sh cover.out 80
```

The 80% threshold is enforced in CI; locally you can override:

```bash
bash scripts/cover.sh cover.out 0     # 0 = no minimum
```

---

## 11. Troubleshooting

### 11.1 "tether-loopback: FAIL — sent=15 acked=14"

The receiver missed one ACK. This used to happen in early v0.1.0 due
to a race in `Sender.Run` (see `AGENTS.md` §9). If you see it on a
recent build, your CPU is too slow. Try `-count=1` to skip the cache.

### 11.2 "sx1262: not detected"

* The antenna is not attached, or there's a short. **Power down
  immediately.**
* Wrong SPI bus. Check `firmware/m5/components/spi_bus/` and
  `firmware/m5/sdkconfig.defaults` against the M5 schematic
  ([`hardware.md`](hardware.md)).
* The SX1262 BUSY pin is not connected. RadioLib handles this when
  used correctly; if you bypass RadioLib, you must poll BUSY before
  every SPI transaction.

### 11.3 "parakeet-tdt: model not found"

You didn't run `scripts/fetch-models.sh`, or it failed silently. Run
it with `-x`:

```bash
TETHER_MODELS=$HOME/.local/share/tether bash -x ./scripts/fetch-models.sh
```

The expected tree is:

```
~/.local/share/tether/
├── parakeet-tdt/
│   └── sherpa-onnx-nemo-transducer-parakeet-tdt-0.6b-v2-int8/
│       ├── model.int8.onnx
│       ├── tokens.txt
│       └── …
└── piper-voices/
    └── en_US-lessac-medium/
        ├── en_US-lessac-medium.onnx
        └── en_US-lessac-medium.onnx.json
```

### 11.4 "piper: subprocess hung"

Piper will deadlock if the daemon doesn't drain its stdout. The
v0.1.0 daemon reads Piper output in a dedicated goroutine; if
you've wired your own, **always read in a goroutine**. There is a
unit test in `go/internal/tts/` that simulates this — see
`TestPiper_ForceKillOnHang`.

### 11.5 "no audio output" on the M5

* **First boot after flashing** is 2–3 seconds slow while LittleFS
  mounts the SD. Wait. Don't power-cycle during that window.
* **i2s_amp not configured.** Check
  `firmware/m5/components/i2s_amp/src/i2s_amp.cpp` against the
  MAX98357A wiring. The default is GPIO 7/8/9 — see [`hardware.md`](hardware.md).

### 11.6 "matrix: appservice not registered"

The homeserver rejected the registration. Check:

1. `registration.yaml` is valid YAML. The `regex` fields must escape
   the dot in `yourserver\.tld`.
2. `homeserver.yaml` has `app_service_config_files` pointing at the
   absolute path of `registration.yaml`.
3. Both `as_token` and `hs_token` match what the daemon reads from
   its config.
4. The homeserver can reach the daemon's HTTP listener (default
   `http://127.0.0.1:8448`). If they're on different hosts, set up a
   reverse proxy.

### 11.7 ESP-IDF "i2s_std_slot_config_t has no member 'left_align'"

You're on the wrong ESP-IDF version. The M5 firmware requires
**v5.2**. v5.3+ removed `left_align` and `big_endian`; the
v0.1.0 M5 firmware has already been adjusted to use the v5.2 API
(`bit_shift=true` for left-align). Don't blindly upgrade.

### 11.8 "golangci-lint: failed to load config"

The `.golangci.yml` is in the **v2 schema**. If you're running
locally-installed `golangci-lint` and it's v1.x, it will refuse to
parse the file. Install v2.12.2:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

---

## 12. Next steps

* Read [`research.md`](research.md) — the locked design decisions,
  the protocol packet format, and the multi-conversation model.
* Read [`plan.md`](plan.md) — the phased TDD plan that produced
  v0.1.0 and the v0.2.0 contract.
* Read [`AGENTS.md`](AGENTS.md) — the operational guide for
  contributors: package responsibilities, CI matrix, coding
  conventions, and the gotchas learned during the v0.1.0 CI
  hardening pass.
* Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md),
  [`docs/CLI.md`](docs/CLI.md), and [`docs/TESTING.md`](docs/TESTING.md)
  for component-level detail.
* File an issue at <https://github.com/jbutlerdev/tether/issues>
  with a `[install]` prefix if this document is wrong or out of
  date.
