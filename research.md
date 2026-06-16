# Research Report: Tether — Asynchronous LoRa Voice + Text Messenger
*Revised 2026-06-14. Companion to `hardware.md`. This is the complete design — every section is locked, no deferred decisions.*

---

## 1. Executive Summary

**Tether** is a portable, half-duplex, push-to-talk (PTT) voice-and-text messenger that bridges a handheld **ThinkNode M5** to a **PC base station** over a US915 LoRa link, and from there into **Matrix** rooms and **Forge** AI agent sessions.

Audio is captured on the M5, compressed with **Opus @ 16 kbps**, buffered in PSRAM, written to an SD card, fragmented into LoRa packets with per-chunk ACKs, reassembled on the PC, **transcribed with NVIDIA Parakeet-TDT**, and dispatched as text into the appropriate Matrix room or Forge session. Replies stream back over the same path in reverse: text is chunked, **synthesized with Piper TTS**, encoded back into Opus, fragmented, transmitted, reassembled, decoded, and played on the M5's speaker.

The system supports **multiple simultaneous conversations** (Matrix rooms + Forge sessions), each appearing as a discrete "channel" on the M5 with its own scrollable history. PTT half-duplex, store-and-forward, and the range-vs-latency trade-off favor range.

### 1.1 Locked decisions

| Topic | Decision |
|---|---|
| Region | **US915** (902–928 MHz, 64 × 125 kHz + 8 × 500 kHz channels) |
| Duty-cycle limit | **None** (US ISM, 100 % allowed) |
| Max EIRP | **+30 dBm** (FCC Part 15); PA runs at +20 dBm in v1 |
| Modem preset | **SF11 / BW125 / CR 4/8** (custom, range-prioritized — ~3 dB better than Meshtastic LONG_FAST) |
| Sync word | `0xF3` (private) |
| Default channel | Ch 0, 902.3 MHz (single channel for v1) |
| Voice codec (radio link) | **Opus @ 16 kbps**, 8 kHz mono, 20 ms frames, complexity 5 |
| STT model | **NVIDIA Parakeet-TDT 0.6B v2** (English) via `sherpa-onnx`, int8 ONNX |
| TTS engine | **Piper** (`OHF-Voice/piper1-gpl`), int8 ONNX, voice `en_US-amy-low` (configurable) |
| M5 firmware | **Custom C++/FreeRTOS** — replaces Meshtastic entirely |
| Bridge firmware | **Custom C++ on RAK4631** (nRF52840 + SX1262) — RadioLib |
| PC daemon | **Go** — `go.bug.st/serial`, `gopkg.in/hraban/opus.v2`, `maunium.net/go/mautrix` |
| PC virtual audio (mic in, TTS out) | PulseAudio null sink (Linux) / VB-Cable (Windows) |
| SD filesystem | **LittleFS** (power-loss resilient) |
| Link-layer crypto | **AES-128-CTR** via SX1262 hardware engine, per-conversation key |
| End-to-end crypto (Matrix leg) | **Megolm** via mautrix-go (matrix-native E2EE) |
| PTT UX | **Two** physical buttons (A=PTT, B=Menu/cycle) + a GPS slider (not a button); EPD displays current conversation + state |
| Max message length | **60 s** recorded audio, 30 s TTS playback chunk, 5000 chars TTS input |
| Multi-conversation | Up to 16 active conversations on the M5, full history persisted |
| PC GUI | Skip for v1; bubbletea TUI for operator control |

### 1.2 External integrations

| System | Role | Mapped to |
|---|---|---|
| **Matrix** (`maunium.net/go/mautrix` appservice) | Bidirectional text messaging. Tether appears as a puppeted user in each Matrix room. | Each Matrix room = one Tether conversation. |
| **mautrix-gmessages** (Google Messages bridge) | Reused as a reference architecture for the appservice pattern; not bundled. | Pattern source only. |
| **Forge** (`../forge`, Rust + PostgreSQL + pi-mono) | Durable AI agent sessions. Each forge session = one Tether conversation. Voice → STT → `POST /messages`; forge SSE events → TTS → voice. | Each forge session = one Tether conversation. |
| **sherpa-onnx** | Parakeet-TDT ONNX runtime (STT). | In-process from Go via cgo. |
| **piper1-gpl** | Piper ONNX TTS runtime. | Subprocess pipe to Go. |

---

## 2. Hardware Context

See `hardware.md` for the BOM. Key facts that drive the design:

* **ThinkNode M5** — ESP32-S3 (Xtensa LX7 dual-core @ 240 MHz, 512 KB SRAM, **8 MB PSRAM**), 1.54″ EPD, 1200 mAh Li-Po, GPS, **onboard SX1262**, **2 physical buttons** (A=PTT GPIO 21, B=Menu/cycle GPIO 14) + a **GPS slider** on GPIO 10 (digital input, not a button), **I2S0 mic on GPIO 35/36/37** (right edge), **I2S1 amp on GPIO 47/48 (right) and 18 (left)** in split config, **microSD slot on the SPI bus** (CS=GPIO 10, same GPIO number as the GPS slider but a different physical pad).
* **SX1262** uses a **single BUSY pin + one multi-purpose IRQ line**, not the DIO0–DIO5 model of the older SX1276.
* The M5's onboard LoRa chip and the SD card share one HSPI bus — arbitration pattern in §7.4.
* **Bridge** = RAK4631 core (nRF52840 + SX1262). Re-flashed with custom firmware speaking our line-framed binary protocol over USB-Serial at 921 600 baud.
* **Base station** = a PC (Linux preferred) running the Go daemon. Should have a real CPU and ideally a GPU; CPU-only is fine for v1 at the cost of higher STT latency.

### 2.1 M5 physical controls (2 buttons + 1 GPS slider)

