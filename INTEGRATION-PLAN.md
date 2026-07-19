# Tether Integration Plan — v0.2.0

**Goal:** Move from "well-tested components with mock-wired mains" to a
working end-to-end system on real hardware: LilyGO T3-S3 MVSR (handheld)
+ RAK4631 bridge (USB-serial) + Linux PC running `tetherd`.

**Hardware target:** LilyGO T3-S3 MVSR only (M5 variant code stays for
test coverage but is not finished). RAK4631 bridge + Linux base station.

**Precedence:** `research.md` > `plan.md` > this file.

---

## Gap summary (verified against actual code)

| Piece | Status | What's missing |
|---|---|---|
| Go serial transport | **absent** | `go/internal/serial/` has only `loopback.go`. No `go.bug.st/serial` dep. No frame codec. No `radio.Radio` adapter over a real `/dev/ttyACM0`. |
| Go Opus codec | **mock only** | `codec/opus.go` is an identity mock. No cgo libopus wrapper. |
| Go `tetherd` main() | **all mocks** | `stt.NewMock()`, `tts.NewMock()`, `codec.NewMock()`, `forge.NewMockClient()`, `serial.NewLoopbackPair()`. No build-tag wiring to real impls. |
| Go STT (Parakeet) | **exists, gated** | `parakeet_cgo.go` behind `-tags parakeet`. Not wired to daemon. |
| Go TTS (Piper) | **exists, not wired** | `piper_subprocess.go` exists. Not wired to daemon. |
| Go forge client | **exists** | `forge/client.go` has a real HTTP+SSE client. Not wired to daemon (mock used). |
| Bridge `main.cpp` | **skeleton** | `Serial.begin` + WDT feed only. `SerialLink` never instantiated. LoRa never initialized. |
| MVSR `main.cpp` | **Phase 3 skeleton** | No radio constructed. No tasks started. Buttons not wired to PTT. Placeholder START packet. No-op RX handler. NVS not initialized. |
| M5 radio_task protocol | **placeholder** | Uses 100-byte chunks + `{0x01,0x02,0x03}` START. Not the 34-byte header from `protocol` component. Doesn't match Go Sender/Receiver. |

---

## Work blocks

### I1 — Go: bridge frame codec + serial transport
**Files:** `go/internal/serial/frame.go`, `frame_test.go`, `transport.go`, `transport_test.go`

- `frame.go`: Go mirror of `firmware/bridge/src/frame.h` — `0xAA 0x55 | type | len(2 LE) | payload | crc16(2 LE)`, CRC-16/CCITT-FALSE. `EncodeFrame(Frame) []byte`, `FrameDecoder` streaming decoder.
- `transport.go`: `Transport` implements `radio.Radio` over a `io.ReadWriteCloser` (serial port):
  - `Send(ctx, env)` → `protocol.Encode(env)` → `EncodeFrame(kAck, encoded)` → write to port
  - `Receive(ctx)` → background goroutine decodes frames → `kRxPacket` frames → `protocol.Decode(payload)` → return env
  - `Init(ctx, preset)` → `EncodeFrame(kSetConfig, [sf,bw,cr,power,sync])` → write
  - `SetChannel` → no-op (single channel v1)
  - `Close()` → close port + stop goroutine
- `transport_test.go`: use `net.Pipe()` as the serial port; verify Send/Receive round-trip, frame codec correctness, CRC rejection.
- Add `go.bug.st/serial` to `go.mod` (only imported in the build-tagged `transport_real.go`).

### I2 — Go: real Opus codec (cgo libopus)
**Files:** `go/internal/codec/opus_cgo.go`, `opus_cgo_test.go`

- Build tag `opus`. cgo wrapper around `<opus/opus.h>`:
  - `NewCgoEncoder()` → `opus_encoder_create`, 8 kHz / mono / VOIP / 16 kbps / complexity 5
  - `NewCgoDecoder()` → `opus_decoder_create`, 8 kHz / mono
  - Implement the `Opus` interface (Encode/Decode)
- Test: encode a known PCM sine → decode → verify RMS / no crash. Round-trip identity isn't exact (lossy) but frame-size alignment is testable.
- The mock stays as the default (`!opus` build tag).

### I3 — Go: `tetherd` build-tag wiring + config
**Files:** `go/cmd/tetherd/wire_prod.go`, `wire_mock.go`, `config.go`, `config_test.go`

