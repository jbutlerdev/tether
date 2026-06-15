# Tether — Architecture

This document is a top-level map of the Tether system. It points at
`research.md` for the design rationale and at `plan.md` for the
phased build-out; its job is to give a newcomer enough orientation
to navigate the codebase in 5 minutes.

## 1. System diagram

```
┌─────────────────────────┐                   ┌──────────────────────────┐
│   ThinkNode M5 (M5)     │   LoRa (US915)    │  RAK4631 bridge (nRF52) │
│  ESP32-S3 + SX1262      │ ─── 902.3 ────►   │   nRF52840 + SX1262      │
│  + 1.54″ EPD + 3 buttons│ ◄─── MHz ─────    │   USB-Serial 921 600 baud│
│  + mic + speaker        │                   │   line-framed binary     │
└─────────────────────────┘                   └────────────┬─────────────┘
        ▲ PTT press                                         │
        │ Opus 8 kHz 16 kbps                                │ /dev/ttyACM0
        │ fragmented, 227 B/chunk                           ▼
        │                                          ┌────────────────────┐
        │   text reply via TTS                    │   tetherd (Go)     │
        │ ◄───── TTS_DATA / TTS_END / ACK ─────── │  - loopback        │
        │                                          │  - matrix rooms    │
        │                                          │  - forge sessions  │
        │                                          │  - Parakeet STT    │
        │                                          │  - Piper TTS       │
        │                                          │  - PulseAudio sink │
        │                                          └─────────┬──────────┘
        │                                                    │
        │                                  ┌─────────────────┼────────────────┐
        │                                  ▼                 ▼                ▼
        │                            ┌──────────┐    ┌──────────┐    ┌──────────────┐
        │                            │ Matrix   │    │  Forge   │    │  PulseAudio  │
        │                            │ homesrv. │    │ sessions │    │  null sink   │
        │                            │ appsvc.  │    │ (HTTP)   │    │  (file WAV)  │
        │                            └──────────┘    └──────────┘    └──────────────┘
        │
        └─── voice + text ───┐
                             ▼
                       human user
```

* **M5** is the handheld: a ThinkNode M5 with ESP32-S3, Semtech
  SX1262 LoRa modem, 1.54″ E-Paper display, three physical buttons
  (A=PTT, B=Next, C=Prev), an I2S microphone, and an I2S amplifier
  driving a small speaker.
* **Bridge** is a RAK4631 (nRF52840 + SX1262). It receives LoRa
  packets and forwards them to the base station over USB-Serial at
  921 600 baud. It is stateless — no filesystem, no SD.
* **`tetherd`** is a Go daemon on a Linux (preferred) base station.
  It owns the long-lived state: conversation history, the ACK
  state machine, the Parakeet STT, the Piper TTS subprocess, the
  Matrix appservice client, and the Forge HTTP client.
* **Matrix and Forge** are the two destination kinds for outbound
  text. A single M5 channel can be a Matrix room or a Forge
  session; the M5 doesn't know the difference.

## 2. Component map

One line per component, in the order they appear in the build:

| Path                              | Purpose                                              |
|-----------------------------------|------------------------------------------------------|
| `go/cmd/tetherd/`                 | daemon entry point                                   |
| `go/internal/serial/`             | RAK4631 ↔ USB-Serial framing                         |
| `go/internal/radio/`              | LoRa packet fragmentation + ACK state machine        |
| `go/internal/codec/`              | Opus encode/decode wrappers                          |
| `go/internal/stt/`                | Parakeet-TDT STT (cgo → sherpa-onnx)                 |
| `go/internal/tts/`                | Piper TTS (subprocess pipe)                          |
| `go/internal/matrix/`             | mautrix-go appservice                                |
| `go/internal/forge/`              | HTTP + SSE client for forge sessions                 |
| `go/internal/audio/`              | PulseAudio / VB-Cable / BlackHole sink                |
| `go/internal/conv/`               | conversation state machine + LittleFS persistence    |
| `go/pkg/protocol/`                | wire format (proto schema + hand-written codec)      |
| `firmware/m5/`                    | ESP-IDF project for the ThinkNode M5                 |
| `firmware/bridge/`                | PlatformIO project for the RAK4631                   |
| `proto/tether.proto`              | the v1 wire format                                    |
| `scripts/`                        | build, format, fetch-models, CI helpers              |

## 3. Wire format