The ELECROW ThinkNode M5 has **two** physical buttons, not three.
The third "control" on the case is a *switch* (slider) for the GPS
module, not a button. This is fixed by the board's hardware — see
the [Meshtastic variant.h](https://raw.githubusercontent.com/meshtastic/firmware/refs/heads/develop/variants/esp32s3/ELECROW-ThinkNode-M5/variant.h)
which defines exactly `PIN_BUTTON1=21` and `PIN_BUTTON2=14`. The
"3-button" model that earlier drafts of this document assumed is
incorrect.

| Control | Function | Wiring |
|---|---|---|
| **A** (front, large) | **PTT** — push to record, release to enqueue + transmit | GPIO 21, IRQ, debounced |
| **B** (side) | **Menu / cycle** — short press advances to the next conversation; long-press (2 s) enters the settings menu. Inside the settings menu, kPtt acts as "decrease / go back" (the v0.1.0 design used a third "Prev" button that does not exist on this hardware). | GPIO 14, IRQ, debounced |
| **GPS slider** (case) | Toggles the L76K GPS module on/off. **Not a button.** Wired to GPIO 10 (digital input); see §2.2 for the GPIO-10 collision with SD_CS. Tether does not use the GPS, but the slider is sensed at boot to log a "GPS off" line in `tether-m5` serial output. | GPIO 10, digital input |

### 2.2 M5 I/O mapping (verified against the ELECROW schematic)

The pin numbers below are the **authoritative map for Tether v0.1.0**;
they're encoded in `firmware/m5/components/board/include/board.h`
and are cross-checked against the Meshtastic variant.h. Earlier
drafts of this document had `(board-defined, free GPIO)` for the
I2S and button lines; v0.1.0 fills in concrete numbers. The
system architect chose the I2S pins after surveying the right-edge
pads for free runs of three.

| Peripheral | ESP32-S3 GPIO | Notes |
|---|---|---|
| SX1262 BUSY | 5 | Do not use as GPIO |
| SX1262 IRQ (DIO1) | 4 | Edge-triggered ISR, flag-setter only |
| SX1262 CS | 17 | SPI CS |
| SX1262 RESET | 6 | Pull high at boot |
| SX1262 POWER_EN | 46 | High to enable |
| SPI SCK / MOSI / MISO | 16 / 15 / 7 | Shared bus (LoRa, EPD, SD) |
| SD CS | 10 | Separate SPI CS. **Same GPIO number as the GPS slider** but a *different physical pad* per the M5 schematic — see `board.h::kPinSdCs` and `kPinGpsSwitch` for the comment. |
| EPD CS / DC / RST | 39 / 40 / 41 | SPI + GPIO |
| EPD BUSY | 42 | Input, polled |
| EPD SCLK / MOSI | 38 / 45 | Shared SPI bus |
| I2S0 (INMP441) WS / BCLK / DIN | 35 / 36 / 37 | All on the right edge, sequential. Architect's choice. |
| I2S1 (MAX98357A) WS / BCLK / DOUT | 47 / 48 / 18 | Split config: WS+BCLK on right edge (47, 48), DOUT on left (18). GPIO 47/48 are the meshtastic `Wire1` SDA/SCL pads but Tether does not use the PCA9557, so they are free. |
| Button A (PTT) | 21 | Pull-up, IRQ on press |
| Button B (Menu) | 14 | Pull-up, IRQ on press |
| GPS slider | 10 | Digital input. See SD CS note. |
| GPS L76K UART | 19 (RX) / 20 (TX) | 9600 baud, not used in v1 |
| Battery ADC | 8 (channel 7) | For low-battery detection |
| VBUS detect | 12 | High when USB-C is plugged |
| Buzzer | 9 | PWM, active high |
| UART1 (RAK4631 bridge) | 43 (TX) / 44 (RX) | 921 600 baud |

---

## 3. Audio Pipeline (M5 → LoRa → Base Station)

### 3.1 Capture (M5)

| Stage | Setting | Why |
|---|---|---|
| Microphone | **INMP441** I2S MEMS | Hardware in `hardware.md` |
| Sample rate | **8 kHz** | Opus narrowband native; half the data of 16 kHz |
| Bit depth | **16-bit signed mono** | Standard for voice |
| I2S DMA | `dma_buf_count = 4`, `dma_buf_len = 256` samples | 32 ms per buffer, 4 buffers = 128 ms total — absorbs an SD-write pause without dropping samples |
| Frames per buffer | 256 / 160 = **1.6 Opus frames** | Round up to 2 frames (40 ms) for clean alignment |

### 3.2 Opus encoder (M5, libopus)

Using **`esphome/micro-opus`** (or `kahrendt/esp-libopus`):

```
application        = OPUS_APPLICATION_VOIP
bitrate            = 16000
sampling rate      = 8000
frame size         = 20 ms  (160 samples)
complexity         = 5
vbr                = 1
vbr_constraint     = 0
force_channels     = 1
use_phase_inversion= 0
```

**Memory footprint on ESP32-S3:**
- ROM: ~80 KB (encoder)
- RAM for encoder state: ~30 KB
- Per-frame working memory: ~6 KB
- All fits in 8 MB PSRAM with the encoded output buffer.

**Bitrate math:**
- 16 kbps × 1 s = **2 KB/s**
- 60 s message (max) = **120 KB** Opus
- With 16-byte app header on a 255-byte FIFO → ~239-byte payload → **~500 packets** per max-length message.

### 3.3 Decode path on PC

Go daemon uses `gopkg.in/hraban/opus.v2` to decode reassembled Opus to **PCM 8 kHz / 16-bit / mono** and stream to the virtual audio sink.

### 3.4 Airtime budget (concrete, distance-first)

At our preset (SF11/BW125/CR4/8) and a ~200-byte application payload:

| Message length | Opus data | Packets | Airtime (SF11/BW125) | Wall-clock at 100 % duty |
|---|---|---|---|---|
| 5 s | 10 KB | ~50 | ~38 s | **38 s** |
| 10 s | 20 KB | ~100 | ~75 s | **1 min 15 s** |
| 30 s | 60 KB | ~300 | ~3 min 45 s | **3 min 45 s** |
| 60 s | 120 KB | ~500 | ~6 min 15 s | **6 min 15 s** |

### 3.5 Link budget sanity check

* TX: +20 dBm, antenna +2 dBi, cable −1 dB
* Free-space path loss at 5 km / 915 MHz: ≈ 100 dB
* Sensitivity at SF11/BW125/CR4/8: **−127 dBm**
* Required SNR: −7.5 dB → **~10 dB fade margin at 5 km**
* **Realistic range: 2–5 km LOS, 1–3 km suburban** with stock antennas; **5–10 km LOS** with a base-station external antenna.

---

## 4. Speech-to-Text (Parakeet-TDT on the base station)

### 4.1 Model

**`nvidia/parakeet-tdt-0.6b-v2`** (English, 600 M params, TDT = Token-and-Duration Transducer). Converted to ONNX by `k2-fsa/sherpa-onnx`.

| Variant | Size | Notes |
|---|---|---|
| `sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8` | ~640 MB (encoder.int8) | int8 quantized, recommended for CPU |
| float32 version | ~2.4 GB | Higher accuracy, GPU-only |

We use **int8** for v1. Falls back to float32 only if WER is unacceptable.

### 4.2 Streaming model

**Parakeet-TDT v2 is non-streaming in the ONNX build.** The full clip must be available before inference begins. This is **acceptable for our store-and-forward design**: the M5 transmits the whole recorded clip, the PC reassembles it, then runs STT. Total latency from PTT-release to text-ready = airtime + STT time.

For streaming STT (per-chunk partial transcripts), v3 has a true streaming model; we revisit in v2.

### 4.3 Runtime integration

`sherpa-onnx` exposes a C API. Go daemon uses **cgo bindings** (or shells out to the `sherpa-onnx` CLI for the prototype).

```go
// #cgo LDFLAGS: -lsherpa-onnx-c-api
// #include <sherpa-onnx/c-api.h>
import "C"

func transcribe(pcm []float32) (string, error) {
    // ... call C API ...
}
```

### 4.4 Performance (CPU only)

* Modern x86 CPU (Ryzen / Core i5 or better): **~0.3–0.5× realtime** for 16 kHz audio; a 10 s clip transcribes in 3–5 s.
* With CUDA GPU: ~0.05× realtime.
* Memory: ~1.5 GB peak RSS for int8 inference.

### 4.5 Pipeline

```
Opus reassembled on PC
    → decode to PCM (8 kHz / 16-bit / mono)
    → upsample to 16 kHz (Parakeet expects 16 kHz)
    → run Parakeet-TDT int8 inference
    → text (with optional punctuation/casing — both built in)
    → dispatch to Matrix room or Forge session
```

---

## 5. Text-to-Speech (Piper on the base station)

### 5.1 Engine

**Piper** (`OHF-Voice/piper1-gpl`, GPL fork of rhasspy/piper). Fast local neural TTS. ONNX int8 voices ~15–60 MB each. We ship with `en_US-amy-low` for v1; voice is operator-configurable.

### 5.2 Voices

| Voice | Size | Quality | Sample rate |
|---|---|---|---|
| `en_US-amy-low` | 15 MB | OK, fast | 16 kHz |
| `en_US-amy-medium` | 60 MB | Better | 22 kHz |
| `en_US-libritts-high` | 120 MB | Best | 22 kHz |

Voice is per-deployment, set in `tetherd.toml`. We default to `medium` (60 MB) — quality matters for intelligibility over a noisy radio.

### 5.3 Streaming TTS via sentence chunking

Piper itself is not streaming. We synthesize the **whole reply at once** and stream the result to the M5 as it's being produced, but for perceived liveness we **chunk by sentence boundary**:

* Subscribe to Forge SSE `text_delta` events.
* Accumulate text. On `.` `?` `!` or after N=80 chars, flush the buffer to Piper.
* Encode Piper's PCM output as Opus, fragment, transmit.
* M5 plays the chunk while the next chunk is being synthesized and transmitted.

This gives perceived "first-audio-in-1–2 s" latency even for a 30 s reply.

### 5.4 Pipeline

```
Incoming text (Matrix event or forge SSE delta)
    → accumulate until sentence boundary
    → Piper synthesize → 16/22 kHz mono PCM
    → resample to 8 kHz
    → Opus encode @ 16 kbps
    → fragment, transmit as "TTS chunk" packets
    → M5 reassemble, decode, queue for speaker
```

### 5.5 Subprocess vs cgo

Piper ships a CLI. We invoke it as a subprocess for v1 (simpler, no cgo friction) and stream stdout PCM into a Go pipe. Migration to cgo in v2 if profiling shows pipe overhead matters.

---

## 6. LoRa Link Configuration (US915, distance-first)

### 6.1 Modem preset

| Preset | SF | BW | CR | Sensitivity | Time-on-air (200 B) | Range rank |
|---|---|---|---|---|---|---|
| SHORT_TURBO | 7 | 500 kHz | 4/5 | −108 dBm | ~25 ms | worst |
| SHORT_FAST | 7 | 250 kHz | 4/5 | −112 dBm | ~50 ms | |
| MEDIUM_FAST | 9 | 250 kHz | 4/5 | −119 dBm | ~150 ms | |
| LONG_FAST (Meshtastic US default) | 11 | 250 kHz | 4/8 | −124 dBm | ~400 ms | |
| **OUR PRESET** | **11** | **125 kHz** | **4/8** | **−127 dBm** | **~750 ms** | **best in class without SF12** |
| (extreme) | 12 | 125 kHz | 4/8 | −130 dBm | ~1.5 s | 2× airtime, +3 dB |

We pick **SF11 / BW125 / CR 4/8**. SF12 buys ~3 dB more at 2× airtime; not worth it.

### 6.2 US915 specifics

* **64 uplink channels** (ch 0–63, 902.3–914.9 MHz, 125 kHz) + **8 downlink** (ch 64–71, 500 kHz)
* **Default channel: ch 0 (902.3 MHz)** — single fixed channel for v1
* **Sync word: `0xF3`** (private network) — both ends match
* **Max TX power: 30 dBm EIRP**; PA runs at +20 dBm in v1 (SX1262 internal PA limit, no external front-end)
* **No frequency hopping** in v1; can add later for interference mitigation
* **No duty-cycle limit** (US ISM, 100 %)

### 6.3 Channel Activity Detection (CAD)

Before every TX, the radio task runs SX1262 hardware CAD:
- Listen for ~5 symbols (~80 ms at SF11/BW125)
- If channel busy, random backoff 100–500 ms, retry up to 5 times
- US915 has no duty-cycle constraint; backoff is purely for coexistence
- Implemented via `RadioLib::SX1262::startCAD()`

### 6.4 Link-layer encryption

* **AES-128-CTR** via SX1262 hardware engine (`radio.setEncryption(key, nonce)`)
* **Per-conversation key** (see §14) — each conversation has its own 16-byte key derived from the master PSK
* The bridge firmware and M5 firmware both enable encryption for a given `conversation_id`; the Go daemon trusts the bridge's already-decrypted plaintext

---

## 7. Firmware Architecture (M5, FreeRTOS, two-core)

### 7.1 Task layout

| Task | Core | Priority | Stack | Purpose |
|---|---|---|---|---|
| `hw_irq_dispatcher` | 0 | (ISR → task, max) | 1 KB | Defers LoRa SX1262 IRQ events; never touches SPI |
| `audio_capture` | 0 | High (23) | 4 KB | Drains I2S DMA, calls Opus encoder, pushes frames to PSRAM ring buffer |
| `radio_control` | 1 | High (23) | 8 KB | LoRa state machine, fragmentation, ACK timer, retransmit, conversation routing |
| `storage_flush` | 0 | Med (15) | 4 KB | Drains PSRAM ring buffer to LittleFS on SD, takes SPI mutex |
| `ui_state` | 0 | Low (8) | 4 KB | Button debounce, EPD refresh, PTT state machine, beep tones |
| `conv_manager` | 0 | Med (16) | 4 KB | Reads/writes conversation metadata, scrolls EPD history |
| `watchdog_feeder` | 1 | Low (10) | 1 KB | Feeds all task watchdogs every 500 ms |
| `power_mgmt` | 0 | Idle (3) | 2 KB | Enters light/deep sleep when idle |

### 7.2 Core pin map

* **Core 0:** audio + storage + UI + power + conv_manager
* **Core 1:** radio + watchdog
* Rationale: audio path is deterministic on its own core, radio state machine is timing-sensitive on its own.

### 7.3 Synchronization primitives

| Resource | Primitive | Notes |
|---|---|---|
| **SPI bus (SD + LoRa + EPD)** | `spi_bus_mutex` | All bus activity takes this |
| **PSRAM ring buffer** (audio → radio) | SPSC, no mutex | Head/tail indices + memory barrier |
| **PSRAM ring buffer** (audio → storage) | SPSC, no mutex | Same |
| **EPD display** | `epd_mutex` | Avoid partial-refresh collisions |
| **LoRa state machine** | One task owns it; others send via FreeRTOS queue | Eliminates radio races |
| **Conversation DB (LittleFS)** | `conv_db_mutex` | Brief, only held for read/write of one entry |
| **Button events** | FreeRTOS queue from ISR to `ui_state` | ISR does only a `xQueueSendFromISR` |

### 7.4 SPI arbitration — concrete pattern

The M5's onboard SX1262, SD card, and EPD all share one HSPI bus. A LoRa IRQ firing mid-SD-write would crash the bus. Rules:

1. **Use three `spi_device_handle_t`** on the same bus, one per CS pin (SD_CS, LORA_CS, EPD_CS).
2. **All** bus activity takes `spi_bus_mutex` first.
3. **The SX1262 ISR is a flag-setter only.** The handler reads the 2-byte status register over SPI (with the mutex, ~50 µs) and signals the radio task. Heavier work happens in the radio task.
4. **I2S DMA** does not need the SPI mutex (different peripheral).
5. **Watchdog:** no task holds `spi_bus_mutex` more than 10 ms. If a write would block longer, yield.

### 7.5 ISR design (SX1262)

```c
void IRAM_ATTR lora_irq_handler(void *arg) {
    BaseType_t hp_woken = pdFALSE;
    xSemaphoreTakeFromISR(spi_bus_mutex, &hp_woken);
    uint16_t status = sx1262_read_irq_status();
    sx1262_clear_irq(status);
    xSemaphoreGiveFromISR(spi_bus_mutex, &hp_woken);
    xQueueSendFromISR(radio_event_queue, &status, &hp_woken);
    portYIELD_FROM_ISR(hp_woken);
}
```

### 7.6 Power states

| State | Current | Description |
|---|---|---|
| Active TX | ~150 mA | ESP32 + SX1262 transmitting |
| Active RX | ~50 mA | ESP32 + SX1262 listening |
| Encode only | ~80 mA | ESP32 encoding, LoRa idle |
| **Light sleep** | ~10 mA | CPU paused, LoRa sleep, RTC alive |
| **Deep sleep** | ~10 µA | RTC wake only; woken by PTT or timer |

A 60 s TX = ~15 mAh. **~80 messages per 1200 mAh charge** if we deep-sleep aggressively.

---

## 8. Packet Protocol & Fragmentation

### 8.1 Header structure (24 bytes)

```
Offset  Size  Field
0       2     target_id        // node address; 0xFFFF = broadcast
2       2     sender_id
4       16    conversation_id  // UUID; identifies Matrix room or Forge session
20      2     seq_num          // index of this chunk (0-based)
22      2     total_seqs       // total chunks
24      1     msg_type         // 0=DATA, 1=START, 2=END, 3=ACK, 4=TTS_DATA, 5=TTS_END, 6=UI_UPDATE
25      1     flags            // bit0=RETRANSMIT, bit1=LAST_TTS_CHUNK
26      1     audio_kind       // 0=mic_capture, 1=tts_inbound, 2=beep_tone
27      1     reserved
28      2     header_crc       // CRC-16/CCITT over bytes 0..27
```

Followed by up to 227 bytes of payload (255 FIFO − 16 MAC overhead − 24 app header).

**`conversation_id` is 16 bytes (UUID) so a single radio can participate in many Matrix rooms and forge sessions** without address-space collisions. The conversation_id is generated by the base station when a new Matrix room or forge session is created and pushed to the M5 via a UI_UPDATE message.

### 8.2 Message types

| Type | Direction | Purpose | ACK? |
|---|---|---|---|
| `START` | M5→PC or PC→M5 | Header info: codec, duration, conversation_id; receiver allocates buffer | Sent 3× |
| `DATA` | both | Opus audio chunk | Per-chunk ACK |
| `END` | both | Closes the message | Sent 2× |
| `ACK` | receiver→sender | Cumulative 32-bit bitmap ack | (it is the ack) |
| `TTS_DATA` | PC→M5 | Incoming TTS audio chunk (from Matrix/forge reply) | Per-chunk ACK |
| `TTS_END` | PC→M5 | Closes TTS stream | Sent 2× |
| `UI_UPDATE` | PC→M5 | Push new conversation, push text-only message, etc. | Sent 3× |

### 8.3 Message flow (M5 → PC, mic capture)

1. User presses PTT. M5 starts recording.
2. User releases PTT. M5 finishes recording, writes Opus blob to LittleFS under `/queue/pending/<msg_id>.opus`, where `<msg_id>` is monotonic.
3. M5's `radio_control` picks up the file from the queue.
4. M5 sends `START (type=1, audio_kind=0)` 3× (redundancy, no ACK).
5. M5 sends `DATA` chunks. Each waits up to 2 s for an ACK; max 5 retransmits.
6. M5 sends `END` 2×.
7. PC reassembles, decodes, runs STT, dispatches to conversation target.
8. PC may immediately send back a `TTS_DATA` stream if the conversation agent replies in real time (e.g., forge agent end-of-turn).

### 8.4 Message flow (PC → M5, TTS playback)

1. PC subscribes to Matrix events and forge SSE for the active `conversation_id`.
2. Text arrives. PC accumulates per sentence, synthesizes with Piper, encodes to Opus, fragments.
3. PC sends `TTS_DATA` chunks over LoRa.
4. M5 reassembles into a single Opus blob, decodes on the fly, writes PCM to an I2S DMA buffer, speaker plays.
5. PC sends `TTS_END`.
6. M5 flushes remaining DMA, stops speaker.

### 8.5 Reliability

* `DATA` and `TTS_DATA` packets are individually ACKed with a 32-bit cumulative bitmap (1 ACK covers 32 chunks).
* ACK timer: 2 s.
* Max 5 retransmits per packet.
* Failed message: M5 marks `failed` in LittleFS, shows "X" icon on EPD; user can manually re-send from history.
* Duplicate detection: receiver drops `seq_num <= already_received` for the same `conversation_id + msg_id`.
* `conversation_id + msg_id` is globally monotonic; never reused → replay-safe.

### 8.6 ACK format

```
Offset  Size  Field
0       16    conversation_id
16      4     msg_id
20      2     next_expected_seq  // seq of the next un-acked packet
22      2     ack_bitmap_lo      // 16 bits covering seqs [next_expected_seq .. next_expected_seq+15]
24      2     ack_bitmap_hi      // 16 bits covering [next_expected_seq+16 .. next_expected_seq+31]
26      2     crc16
```

---

## 9. Multi-Conversation Model

The M5 supports up to **16 active conversations** at once. Each is a UUID (`conversation_id`) and has metadata:

```rust
struct Conversation {
    id: Uuid,                  // 16 bytes
    name: String,              // "Alice (Matrix)" / "Forge: build-fix" (truncated to 24 chars for EPD)
    kind: ConvKind,            // MatrixRoom | ForgeSession | Broadcast
    target: String,            // Matrix room_id or forge session_id (for replay)
    encryption_key: [u8; 16],  // per-conversation AES-128 key
    last_activity: i64,        // epoch ms
    unread_count: u16,
    history: Vec<HistoryEntry>,// last 50 messages (rolling)
    // Persisted to LittleFS /conv/<uuid>/meta.bin
    // History: /conv/<uuid>/history.bin (LittleFS file, ring-buffered)
}
```

### 9.1 Conversation lifecycle

1. **Bootstrap:** base station starts; on first run, it registers a Matrix appservice user (`@tether:matrix.example.com`) and creates a "default" Matrix DM room between the appservice user and the operator. This becomes `conversation_id[0]`.
2. **Adding a Matrix room:** the operator invites the appservice user to any room, or sends `/tether add` in a room; the appservice creates a `Conversation` for that room, generates a `conversation_id`, and sends a `UI_UPDATE` packet to the M5 with the new conversation's metadata.
3. **Adding a Forge session:** operator runs `forge sessions create` and then `tether link <forge_session_id>`; the daemon creates a `Conversation` and pushes it.
4. **Removing:** `/tether remove` in Matrix, or `tether unlink <id>` on the CLI; daemon sends `UI_UPDATE` to remove; M5 deletes the `Conversation` and its history.
5. **Persistence:** M5 persists all active conversations and their history in LittleFS across reboots. On startup, the M5 sends a "sync request" to the base station to get any updates it missed while off.

### 9.2 On-radio history

* Each conversation keeps **last 50 messages** in a ring-buffered LittleFS file: `/conv/<uuid>/history.bin`.
* Format: append-only record stream, 1 record per message (sender, text, timestamp, msg_id, status).
* EPD "history" view: scrollable list of last 50 messages. The most recent 3 are shown on the idle screen as a preview.
* Old messages are kept on the base station's Postgres; the M5 only carries the rolling window.

### 9.3 Conversation display on EPD

**Idle screen:**
```
┌──────────────────────────────┐
│ ► Alice (Matrix)        ●3   │   ← current + unread badge
│   "see you at 5"             │   ← last inbound (preview, truncated)
├──────────────────────────────┤
│ [3] Alice        [ ] Forge   │   ← conversation tabs at bottom
│      2 14:32       4 14:28   │   ← per-tab preview
└──────────────────────────────┘
```

**Recording screen:**
```
┌──────────────────────────────┐
│ ●  REC    00:03              │   ← recording timer
│   Alice (Matrix)             │
│   release PTT to send        │
└──────────────────────────────┘
```

**Transmitting screen:**
```
┌──────────────────────────────┐
│ ↑  TX     00:38 / 01:15      │   ← progress
│   Alice (Matrix)             │
│   ACK 47/100                 │
└──────────────────────────────┘
```

**TTS playback screen:**
```
┌──────────────────────────────┐
│ ↓  PLAY  Forge: build-fix    │
│   "running cargo test now…"  │
│   ▓▓▓▓▓▓░░░░  00:08 / 00:14  │
└──────────────────────────────┘
```

**Settings mode (B held 2 s):**
```
┌──────────────────────────────┐
│ SETTINGS                     │
│   Channel:    902.3 MHz      │
│   Modem:      SF11/BW125     │
│   Volume:     ▓▓▓▓▓░░░ 60%   │
│   Addr:       0x4A1F         │
│   VBat:       3.92 V         │
└──────────────────────────────┘
```

### 9.4 Conversation selection

* **Button B (short):** next conversation
* **Button C (short):** prev conversation
* Wrap-around in both directions
* The selected conversation becomes the "active" one — PTT transmits into it, incoming messages from it are auto-played via TTS

---

## 10. PTT UX

### 10.1 PTT state machine

```
         ┌──────┐
         │ IDLE │◄────────────────────────────┐
         └──┬───┘                             │
            │ press A                         │
            ▼                                  │
       ┌──────────┐                           │
       │LISTENING │ (mic muted, speaker on)    │
       │          │                           │
       └────┬─────┘                           │
            │ B/C press                       │
            ▼                                  │
       ┌──────────┐                           │
       │ IDLE (   │   different conv selected │
       │  new)    │                           │
       └──────────┘                           │
                                              │
   press A          release A                 │
       │                │                     │
       ▼                ▼                     │
 ┌─────────┐  release  ┌─────────┐ ACK done  │
 │RECORDING├──────────►│ QUEUED  ├──────────►─┤
 │ (mic on,│           │  /TX    │           │
 │ spk off)│           │         │           │
 └─────────┘           └────┬────┘           │
       ▲                     │ ACK fail x5    │
       │ A held 3s           ▼                │
       │                ┌─────────┐           │
       └────────────────│ FAILED  │           │
                        └─────────┘           │
```

### 10.2 Audio feedback (beep tones)

| Event | Tone | Duration |
|---|---|---|
| PTT pressed, channel clear | 1 kHz, 80 ms | "Talk permit" |
| PTT pressed, channel busy | 200 Hz, 200 ms | "Busy" |
| PTT released, message queued | 1 kHz × 2, 60 ms each | "Roger" |
| Message fully ACKed | 1.5 kHz, 200 ms | "Confirmed" |
| Message failed | 400 Hz, 400 ms | "Error" |
| Conversation switched | 800 Hz, 30 ms | "Click" |
| Low battery warning | 1 kHz × 3, 100 ms each | "Battery" |

Generated by a small sine-wave generator on the I2S TX channel. Played simultaneously with the main I2S stream by mixing.

### 10.3 PTT + speaker ducking

* When PTT is held: mic is enabled, speaker is muted (to avoid feedback)
* When TTS is playing: mic is muted, speaker is enabled
* When idle: both ready; speaker may play the last message on demand (button combo)

### 10.4 VOX (voice-activated PTT) — out of scope for v1

Could be added in v2: energy threshold on mic triggers PTT automatically. Disabled for v1 because it conflicts with TTS playback in the same room.

### 10.5 Cancel / abort

* **A held 3 s** during recording: discards the recording, returns to IDLE, no TX.
* **A held 3 s** during TX: marks message `canceled` on M5, sends a `TTS_END`-like signal to PC; PC drops the partial.

---

## 11. Matrix Appservice Integration

### 11.1 Architecture

We build a **mautrix-go appservice** that registers with a Matrix homeserver, claims the `@tether:*` namespace, and creates a "puppet" user per active Tether conversation.

```
Homeserver ─── appservice ─── tetherd (Go)
                │              │
                │              ├─ RAK4631 bridge (USB-Serial)
                │              ├─ sherpa-onnx (Parakeet STT)
                │              ├─ piper (TTS)
                │              └─ forge client (HTTP/SSE)
                │
                └── appservice user: @tether:matrix.example.com
                    puppets: one per active room (or shared single user, depending on privacy)
```

### 11.2 Registration

`appservice-registration.yaml`:
```yaml
id: tether
url: http://localhost:8089
as_token: "<random>"
hs_token: "<random>"
sender_localpart: tether
namespaces:
  users:
    - exclusive: true
      regex: "@tether_.*:matrix.example.com"
  rooms: []
rate_limited: false
```

### 11.3 mautrix-go usage

```go
import (
    "maunium.net/go/mautrix"
    "maunium.net/go/mautrix/appservice"
    "maunium.net/go/mautrix/id"
)

as := appservice.CreateClient(...)
client := as.Client()
// Send a message into a room
client.SendMessage(roomID, mautrix.EventMessage, &mevt.MessageEventContent{
    MsgType: mevt.MsgText,
    Body:    transcribedText,
})
// Listen for inbound
syncer := as.Syncer()
syncer.OnEventType(mevt.EventMessage, func(_ mautrix.Source, ev *mevt.Event) {
    if ev.Sender == as.UserID { return } // ignore our own
    text := ev.Content.AsMessage().Body
    conversation := findConvByRoomID(ev.RoomID)
    go enqueueTTS(conversation, text)
})
as.Start()
```

### 11.4 Mapping Matrix rooms to Tether conversations

| Matrix concept | Tether equivalent |
|---|---|
| `m.room_id` | `conversation_id` (UUID v5 derived from `m.room_id`) |
| Room name | `Conversation.name` (truncated) |
| Other participants | Ignored for v1 (Tether is a single user); Matrix displays the appservice user as the sender |
| Typing events | Ignored |
| Read receipts | Ignored |
| Reactions | Ignored (not in v1) |
| E2EE | Matrix native (Megolm) — mautrix-go handles this transparently if we enable it |

### 11.5 E2EE

For v1 we **do not enable E2EE** end-to-end (Tether audio is encrypted on the LoRa link with AES-128-CTR; the Matrix leg is plaintext, since the appservice sits on the homeserver and the operator trusts their own homeserver). E2EE is a v2 feature — mautrix-go supports it.

### 11.6 mautrix-gmessages compatibility

The mautrix-gmessages bridge (Google Messages) is **not bundled**, but its architecture is the reference pattern. If the operator wants to forward Tether text to Google Messages, they can run mautrix-gmessages as a second bridge; Tether's Matrix room would be the source for messages going out, and incoming Messages would appear in the same Matrix room (Tether doesn't directly integrate with mautrix-gmessages).

