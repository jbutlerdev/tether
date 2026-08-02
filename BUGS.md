# Tether Bug Report

Scanner date: 2026-07-19. Scope: full repository (Go daemon, RAK4631
bridge firmware, M5/MVSR firmware, protocol, tooling). Every finding
was verified by reading the code paths end-to-end across layer
boundaries, not just in isolation. Severity:

- **CRITICAL** — will fail on real hardware, or is unsafe (UB / OOB / race)
- **HIGH** — silent data loss or wrong behavior in realistic conditions
- **MEDIUM** — latent bug, doc/implement mismatch, or untested-but-wrong behavior

---

## CRITICAL

### 1. M5 RadioTask ignores `next_expected_seq` in ACKs → every LoRa message fails on real hardware

**File:** `firmware/m5/components/radio_task/src/radio_task.cpp` (HandleAck, ~line 176–195)

The Go side (`receiver.go` + `ack.go`) sends **cumulative** ACKs.
`AckBitmap.Set` advances `NextExpectedSeq` and shifts lower bits out
of the bitmap **immediately** — after just chunk 0, the emitted ACK
is `(next=1, bitmap=0x00000000)`. The Go Sender interprets this
correctly via `bmp.Has(seq)` (seq < Next ⇒ acked).

The M5 `RadioTask::HandleAck` does the opposite:

```cpp
uint32_t bitmap = ack.ack_bitmap_lo | (ack.ack_bitmap_hi << 16);
acked_bitmap_ |= bitmap;                 // absolute bit positions
current_chunks_acked_ = __builtin_popcount(acked_bitmap_);
```

It **ignores `ack.next_expected_seq`** and ORs the bitmap in as
absolute chunk indices. An ACK for chunk 0 arrives as `(next=1,
bitmap=0)` ⇒ M5 marks *nothing* acked ⇒ `SendOneDataChunk` re-sends
chunk 0 ⇒ the base re-emits the same cumulative ACK ⇒ infinite
retransmit until `retransmits_left_` (5) is exhausted ⇒ **`last_failed_`
= true on every message**, even a 1-chunk one.

Only the Go↔Go paths (e2e simulator, daemon test) exercise the
cumulative-ACK contract; the M5 host test harness injects *absolute*
ACKs (`InjectAckForTest` sets a raw bitmap), so this is invisible to
every test in the tree.

**Fix:** in `HandleAck`, treat the window relative to
`ack.next_expected_seq` (advance a sliding window, mirroring
`protocol.AckBitmap`), or — simpler for ≤32-chunk v1 — popcount the
window with `next_expected_seq` as the base and cap at chunk count.

### 2. M5 RadioTask >32-chunk messages: UB shift + size_t underflow → out-of-bounds read

**File:** `firmware/m5/components/radio_task/src/radio_task.cpp:143,146`

```cpp
if (!(acked_bitmap_ & (1u << i))) {            // UB for i ≥ 32
  size_t offset = i * kMaxPayloadSize;
  size_t len = std::min(kMaxPayloadSize,
                        current_msg_.payload.size() - offset); // underflows
```

- `1u << i` for `i ≥ 32` is undefined behaviour (shift ≥ width).
- `payload.size() - offset` underflows to a huge number when
  `offset ≥ size()`, so `len` becomes 221 and `TransmitPacket`
  memcpy/encodes **past the end of the payload vector**.
- `current_chunks_acked_ = __builtin_popcount(acked_bitmap_)` can
  never reach `current_chunks_total_` for >32 chunks, so a big
  message can never complete even if every chunk is acked.

Max v1 message ≈ 32 × 221 = **7072 bytes** (~3.5 s of 16 kbps
voice). A normal PTT utterance exceeds this. The Go side handles
window rebasing; the M5 has no equivalent.

**Fix:** cap `current_chunks_total_` at 32 with an error, or implement
the rolling 32-bit window (`next_expected` + rebase) on the M5.

### 3. M5 `main.cpp`: unsynchronized data race on `g_recording_buffer` / `g_is_recording`

**File:** `firmware/m5/main/main.cpp:92–93,104–117,193–218`