- `config.go`: `Config` struct + `LoadConfig(path)` TOML parser (matches research.md §13.5 schema).
- `wire_prod.go` (`//go:build production`): construct real serial transport, real Opus (build tag `opus`), real Parakeet (build tag `parakeet`), real Piper, real forge HTTP client. Read `tetherd.toml`.
- `wire_mock.go` (`//go:build !production`): current mock wiring (unchanged behavior).
- `main.go`: call `loadAndWire()` (defined in one of the two wire files) instead of inline mocks.
- Tests: `config_test.go` parses a sample TOML; the mock wire path is exercised by `main_test.go` (existing).

### I4 — Bridge firmware: finish `main.cpp`
**File:** `firmware/bridge/src/main.cpp`

- Instantiate `ArduinoSerialPort` (wraps `Serial`), `RadioLibBackend` (RadioLib SX1262 on RAK4631 pins), `LoRaRadio`, `SerialLink`.
- `setup()`: `Serial.begin(921600)`, init LoRa with default preset, construct SerialLink.
- `loop()`: call `serialLink.Step()` in a tight loop + feed WDT.
- The `native` env test already tests `SerialLink.Step()` with mocks; this just wires the real objects.

### I5 — MVSR firmware: finish `main.cpp`
**File:** `firmware/m5/main/main.cpp` (MVSR-targeted)

- Init NVS (`nvs_flash_init`).
- Construct `RadioLibBackend` → `LoraRadio` → `Init(Preset::Default())` + `SetChannel(0)`.
- Construct `OpusEncoder`, `PsramRing`, `AudioCapture`, `StorageFlush`.
- Wire buttons → PTT: PTT press → `ptt.OnButton(press)` → `AudioCapture` starts; release → `ptt.OnButton(release)` → stop + enqueue to radio.
- Start FreeRTOS tasks: `audio_capture`, `storage_flush`, `radio_task`, `conv_manager`, `ui_state`, `watchdog`.
- Wire `conv_manager` sink → `radio_task` (UI_UPDATE outgoing).
- Wire `radio_task` RX → `conv_manager.OnUiUpdate` (UI_UPDATE incoming) + TTS playback path.
- SSD1306 display: boot screen → idle screen; state-driven screens (recording/tx/tts) via `ui_state`.
- Replace placeholder START packet with `protocol.Encode` (34-byte header).
- Replace no-op `HandleRxPacket` with `protocol.Decode` + dispatch.

### I6 — MVSR firmware: align radio_task to the 34-byte protocol
**File:** `firmware/m5/components/radio_task/src/radio_task.cpp`

- Use `protocol::Encode` / `protocol::Decode` (the C++ mirror) instead of 100-byte chunks + placeholder START.
- Fragment payloads into ≤221-byte chunks with the 34-byte header (seq_num / total_seqs).
- ACK handling: decode the 28-byte self-describing ACK payload, match conv_id + msg_id.
- This aligns the M5 radio_task with the Go `radio.Sender`/`radio.Receiver`.

---

## What's testable without hardware

| Block | Testable? | How |
|---|---|---|
| I1 (Go serial) | ✅ | `net.Pipe()` as fake serial; frame codec unit tests |
| I2 (Go Opus) | ✅ | libopus is installed; cgo round-trip test |
| I3 (Go wiring) | ✅ | config parsing; mock wire path (existing tests) |
| I4 (bridge main) | ⚠️ compile-only | `native` env tests SerialLink already; `rak4631` env needs hardware. CI doesn't build `rak4631`. |
| I5 (MVSR main) | ⚠️ compile-only | CI builds MVSR (`m5-t3s3-mvsr-build`); host tests cover components but not `main.cpp` |
| I6 (radio_task protocol) | ✅ | host tests (`test_host/`) can test the protocol alignment |

---

## Execution order

1. **I1** (Go serial transport) — unblocks everything on the Go side.
2. **I2** (Go Opus codec) — needed for real audio.
3. **I3** (Go wiring + config) — ties it together.
4. **I4** (bridge main) — quick, makes the bridge actually forward.
5. **I6** (radio_task protocol) — align M5 radio with Go.
6. **I5** (MVSR main) — the big one; depends on I6.

---

## Known limitations at v0.2.0

- **No Matrix integration wiring** — the appservice exists but is not wired into the daemon's `main()`. Forge is the primary target (it's running).
- **No E2EE** — research.md §11.5 says v1 is plaintext on the Matrix leg.
- **STT requires sherpa-onnx C library** — not installed in this env. The cgo wrapper exists (`parakeet_cgo.go`); build with `-tags parakeet,production` + installed sherpa-onnx.
- **TTS requires piper binary** — not installed in this env. The subprocess wrapper exists (`piper_subprocess.go`); build with `-tags production` + piper in PATH.
- **Single channel (ch 0, 902.3 MHz)** — no frequency hopping in v1.