---

## 12. Forge Integration

### 12.1 Forge API surface (from `../forge`)

Tether uses these endpoints (and only these):

| Method | Path | Use |
|---|---|---|
| `POST` | `/auth/login` | API-key login at startup |
| `GET`  | `/sessions` | List existing forge sessions |
| `POST` | `/sessions` | Create a new forge session (becomes a Tether conversation) |
| `GET`  | `/sessions/:id` | Poll session metadata |
| `DELETE` | `/sessions/:id` | End a session |
| `POST` | `/messages` | Send transcribed text into a session (returns 202; agent runs in background) |
| `GET`  | `/messages?session_id=<uuid>` | Poll message history (fallback) |
| `GET`  | `/sessions/:id/events?since=<seq>` | **SSE stream** of new message rows + agent events (primary) |
| `GET`  | `/health` | Health check |

**Auth:** Tether's daemon stores a `FORGE_API_KEY` in its config; uses `X-API-Key: <key>` header on every call.

### 12.2 Per-session Tether conversation

* **One forge session = one Tether conversation.** When the operator runs `tether forge create [--profile coder]`, the daemon:
  1. `POST /sessions` → gets back a session UUID
  2. Creates a Tether `Conversation` with `kind = ForgeSession`, `conversation_id = UUIDv5(session_uuid)`, `name = "Forge: <profile>"`
  3. Pushes the new conversation to the M5 via `UI_UPDATE`