The LoRa link carries a single protobuf message — the `Envelope` —
plus a CRC over the marshaled bytes stored in the envelope's
`header_crc` field. See `proto/tether.proto` for the full schema
and `go/pkg/protocol/header.go` for the encode/decode helpers.

* **Fragmentation:** payloads larger than 227 B are split into
  chunks of 227 B; the envelope carries `seq_num` (0-based) and
  `total_seqs` (set on every chunk).
* **Control messages:** `START`/`END` bracket a multi-chunk
  transmission; `DATA` carries a chunk; `ACK` carries the
  cumulative 32-bit bitmap; `TTS_DATA`/`TTS_END` carry the
  inbound TTS stream (M5 speaker); `UI_UPDATE` pushes a new
  conversation to the M5.
* **CRC:** CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no
  reflect, no xorout). Reference vector: `"123456789"` → `0x29B1`.
* **Crypto:** the `encryption_key` field in `ConvInfo` is 16 B
  HKDF-derived; payload encryption is applied in Phase 8.

The design is locked in `research.md` §8.

## 4. State machines

* **M5 PTT flow:** `research.md` §10 — `IDLE → RECORDING →
  ENCODING → TRANSMITTING → WAIT_REPLY → PLAYING → IDLE`. The
  FreeRTOS task graph that runs this is in `research.md` §7.1.
* **Go sender:** `internal/radio/sender.go` (Phase 1) —
  `IDLE → TX(seq) → WAIT_ACK → (ack | timeout) → TX(next) | RETRY
  → DONE/FAILED`.
* **Go receiver:** `internal/radio/receiver.go` (Phase 1) —
  reassembly keyed by `(target_id, conversation_id, message_id)`,
  ACK emission driven by the cumulative bitmap.
* **Conversation:** `internal/conv/` (Phase 4) — `LIVE ↔ IDLE` per
  conversation; `LIVE` means a PTT press would route to that
  conversation.

## 5. Data flow

### 5.1 Voice in (PTT pressed on the M5)

```
mic → I2S → opus_encode (16 kbps, 8 kHz) → Fragment (≤227 B/chunk)
     → SX1262 TX → LoRa air → RAK4631 RX → USB-Serial → tetherd
     → reassemble → opus_decode → sherpa-onnx (Parakeet-TDT) → text
     → dispatch to Matrix room or Forge session
```

### 5.2 Text in (Matrix or Forge event)

```
Matrix appservice webhook / Forge SSE → conv.Store
     → Fragment → SX1262 TX (over the bridge)
     → M5 RX → reassemble → display on EPD
```

### 5.3 Voice out (TTS reply)

```
text → Piper TTS subprocess → opus chunks → Fragment
     → SX1262 TX (TTS_DATA) → M5 speaker via I2S
     → final TTS_END chunk tells the M5 to release the PTT LED
```

### 5.4 Text out (UI update)

```
new Matrix room joined / new Forge session opened
     → conv.Store add → UI_UPDATE Envelope → M5 displays new channel
```

## 6. Failure modes and recovery

| Failure                          | Detection                  | Recovery                                  |
|----------------------------------|----------------------------|-------------------------------------------|
| Lost LoRa chunk                  | sender retry / bitmap gap  | retransmit up to N times                  |
| Stuck conv (no ACK)              | sender timeout             | mark conv as degraded; UI shows "…"       |
| Forge session idle-out (30 min)  | SSE heartbeat missed       | UI shows "session expired" indicator     |
| Piper subprocess stalls          | pipe read timeout          | kill + restart; drop TTS chunk            |
| sherpa-onnx inference error      | cgo panic / err return     | fall back to "I didn't catch that" TTS    |
| LittleFS mount failure (M5)      | mount() returns err        | reformat; refuse to boot until fixed      |
| Matrix appservice de-registered  | appservice 401 on reg call | refuse to start; require re-registration |
| Bridge USB unplugged             | serial EOF                 | tetherd backs off, retries; bridge auto-re-enumerates |
| EPD ghosting                     | partial-refresh counter    | full refresh every 50 partials (per state machine) |

The 13-phase end-to-end test plan in `research.md` §16 is the
exhaustive enumeration. Phase 8 (hardening) writes the automated
fail-injection tests that exercise each row of this table.

## 7. References

* `README.md` — what Tether is, in 60 seconds
* `research.md` — the design, with rationale
* `plan.md` — the phased build plan
* `AGENTS.md` — environment rules, coding conventions
* `proto/tether.proto` — the wire format
* `docs/TESTING.md` — how to write and run tests
* `hardware.md` — the bill of materials
