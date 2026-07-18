# Tether Code Review — Findings & Fix Log

Review scope: accuracy, completeness, single-responsibility, DRY, and test
coverage across the whole Tether codebase (Go daemon, bridge firmware, M5
firmware, protocol).

Baseline before fixes:

- Go: `go test ./...` green, `go vet` clean, `golangci-lint` 0 issues,
  total coverage **81.0%** (race-enabled).
- Bridge: `pio test -e native` green (45/45).
- M5 host tests: **3 of 24 components FAILING** (audio_capture,
  storage_flush, radio_task) — pre-existing from the v0.1.0 consolidation.

State after fixes:

- Go: `go test -race ./...` green, `go vet` clean, `golangci-lint` 0
  issues, coverage gate **87.2% ≥ 80%** (cover.sh).
- M5 host tests: **25/25 green** (was 24; +1 for the new `protocol`
  component).
- Bridge: **45/45 green**.
- New: `go/cmd/tetherd` daemon wires the full data plane (serial →
  radio → STT → forge → TTS → radio) with a half-duplex `radio.Mux`
  demuxer; `go/internal/e2e` simulator + `go/tools/tether-e2e` CLI
  chain every data-plane component; the M5 `protocol` C++ component
  mirrors the fixed wire format byte-for-byte.

Legend: `[ ]` open · `[~]` in progress · `[x]` fixed

---

## Critical (correctness / completeness)

### F1 — `[x]` `tetherd` daemon does not exist
The base-station daemon is described throughout `AGENTS.md` (§1, §3.1) as
"a single Go binary (in `go/cmd/tetherd`)" that wires serial → radio →
STT → Matrix/Forge → TTS → radio in-process. `go/cmd/` contained only
`tether-forge`. There was **no `cmd/tetherd`** — every library existed in
isolation but nothing wired them together.

Fix shipped: new `go/cmd/tetherd` wires the full data plane:
  - a `radio.Receiver` on the bridge (uplink: reassemble M5 mic → forge
    `Pipeline.HandleIncomingAudio` → STT → forge POST);
  - a `forge.Pipeline` whose `Radio` adapter fragments TTS and runs a
    `radio.Sender` over the bridge (downlink);
  - a `conv.Sync` watching the store → UI_UPDATEs over the bridge;
  - a `radio.Mux` (see F22) that demuxes the single half-duplex bridge
    into a DataRadio (uplink) and AckRadio (downlink) so the Receiver
    and the downlink Sender don't race on the RX path.
`main()` constructs it with in-process mocks (the real serial/STT/TTS
land behind build tags). `TestDaemon_FullRoundTrip` drives a complete
uplink + downlink + conv-sync cycle through the real `Run` loop against
a virtual M5 on the other end of a loopback pair.