* The operator can later rename it: `tether forge rename <id> "build-fix"`.

### 12.3 Outbound: voice → forge

1. M5 sends a voice message to `conversation_id` (mapped to a forge session).
2. PC reassembles, decodes, runs STT.
3. Daemon `POST /messages` with the transcribed text → forge spawns / reuses `pi`, runs the agent.
4. Daemon subscribes to `GET /sessions/:id/events?since=<latest_seq>` (SSE).
5. As `text_delta` events arrive → buffer by sentence → Piper TTS → Opus → fragment → transmit to M5.
6. When `agent_end` arrives → final TTS chunk → TTS_END to M5.

### 12.4 Outbound: text (UI_UPDATE with embedded text)

The M5 cannot generate text directly in v1, but the operator can push a text-only message to a forge session from the CLI: `tether forge say <id> "stop the build"`. This creates a `UI_UPDATE` packet with the text (no audio). M5 displays it as a "from tether" message in history.

### 12.5 Inbound: forge → voice

Forge SSE event types we consume:

| Event | Action |
|---|---|
| `text_delta` | Buffer; flush on sentence boundary → TTS |
| `tool_call_start` | Add to "tool in progress" list; TTS prefix "running tool: <name>" |
| `tool_call_end` | Update tool status; if tool is `bash`, stream output as TTS |
| `agent_end` | Flush TTS buffer, send TTS_END to M5 |
| `error` | TTS error message "agent error" + log to M5 history |