Three FreeRTOS tasks touch these globals with no mutex:

- The **button-debounce task** (via `OnPttStateChange`) does
  `g_recording_buffer.clear()` on press and `std::move(...)` +
  `clear()` on release (lines 104–117).
- The **audio-capture task** (`AudioCaptureEntry`) concurrently does
  `push_back`/`insert` into the same vector (lines 217–218) and
  reads/writes `g_is_recording` (line 193).

`std::vector` concurrent mutation is UB: torn length/pointer ⇒ crash
or corrupted audio. `g_is_recording` is also a plain `bool` shared
across tasks (no `std::atomic`). Because `main.cpp` is excluded from
the host tests, no test exercises this.

**Fix:** guard both with a mutex (or use a mutex-protected queue and
move the buffer out atomically on release).

### 4. The M5 firmware "data plane" is compiled against stub components, not the real ones

**Files:** `firmware/m5/components/micro_opus/src/opus_stub.cpp` (encode
= 1 byte/frame, decode = zero-fill), `firmware/m5/components/RadioLib/`
(stub header), `firmware/m5/components/esp_littlefs/` (stub),
`firmware/m5/main/idf_component.yml` (only comments pointing at
registry URLs).

The firmware that CI builds can never move real audio or talk to a
real SX1262:

- `micro_opus` stub: every frame encodes to a single 0x00 byte and
  every decode returns 160 samples of silence. Opus "works" only by
  being a no-op.
- **RadioLib API drift:** `lora_sx1262.cpp` is written against the
  stub's API — `SX1262::begin(bw, sf, cr, sync, pwr, preamble, ...)`
  and `receive(buf, len, flags)` — which is **not** the real
  RadioLib v6 API (`begin(float freq, float bw, uint8_t sf, uint8_t
  cr, ..., float ocp)`, `receive(buf, len, timeout)`). Swapping in
  the real `jgromes/RadioLib` will **not compile** without rewriting
  `lora_sx1262.cpp`. (The bridge's `lora_radiolib.cpp` uses the
  nearer-to-real `ConfigLoRa_t` API and `receive(buf,len,timeout)`,
  so it is closer but must still be re-verified against v6.)
- `idf_component.yml` is a comment-only manifest; nothing restores
  the registry deps in CI or docs.

**Impact:** `INTEGRATION-PLAN.md` claims "the system is now a complete
data plane"; on the M5/MVSR it is a *compiling skeleton*. The daemon
side is real; the handheld side is not.

**Fix:** vendor the real micro-opus / RadioLib / littlefs components
(or wire the registry URLs) and make CI build *and* smoke-test them,
then re-verify the `lora_sx1262.cpp` API against the real library.

### 5. M5 firmware never emits ACKs → every downlink (TTS) message fails (`ErrMaxRetries` on the base station)

**Files:** `firmware/m5/components/radio_task/src/radio_task.cpp`
(`HandleRxPacket` handles inbound ACKs and forwards non-ACK packets,
but nothing ever builds/sends an ACK; grep for `EncodeAck`/`kAck`
emission returns nothing), `firmware/m5/components/radio_task/include/radio_task.h` (no ACK-out API).

On the downlink path the daemon's `downlinkAdapter`
(`cmd/tetherd/main.go`) runs a real `radio.Sender` over the bridge:
it transmits each TTS chunk and **waits up to 2 s × 5 retries for a
per-chunk ACK**. The M5 firmware has no ACK-emission code — received
DATA/START/END packets are only handed to `OnIncomingPacket`. Every
downlink send therefore exhausts the retry budget and returns
`ErrMaxRetries`: **no TTS ever makes it to the speaker on real
hardware** (compounding the duplicate-append defect noted in #6
below).

Together with #1 (uplink: M5 can't interpret cumulative ACKs), the
firmware's radio layer is doubly broken across the real link — both
directions fail, and only Go↔Go tests (simulator/daemon) pass.

The fix is a proper half-duplex MAC: when TTS/UI_UPDATE DATA chunks
arrive, decode → append → emit `EncodeAck(conv,msg,seq bitmap)` and
TX it back before the Sender's 2 s timer expires.