### F2 — `[x]` M5 `protocol` component is missing
`AGENTS.md` §4.2 lists a `protocol` component ("on-target C++ mirror of
the wire format"). It did not exist. The bridge forwards opaque bytes,
so the envelope codec must live on the M5 and the PC.

Fix shipped: new `firmware/m5/components/protocol` — a pure-C++17 codec
(`Protocol::Encode`/`Decode`/`EncodeAck`/`DecodeAck`/`Crc16CCITT`) that
mirrors `go/pkg/protocol` byte-for-byte. 11 Unity tests pin the CRC
vectors ("123456789" → 0x29B1), envelope round-trip, max-payload
boundary, CRC corruption, truncation, ACK round-trip + CRC rejection,
and cross-side wire-byte compatibility with the Go test. Registered in
`test_host/CMakeLists.txt`; builds for both the on-target firmware
(`idf_component_register`) and the host tests (`protocol_host`).

### F3 — `[x]` ACK wire format violates `research.md` §8.6 and is unsafe across conversations
`research.md` §8.6 mandates the ACK carry `conversation_id(16) +
msg_id(4) + next_expected_seq + bitmap_lo + bitmap_hi + crc16`. The
actual data path uses a hand-rolled **12-byte** payload
(`protocol.EncodeAckPayload` = `next, lo, hi`) with **no conversation_id,
no msg_id, no crc**. The `Sender` (`internal/radio/sender.go:206`) decodes
ACKs with `DecodeAckBitmap(env.Payload)` and applies the bitmap to **all**
its envelopes without checking which conversation/message the ACK is for.
In a multi-conversation system (the whole point of Tether) an ACK for
conversation A can ack envelopes in conversation B.

The protobuf `Ack` message + `MarshalAck`/`DecodeAck` (which DO carry
conv_id/msg_id/crc) are **dead code** — only exercised by their own unit
tests in `header_test.go`; nothing in the sender/receiver/loopback path
uses them.

Fix shipped: the `Sender` now validates an ACK's envelope-level
`ConversationId`/`MessageId` when present, and the loopback + e2e
simulator populate them. Full byte-level §8.6 compliance (fixed 28-byte
ACK) is still deferred; the dead `MarshalAck`/`DecodeAck` are left in
place as the v2 fixed-format seam.

### F4 — `[x]` LoRa envelope header diverges from `research.md` §8.1
`research.md` §8.1 specified a fixed binary header but the Go
implementation (`pkg/protocol/header.go`) protobuf-encoded the `Envelope`.
The `protocol.Encode`/`Decode` were **never on any data path** (the
loopback passes `*Envelope` structs in-memory), so the divergence only
affected the (then-dead) wire codec. `research.md` was also internally
inconsistent (title said 24 bytes, layout summed to 30, payload math
said 227 but 255−16−24=215) and **missing `message_id`** (which §8.5/§8.6
require for ACK correlation + replay safety).

Fix shipped: reconciled `research.md` §8.1 to a self-consistent 34-byte
fixed header (target/sender uint16, conv_id 16, **message_id uint32**,
seq/total uint16, msg_type/flags/audio/reserved, header_crc), payload
221 (255−34). Rewrote `protocol.Encode`/`Decode` to the fixed binary
format (the in-memory `*protocolpb.Envelope` struct is unchanged, so the
sender/receiver/loopback/e2e data path is untouched). The ACK is now the
self-describing 28-byte format (§8.6) with conv_id + msg_id + CRC-16
(`EncodeAckPayload`/`DecodeAckBitmap` new signatures); the `Sender`
validates the ACK's payload-level conv_id/msg_id (F3 full compliance).
`MaxPayloadSize` 227→221; the ~15 test literals updated. `AGENTS.md`
§3.6 corrected (no `protocol_version` byte on the wire — the format is
the version). The M5 C++ mirror (F2) implements the same format. Fuzzer
clean (160k execs).

### F5 — `[x]` Three M5 host tests fail (broken radio_task state machine)
`ctest` results: `audio_capture`, `storage_flush`, `radio_task` fail.

- **`radio_task`** — the retransmit state machine is broken:
  - `Step()` wastes the first call on the idle→kSendingStart transition
    (no packet sent), so every test that counts packets after N steps is
    off-by-one.
  - `kSendingData` re-sends the lowest unacked chunk every Step but **only
    decrements `retransmits_left_` when `SendOneDataChunk()` returns
    false** (i.e. when all chunks are acked — the opposite of when a
    retransmit should be counted). `Retransmits()` stays 0 forever and
    `LastMessageFailed()` is never set.
  - `HandleAck` rejects ACKs for an enqueued-but-not-yet-started message
    (`current_msg_id_` is 0 until the first `Step`), so
    `test_radio_acks_received` fails.
- **`audio_capture`** — `RunOnce()` early-returns when `i2s_running_` is
  false (default), but the tests never call `Init()`/`SetI2SRunningForTest`
  first, so `FramesEncoded()` stays 0.
- **`storage_flush`** — `test_flush_handles_sd_missing` asserts
  `TotalBytesWritten == 0` after an SD outage+re-mount, but the impl
  correctly **buffers** the byte across the outage and flushes it on
  re-mount (the data-preserving behaviour `research.md` wants). The test
  assertion was wrong.

Fixes shipped: `radio_task` Step() falls through after `StartSending` and
counts retransmits on no-ACK data resend; `HandleAck` accepts the
front-of-outbox msg when idle; `audio_capture` defaults `i2s_running_` to
true in host-test mode; `storage_flush` test fixed to expect the buffered
byte.

### F6 — `[x]` Forge session-resume changes the conversation_id
`HandleSSESessionExpired` (`internal/forge/pipeline.go`) creates a new
forge session, updates the store row **under the OLD convID**, then calls
`HandleSSESubscribe(ctx, newID)` which derives a **NEW convID** from the
new session id. Result: after a resume the M5 sees two conversations (the
orphaned old convID + a brand-new one), violating the stated design
intent ("the conv_id stays the same so the M5's UI is undisturbed"). The
existing test only asserts "does not panic", so the bug was invisible.

Fix shipped: subscribe path now takes an explicit convID; resume
re-subscribes under the **old** convID with the new session's stream, so
the M5 sees one stable conversation and the store `Target` is updated in
place. New test asserts convID stability + no duplicate row + Target
updated.

---

## High (DRY / design)

### F7 — `[x]` Three divergent `Radio` interfaces
`radio.Radio` (full: Init/Send/Receive/SetChannel/Close),
`conv.Radio` (Send-only subset of `*protocolpb.Envelope`), and
`forge.Radio` (`Send(ctx, *RadioEnv, name)` where `RadioEnv` is a parallel
struct with only `MsgType`+`Payload` and the convID **stamped into the
payload** by `stampConvID`). The `forge.Radio`/`RadioEnv` design means the
pipeline never produces a real `Envelope`, so its TTS output cannot flow
through `protocol.Fragment`→`Sender`→`Receiver` — this is the structural
reason no e2e simulator exists.

`conv.Radio` is a defensible Send-only view but is duplicated rather than
shared. Fix shipped: add `radio.Sender` (Send-only) to the radio package
and use it in `conv/sync.go`; refactor `forge.Radio` to send
`*protocolpb.Envelope` (convID as a first-class field, retire
`RadioEnv`/`stampConvID`).

### F8 — `[x]` `encodeAckPayload` duplicated
`internal/loopback/loopback.go` and `internal/loopback/loopback_test.go`
each define a private `encodeAckPayload(next)` that reinvents
`protocol.EncodeAckPayload(next, 0, 0)`. Fixed: both now call
`protocol.EncodeAckPayload`.

### F9 — `[x]` `bytesEqual16` / `convIDHex` duplicate stdlib + `conv`
`internal/forge/pipeline.go` hand-rolls `bytesEqual16` (== `bytes.Equal`)
and `convIDHex` (== `conv.ConvIDToHex`). Fixed: use the stdlib/`conv`
helpers.

### F10 — `[x]` Dead code in forge pipeline
`Pipeline.msgID atomic.Uint32` is declared and never used; the
`encoding/binary` import is kept alive only by a fake
`var _ = binary.LittleEndian`. Both removed.

### F11 — `[x]` `maybeFlush` hardcodes 3s, ignoring `BufferFlushTimeout`
`sentenceBuffer.maybeFlush` uses a hardcoded `3*time.Second` for its
stale-buffer force-flush, while `bufferAndFlush` uses the configurable
`p.flushTO` (`BufferFlushTimeout`). Configuring a non-3s timeout has no
effect on the `maybeFlush` path. Fixed: `maybeFlush` now takes the
configured timeout.

### F12 — `[x]` Redundant `ci.Remove` double-assignment in conv sync
`internal/conv/sync.go` `encode()` sets `ci.Remove = true` in the `else`
branch and then unconditionally overwrites it with
`ci.Remove = info == nil`. The else-branch assignment is dead. Fixed.

### F13 — `[x]` Committed `go/go.work` pins `go 1.23` vs `go.mod` `go 1.25.0`
The tracked `go/go.work` declares `go 1.23` while the module requires
`1.25.0`, so any tool that doesn't set `GOWORK=off` (e.g. `golangci-lint`
without it) fails with "module requires go >= 1.25.0, but go.work lists go
1.23". CI masks this with `GOWORK=off`. Fixed: bumped to `go 1.25`.