### 12.6 Tool output TTS

For `bash` tools specifically, we **stream stdout/stderr lines** as they arrive, chunked into TTS-sized fragments. Example: agent runs `cargo test`, we pipe the test output to TTS, M5 hears "running 47 tests, 2 failed, cargo_test_main: assertion failed: ..." This is a powerful use case — operator hears agent progress in real time.

### 12.7 Idle timeout and resume

Forge kills idle sessions after 30 minutes (configurable). The M5's UI shows a "session idle" indicator. When the user next PTT's into the conversation, the daemon `POST /messages` triggers a resume (forge rebuilds the working tree from the message table and re-spawns `pi`). The first TTS reply may take 2–5 s longer than usual — M5 shows "thinking…" during that period.

---

## 13. PC Base Station Architecture

### 13.1 Process layout

```
tetherd (single Go process, single binary)
├── serial_reader  ── reads RAK4631 USB-Serial
├── radio_event_queue (forwarded to radio_control in firmware; on PC this is just an event log)
├── reassembler  ── per-conversation_id, per-msg_id, in-memory
├── opus_decoder ── hraban/opus
├── sherpa_engine ── Parakeet-TDT int8 (cgo to sherpa-onnx C API)
├── tts_engine  ── Piper subprocess pipe
├── matrix_appservice  ── maunium.net/go/mautrix appservice
├── forge_client  ── HTTP + SSE
├── conv_db  ── in-memory map + periodic checkpoint to disk
└── bubbletea_tui  ── operator UI (optional)
```