### 6. TTS_END is fire-and-forget, and the M5 has no inbound dedup → corrupted TTS playback

**Files:** `go/cmd/tetherd/main.go:246–253` (marker sent with a single
`bridge.Send`, no retransmit), `firmware/m5/main/main.cpp:152–162`
(`OnIncomingPacket` appends every TTS_DATA payload to `g_tts_buffer`
with no `(msg_id, seq_num)` tracking).

Two compounding defects:

1. **Lost TTS_END = permanent buffer corruption.** The M5 flushes
   `g_tts_buffer` only when TTS_END arrives. The daemon sends the
   marker once, with no ACK/retry. If it is lost on the air, the
   stale audio stays in the buffer and is prepended to the *next*
   utterance, and the M5 never shows "TTS done".
2. **Retransmitted chunks are appended twice.** `downlinkAdapter`
   runs a real Sender with per-chunk ACK + retry; a chunk whose ACK
   was lost is re-sent and the M5's `HandleRxPacket` → `OnIncomingPacket`
   path appends the duplicate payload again → garbled audio.

**Fix:** make TTS_END reliable (send it via the fragment/ACK path as
an END-type envelope, or piggyback a flush flag on the last DATA
chunk), and track `(conv, msg_id, seq_num)` on the M5 to drop
duplicates, resetting the buffer on each new TTS_DATA burst.

---

## HIGH

### 7. `AudioCapture::RunOnce` writes length+frame in two non-atomic writes; partial-frame read resets the whole ring

**Files:** `firmware/m5/components/audio_capture/src/audio_capture.cpp`
(2-byte len then frame in two `ring_.Write` calls),
`firmware/m5/main/main.cpp:199–213` (drain reads 2-byte len, then if
`Available() < frameLen` calls `g_ring->ResetForTest()`).

If the drain ever observes a partial frame (ring wraps or the task is
preempted between the two writes — plausible at the 221-byte wrap
boundary of a 32 KB ring), `ResetForTest` **wipes the entire ring**,
silently discarding the whole recording with no counter or log.

**Fix:** write len+frame with a single pre-checked contiguous reserve
(and never call a "*ForTest" method from production code); log and
drop only the partial frame instead.

### 8. UI_UPDATE is not fragmented, but `Transport.Send` rejects payloads > 221 B

**Files:** `go/internal/conv/sync.go` (`encode` comment claims "the
radio layer is responsible for fragmentation" and sends via
`PacketSender.Send`), `go/internal/serial/transport.go` (`Send` calls
`protocol.Encode` directly — no fragmentation; `header.go` returns
`ErrPayloadTooLarge` above 221 bytes).

Forge session UUIDs (36 chars) + 24-char name + encryption key + a
long Matrix room id (`!localpart:server.name`) can approach or exceed
221 bytes once protobuf-tagged. When that happens the send **fails
silently** (Sync logs a warning per event and moves on), and the M5
never learns about the conversation. The simulator and daemon tests
only use short names, so this path is untested.

**Fix:** fragment UI_UPDATE in `Sync` (reuse `protocol.Fragment` +
`FragmentAndSend`), or shrink the wire format (send name+kind+target
only).

### 9. Per-event 30 s context can truncate long/lossy downlink TTS; force-flush timer captures the first event's ctx