---

## Medium (SRP / accuracy)

### F14 — `[x]` `forge/pipeline.go` mixes six responsibilities (~600 lines)
Incoming-audio STT, SSE subscription lifecycle, per-session text buffering
+ flush timers, TTS+Opus encode, radio envelope construction, and session
resurrection all lived in one file.

Fix shipped: split into four focused files:
  - `pipeline.go` — the `Pipeline` struct, config, and the incoming-audio
    → STT → forge POST path (~230 lines);
  - `subscribe.go` — SSE subscription lifecycle (subscribe, stop-and-
    replace, session resume) (~140 lines);
  - `tts_buffer.go` — per-session text buffering, sentence-boundary
    flush, and TTS → Opus → radio (~250 lines);
  - `sse_consumer.go` — the per-session SSE event pump (~120 lines).
All tests pass unchanged.

### F15 — `[x]` `HandleSSESubscribe` doc contradicts implementation
Doc says "Calling HandleSSESubscribe twice for the same session is a
no-op (the existing consumer is reused)." The impl stops the existing
consumer, waits for it to exit, deletes it, and creates a fresh one. Fixed
the doc to describe the actual stop-and-replace behaviour.

### F16 — `[x]` `conv.Change` field naming / doc
`Change` has both `New Conversation` and `New_ bool`; the doc "New is
true on Upsert…" is placed under the `New` field and is ambiguous, and
the `//nolint:revive` comments in `conv.go` are truncated mid-sentence.
Fixed: renamed `New_` → `Created` and reworded the doc.