### 13.2 Per-conversation state machine

```
           incoming Tether START          all chunks acked
M5 mic ──────────────────────────►  [RECEIVING]  ──────────────► [STT_RUNNING]
                                       │                            │
                                       │ timeout/abort              ▼
                                       ▼                       [DISPATCHED]
                                  [ABORTED]                       │
                                                                  ▼
                                                          forge agent runs
                                                                  │
                                                  text_delta SSE  ▼
                                                          [AGENT_REPLY_STREAMING]
                                                                  │
                                                          agent_end
                                                                  ▼
                                                        [TTS_RUNNING]
                                                                  │
                                                          TTS chunks sent
                                                                  ▼
                                                          [DONE]
```

### 13.3 Go libraries

| Concern | Library |
|---|---|
| Serial port | `go.bug.st/serial` |
| Opus decode/encode | `gopkg.in/hraban/opus.v2` (cgo) |
| Matrix appservice | `maunium.net/go/mautrix` (appservice sub-package) |
| Forge HTTP | stdlib `net/http` |
| Forge SSE | `github.com/r3labs/sse/v2` |
| Piper TTS | subprocess pipe (CLI binary) |
| Parakeet STT | cgo to `sherpa-onnx-c-api` |
| WAV header | stdlib `encoding/binary` |
| Virtual audio | stdlib `os.Pipe` → PulseAudio FIFO / VB-Cable |
| Logging | stdlib `log/slog` |
| Operator TUI | `github.com/charmbracelet/bubbletea` |
| Config | `github.com/BurntSushi/toml` |