**Files:** `go/internal/forge/sse_consumer.go:72` (30 s
`context.WithTimeout` per dispatch), `go/internal/forge/tts_buffer.go`
(timer closure captures the first append's `ctx`).

- A sentence spans multiple radio chunks; `FragmentAndSend` may hold
  the 30 s ctx across a long/lossy burst (2 s ACK timeout × 5 retries
  × N chunks ≫ 30 s). When the ctx fires, the Sender aborts and the
  tail of the utterance is dropped with only a logged error.
- The `time.AfterFunc` force-flush timer captures the ctx of the
  event that *started* the buffer; if that per-event ctx expires and
  the buffer is still pending (e.g. flushTO misconfigured > 30 s),
  synthesized speech is silently dropped (`_ = p.speakAndSend(...)`).

**Fix:** use a pipeline-owned (daemon-lifetime) context for sends; the
30 s budget should apply to the TTS *request*, not the radio burst.

### 10. `Transport.SetChannel` is a documented no-op with no bridge channel frame

**Files:** `go/internal/serial/transport.go:185–194`, header doc
(doc claims SetChannel sends a kSetConfig with a new frequency),
`firmware/bridge/src/serial_link.cpp` (kSetConfig only carries
preset; no frequency field anywhere in the frame protocol).

Any caller that calls `SetChannel` today stays on channel 0 forever
with no error. Together with the stale docs this is a
write-only-API trap.

**Fix:** either implement `kSetChannel` (add a frame type + bridge
handler + `SetFrequency` passthrough) or make `SetChannel` return
`ErrNotSupported` and fix the docs.

---

## MEDIUM

### 11. Stale documentation contradicts the code (three places)

- `go/pkg/protocol/fragment.go:6` says "max 227 bytes per chunk";
  `MaxPayloadSize` is **221** (255 − 34).
- `go/pkg/protocol/header.go:29` comment and `proto/tether.proto:15`
  say `audio_kind // 0=mic, 1=tts, 2=beep`; the actual enum is
  UNSPECIFIED=0, **MIC=1, TTS=2, BEEP=3** (off by one — the C++
  mirror is correct).
- `go/internal/serial/frame.go` and bridge agree on the format; the
  Go side's `FrameAck` is the "TX this payload" command and the
  bridge's kAck does exactly that — consistent (no bug here, just
  noted for context).

### 12. Bridge watchdog is decorative — the WDT is never started

**File:** `firmware/bridge/src/main.cpp:96–103` — loop() writes the
reload magic `0x6E524635` to `NRF_WDT->RR[0]` (0x40010600) every
second, but the watchdog is **never configured or started**
(TASKS_START is never written, CRV/RREN never set). The write is a
no-op; a hung loop() resets nothing. If the WDT were later enabled,
the current code feeds a running WDT — but today there is no
watchdog at all.

### 13. Simulator and daemon diverge on TTS_END and ACK semantics

**Files:** `go/internal/e2e/simulator.go` `ttsAdapter.Send` (TTS_END
only sets a flag, never transmits; the virtual M5 ACKs via the Go
Receiver path), `go/cmd/tetherd/main.go` downlinkAdapter (transmits
the marker envelope; relies on the M5 to ACK).

The e2e simulator's virtual M5 *is* the Go Receiver, which ACKs and
dedups — so the simulator cannot detect either of the two real
firmware gaps (#1: cumulative-ACK interpretation, #5: M5 never
emits ACKs) or TTS_END loss. The one environment that could test the
wire contract models the M5 as a perfect citizen.

### 14. `codec.Framer.DecodeBlob` vs M5 decode loop disagree on len=0 frames

**Files:** `go/internal/codec/framer.go` (`frameLen == 0` → `continue`),
`firmware/m5/main/main.cpp` (`frameLen == 0` → `break`).

Never produced by any current encoder, but a corrupted/truncated
payload with `[0x00,0x00]` is treated differently on each side
(skip vs abort). Pick one and document it.

---

## Not bugs (checked, cross-layer consistency confirmed)

- **Wire header (34 B) and ACK (28 B) layouts:** Go `header.go`/`ack.go`
  vs M5 `protocol.cpp` are byte-for-byte equivalent, including CRC
  (CCITT-FALSE poly 0x1021, init 0xFFFF, vectors match).
- **Frame magic/CRC:** bridge `frame.h/cpp` matches Go `frame.go`
  (0xAA 0x55, CRC over type+len+payload).
- **Presets & channel map:** M5 and bridge both Default() = SF11 /
  BW125 / CR4/8 / 20 dBm / 0xF3, US915 ch0 = 902.3 MHz, 64 × 125 kHz.
- **STT/TTS HTTP clients (ParakeetHTTP/KokoroHTTP):** verified against
  live services; round-trip transcription is exact.
- **Framer round-trip with real Opus:** encode/decode consistent with
  the M5's length-prefixed ring format.