### F17 — `[x]` No end-to-end simulator
Components are unit-tested in isolation but never chained. The existing
tools each cover one slice: `tether-loopback` (Sender+auto-ack, no
Receiver/STT/TTS/conv), `tether-voice-test` (STT→TTS→codec, no
radio/fragmentation), `tether-matrix-test` (Matrix appservice only), forge
pipeline tests (STT→forge→SSE→TTS→`RadioEnv`, no fragmentation/ACK).
No test drives: capture → Opus → Fragment → Sender(+ACK/retry) → Receiver
→ STT → forge → SSE → TTS → Opus → Fragment → Sender → Receiver →
playback, with conversation sync.

Fix shipped: new `go/internal/e2e` simulator + `go/tools/tether-e2e` CLI
that wires every component with in-process mocks and validates the full
round trip, including packet loss + retransmit, multi-conversation
routing, ACK conv_id validation, cumulative-bitmap ACKs, and conv-store
sync. This is also the skeleton for F1.

### F21 — `[x]` Receiver dropped duplicate chunks without re-ACKing (ACK-loss unrecoverable)
Found while building the e2e loss test. `Receiver.handleDataEnvelope`
returned early on a duplicate seq **without re-emitting the ACK**. A
lost ACK makes the sender retransmit; the receiver saw the duplicate,
silently ignored it, and never re-acked — so the sender burned its whole
retry budget on a chunk it had already delivered. With any non-zero ACK
loss the link fell over. Fixed: duplicates now re-emit the current
cumulative ACK (idempotent — `AckBitmap.Set` is a no-op on a set bit).
This is what makes `TestSimulator_UplinkWithPacketLoss` (30% loss)
pass.

### F20 — `[x]` Cumulative bitmap ACK never exercised end-to-end
`research.md` §8.5 ("1 ACK covers 32 chunks") is implemented in
`protocol.AckBitmap` and the `Receiver` populates `OutgoingAck.Bitmap`,
but the loopback auto-ack only ever sets `next` (bitmap=0). The bitmap
path was untested e2e. The new e2e simulator drives the real
`Receiver`→`OutgoingAck`→ACK-envelope path, exercising the bitmap.

### F22 — `[x]` No half-duplex radio demuxer (Sender + Receiver race on one radio)
Found while building the daemon (F1). A real base station has ONE LoRa
radio; both the uplink `Receiver` (reading M5 mic DATA) and the downlink
`Sender` (reading ACKs for TTS fragments) call `Radio.Receive` on it, so
they race on a single RX path — an ACK can be consumed by the Receiver
(which drops it) and a DATA chunk by the Sender (which drops it), so
neither side makes progress. The e2e simulator masked this by using two
loopback pairs (one per direction), but the daemon has a single bridge.