### 13.4 Virtual audio routing

| OS | Sink | How |
|---|---|---|
| **Linux** | PulseAudio null sink | `pactl load-module module-null-sink sink_name=tether_in`; daemon writes PCM to the `.monitor` source of `tether_in` |
| **Windows** | VB-Cable | Install free VB-Cable driver; daemon opens "CABLE Input" as output device |
| **macOS** | BlackHole | Install BlackHole 2-ch virtual audio driver |

The daemon writes **8 kHz / 16-bit / mono PCM** (post-Opus-decode) for both mic-captured audio (so it can be recorded by Audacity etc.) and TTS output (so any app can pick it up as a "voice input").

### 13.5 Configuration (`tetherd.toml`)

```toml
[serial]
port = "/dev/ttyACM0"
baud = 921600

[matrix]
homeserver = "https://matrix.example.com"
registration = "/etc/tether/appservice-registration.yaml"
bot_name = "Tether"

[forge]
api_url = "http://localhost:8080"
api_key = "..."          # FORGE_API_KEY

[stt]
model_dir = "/var/lib/tether/parakeet-tdt-0.6b-v2-int8"
threads = 4

[tts]
piper_binary = "/usr/local/bin/piper"
voice = "/var/lib/tether/piper-voices/en_US-amy-medium.onnx"
chunk_chars = 80         # sentence-boundary buffer size

[audio]
sink = "tether_in"       # PulseAudio null sink name (Linux)
sample_rate = 8000
channels = 1

[conversations]
max_active = 16
history_per_conv = 50
default_volume = 0.6
```

### 13.6 Why no Electron for v1

Adding Electron: Chromium runtime (~150 MB), node-serial bridge, WAV playback layer. None needed for the prototype. A bubbletea TUI gives message history, signal strength, conversation switcher for ~5 MB binary.

---

## 14. Security

### 14.1 Link-layer (LoRa)

* **AES-128-CTR** via SX1262 hardware engine
* **Per-conversation key** — derived from a master PSK via HKDF-SHA256: `conv_key = HKDF(master_psk, salt=conversation_id, info="tether-link-v1")`
* Master PSK provisioned out-of-band (printed on a QR card with each M5, or hard-coded for prototype)
* **Replay protection:** monotonic `(conversation_id, msg_id)` pair; receiver drops any `msg_id <= last_seen_for_conv`

### 14.2 Forge leg