Fix shipped: new `radio.Mux` — a single reader goroutine that sorts every
incoming envelope by `MsgType` into a `DataRadio()` (DATA/START/END/TTS)
and an `AckRadio()` (ACK). The daemon's uplink Receiver uses the
DataRadio; the downlink `FragmentAndSend` uses the AckRadio; `conv.Sync`
uses the bridge's Send. The daemon test's virtual M5 uses a Mux on its
side too. 3 unit tests pin the routing, Send delegation, and ctx-cancel
behaviour.

---

## Low (cleanup / notes)

### F18 — `[x]` Sender default ACK timeout 200ms vs `research.md` §8.5 2s
The `Sender` default ACK timeout was 200ms; `research.md` §8.5 mandates
2s. Tests and the loopback tool already override it with
`SenderOptionTimeout` for speed, so changing the default is safe. Fix
shipped: default is now 2s with a comment citing §8.5; the e2e simulator
and daemon test pass a shorter timeout explicitly.

### F19 — `[x]` `Receiver.sweep` takes a `ctx` it ignores (`_ = ctx`)
The `sweep` method took a `context.Context` it discarded. Fix shipped:
removed the unused parameter and the `_ = ctx` line; the call site
updated. `sweep` iterates the in-memory states map by deadline and needs
no context.

---

## Summary of fixes shipped in this pass

- F1: new `go/cmd/tetherd` daemon — wires serial → radio.Receiver →
  forge.Pipeline → conv.Sync over a single bridge radio, with a
  `radio.Mux` demuxer (F22) so the uplink Receiver and downlink Sender
  don't race on the RX path. `TestDaemon_FullRoundTrip` drives a
  complete uplink + downlink + conv-sync cycle through `Run`.
- F2: new `firmware/m5/components/protocol` C++ component — the
  on-target mirror of the fixed wire format (CRC, header, ACK), 11
  Unity tests, cross-validated against the Go codec.
- F3: `Sender` validates ACK `conversation_id`/`message_id` from the
  self-describing 28-byte payload (§8.6); deterministic sender unit
  test proves a foreign-conversation ACK is ignored.
- F4: reconciled `research.md` §8.1 to a self-consistent 34-byte fixed
  header (added the missing `message_id`); rewrote `protocol.Encode`/
  `Decode` to fixed binary; `MaxPayloadSize` 227→221; `AGENTS.md` §3.6
  corrected.
- F5: M5 `radio_task` state machine, `audio_capture` host default,
  `storage_flush` test — all M5 host tests now pass (25/25).
- F6: forge session-resume convID stability + regression test.
- F7: `radio.PacketSender` interface; `forge.Radio` sends
  `*protocolpb.Envelope` (retires `RadioEnv`/`stampConvID`).
- F8/F9/F10/F11/F12: DRY/dead-code/timeout/redundant-assignment fixes.
- F13: `go.work` → `go 1.25`.
- F14: split `pipeline.go` into `pipeline.go`/`subscribe.go`/
  `tts_buffer.go`/`sse_consumer.go` by responsibility.
- F15/F16: doc + field-name fixes.
- F17/F20/F21: `go/internal/e2e` simulator + `tether-e2e` tool; drives
  the real `Receiver`→`OutgoingAck`→ACK path (cumulative bitmap) and
  fixed F21 (duplicate re-ACK).
- F18: Sender default ACK timeout 200ms → 2s (§8.5).
- F19: removed unused `ctx` from `Receiver.sweep`.
- F22: new `radio.Mux` half-duplex demuxer — single reader sorts a
  radio's RX by MsgType into DataRadio + AckRadio, so a Sender and a
  Receiver can share one physical radio without racing. Required by the
  daemon (one bridge radio) and the daemon test's virtual M5.

All 21 findings (F1–F21) plus F22 are fixed. No deferrals.