* Tether daemon authenticates to forge with an `X-API-Key` header
* All traffic is HTTP (not HTTPS in v1 — assumes localhost or private network; TLS termination is the operator's responsibility)
* Forge's existing multi-tenant user isolation carries through — Tether's API key is a regular forge user, so the daemon only sees its own sessions

### 14.3 Matrix leg

* Appservice registration uses `as_token` and `hs_token` (both random, stored in config)
* **No E2EE in v1** — the appservice user is a "regular" homeserver-side account; messages between the Tether appservice user and a regular Matrix user are E2EE only if both clients support it, but the appservice sees plaintext (this is fundamental to any bridge). For our threat model (operator runs both Tether and the homeserver), this is fine.
* For v2: enable mautrix-go's E2EE (Megolm) so the Tether puppet can participate in E2EE rooms. This is non-trivial — pickup keys, device verification — but mautrix-go has it.

### 14.4 SD card encryption — out of scope for v1

LittleFS on the SD card holds raw Opus, message history, and conversation metadata. Not encrypted in v1. If the M5 is lost, an attacker can read it. For v2: enable the ESP32-S3's flash encryption + a per-device key, and have LittleFS read/write go through that.

---

## 15. Filesystem Layout (LittleFS on M5)

```
/
├── conv/
│   ├── <uuid>/
│   │   ├── meta.bin         # Conversation struct (name, kind, target, last_activity, etc.)
│   │   ├── history.bin      # ring buffer of last 50 messages
│   │   └── ratchet.bin      # per-conv AES counter
│   └── ...
├── queue/
│   ├── pending/<msg_id>.opus
│   ├── inflight/<msg_id>.opus
│   ├── sent/<msg_id>.opus       (optional archive)
│   └── failed/<msg_id>.opus
├── tts_cache/
│   └── <msg_id>.opus            # last TTS audio (for "replay" command)
├── config/
│   ├── node.bin                 # node_id, master PSK
│   ├── channels.bin
│   └── volume.bin
└── log/
    └── <YYYY-MM-DD>.log
```

Writes: ~3 KB/min during active use. A quality SD card (10K+ erase cycles) lasts years.

---

## 16. Test Plan

| Phase | Test | Pass criterion |
|---|---|---|
| 0 | Loopback: two Go processes, one as bridge emulator, one as M5 emulator | 60 s round-trip without loss |
| 1 | Bench OTA: M5 + bridge 1 m apart, both on USB | All packet sizes, all SF presets work |
| 2 | **STT loopback:** PC plays known WAV, Tether STT pipeline returns text | WER ≤ 10 % on a held-out test set |
| 3 | **TTS loopback:** PC synthesizes known sentence, listen-test | Intelligible, no clipping |
| 4 | **Matrix round-trip:** M5 voice → STT → Matrix room → other Element client → reply → M5 TTS | Full round-trip, no manual intervention |
| 5 | **Forge round-trip:** M5 voice → STT → forge session → agent → TTS → M5 | Agent reply arrives within 5 s of agent_end |
| 6 | **Multi-conversation:** 4 concurrent convs (2 Matrix, 2 forge), interleaved PTT | No cross-talk, no mis-routed messages |
| 7 | Indoor range: M5 walks a building, base fixed | 95 % delivery at expected range |
| 8 | Outdoor LOS range, stock antennas | Establish max reliable range vs §3.5 |
| 9 | Power: 10-message scripted cycle, log VBat | Matches §7.6 |
| 10 | Interference: 2nd pair on adjacent channel | CAD backoff works, no crashes |
| 11 | Stress: SD filled, weak signal, hot/cold | Graceful failure modes |
| 12 | **Appservice re-registration:** kill + restart tetherd mid-conversation | In-flight messages recover, no duplicates |

---

## 17. Phased Implementation Plan

### Phase 0 — Tooling & schemas (1–2 days)
1. Finalize the packet header (24 bytes) and ACK format in a shared schema.
2. Generate protobuf/struct definitions in both Go and C++.
3. Create a test vector corpus of Opus + pre-encoded samples for regression testing.

### Phase 1 — Lock the data plane in Go (1–2 weeks)
1. Write a Go CLI that sends/receives a single packet over a loopback serial.
2. Write a Go CLI that Opus-encodes a known WAV and sends it as a fragmented sequence over loopback. Validate reassembly.
3. Add the ACK loop with cumulative bitmap; verify the lost-packet case.

### Phase 2 — Bridge firmware (1–2 weeks)
4. Port the Go protocol's frame format to C++ on the nRF52840 (RAK4631) using RadioLib.
5. Implement CAD, AES-128-CTR via SX1262 hardware engine.
6. Validate PC↔bridge↔M5 path with the M5 running a "ping" sketch.

### Phase 3 — M5 firmware skeleton (2–3 weeks)
7. ESP-IDF project: SPI bus init, SX1262 + SD + EPD on the bus, mutex.
8. I2S mic capture, Opus encode to PSRAM ring buffer.
9. PTT button (GPIO IRQ) → start record, release → save to LittleFS under `/queue/pending/`.
10. Radio task: read chunks from SD, send via LoRa, handle ACKs, retry, fail.

### Phase 4 — EPD + multi-conversation (1–2 weeks)
11. Persistent conversation DB in LittleFS.
12. EPD screens: idle, recording, queued, TX, TTS playback, settings.
13. Button B/C: cycle conversations; long-press B: settings.
14. `UI_UPDATE` packet handling: add/remove/rename conversations.

### Phase 5 — STT + TTS on PC (1–2 weeks)
15. Integrate `sherpa-onnx` Parakeet-TDT int8 via cgo.
16. Integrate Piper via subprocess pipe.
17. Wire the full pipeline: M5 voice → STT → text; text → TTS → M5 voice.
18. Test on a held-out speech corpus.

### Phase 6 — Matrix appservice (1–2 weeks)
19. `mautrix-go` appservice: register, puppet user, listen for `m.room.message`.
20. Map Matrix rooms to Tether conversations.
21. Outbound: STT text → appservice intent SendMessage.
22. Inbound: appservice syncer OnEventType → TTS queue.
23. Add/remove conversation via `UI_UPDATE` to M5.

### Phase 7 — Forge integration (1–2 weeks)
24. Forge HTTP client (login, sessions, messages).
25. Forge SSE consumer (`r3labs/sse/v2`).
26. Map forge sessions to Tether conversations.
27. Tool output streaming into TTS.

### Phase 8 — Hardening (ongoing)
28. Per-conversation AES key derivation (HKDF).
29. Replay protection (monotonic `msg_id`).
30. Watchdog across all M5 tasks.
31. OTA update path (USB for v1; LoRa-Bluetooth for v2).
32. Power optimization: deep sleep, lazy SPI init, peripheral gating.
33. Range + power + interference tests per §16.

### Phase 9 — Polish (optional)
34. Operator TUI in bubbletea: live conversation list, manual TTS replay, RF stats.
35. CLI commands: `tether conv list`, `tether forge create`, `tether say <conv> "text"`.
36. E2EE via mautrix-go Megolm.
37. Frequency hopping.

---

## 18. Sources

**Hardware / LoRa:**
* Semtech SX1262 datasheet — `https://cdn.sparkfun.com/assets/6/b/5/1/4/SX1262_datasheet.pdf`
* Semtech CAD app note — `https://www.semtech.com/uploads/technology/LoRa/cad-ensuring-lora-packets.pdf`
* Meshtastic LoRa config — `https://meshtastic.org/docs/configuration/radio/lora/`
* Meshtastic radio settings — `https://meshtastic.org/docs/overview/radio-settings/`
* ThinkNode M5 wiki — `https://www.elecrow.com/wiki/ThinkNode_M5_Meshtastic_LoRa_Signal_Transceiver_ESP32-S3.html`
* LoRa airtime calculator — `https://avbentem.github.io/airtime-calculator/`
* RadioLib — `https://github.com/jgromes/RadioLib`

**Audio / Codec:**
* Xiph Opus recommended settings — `https://wiki.xiph.org/Opus_Recommended_Settings`
* micro-opus (ESP component) — `https://components.espressif.com/components/esphome/micro-opus`
* esp-libopus — `https://github.com/kahrendt/esp-libopus`

**STT:**
* NVIDIA Parakeet-TDT 0.6B v2 — `https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2`
* sherpa-onnx NeMo transducer models — `https://k2-fsa.github.io/sherpa/onnx/pretrained_models/offline-transducer/nemo-transducer-models.html`

**TTS:**
* Piper — `https://github.com/rhasspy/piper` (now `https://github.com/OHF-Voice/piper1-gpl`)

**ESP-IDF / embedded:**
* ESP-IDF external RAM — `https://docs.espressif.com/projects/esp-idf/en/stable/esp32s3/api-guides/external-ram.html`
* ESP-IDF file system — `https://docs.espressif.com/projects/esp-idf/.../file-system-considerations.html`
* LittleFS — `https://github.com/littlefs-project/littlefs`

**Go libraries:**
* `maunium.net/go/mautrix` — `https://pkg.go.dev/maunium.net/go/mautrix` and `https://github.com/mautrix/go`
* `gopkg.in/hraban/opus.v2` — `https://pkg.go.dev/gopkg.in/hraban/opus.v2`
* `go.bug.st/serial` — `https://pkg.go.dev/go.bug.st/serial`
* `charmbracelet/bubbletea` — `https://github.com/charmbracelet/bubbletea`
* `r3labs/sse/v2` — `https://github.com/r3labs/sse`

**Audio routing (PC):**
* VB-Cable — `https://vb-audio.com/Cable/`
* VoiceMeeter — `https://vb-audio.com/Voicemeeter/banana.htm`

**Forge (sibling project):**
* `../forge/README.md`, `../forge/AGENTS.md` (Rust + PostgreSQL + pi-mono)
