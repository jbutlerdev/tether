# Tether — Phased Implementation Plan
*Created 2026-06-14. Companion to `research.md` (design) and `AGENTS.md` (working guide).*

This is the **complete, line-numbered, file-by-file, test-first implementation plan**. Every phase is broken into ordered tasks. For each task the doc specifies:

1. **Goal** — what "done" looks like
2. **Tests first** — the failing test(s) to write before any production code
3. **Implementation** — the production code that makes them pass
4. **Refactor** — what to clean up while staying green
5. **Commit** — the commit message and cadence
6. **Coverage gate** — the threshold this task must keep above

A phase is "done" only when every task in it is merged, the phase-level coverage gate is met, and CI is green. Do not start a phase before the previous one is done.

---

## 0. Cross-cutting rules (apply to every phase)

### 0.1 TDD discipline (strict, no exceptions)

For every non-trivial unit of work:

1. **Write a failing test** in the same commit that introduces the unit. The test must fail for the *right reason* (e.g., compile error, "function not defined", `assert.Equal` mismatch), not for an unrelated reason.
2. **See it fail** by running the test suite. Capture the failure in the commit message body.
3. **Write the minimum implementation** that makes the test pass.
4. **See it pass.** All previous tests must still pass — no regressions.
5. **Refactor** while green. Cleanup, rename, dedupe, extract. Tests must remain green.
6. **Commit.** One commit per red→green→refactor cycle. Commit message format:
   ```
   <area>: <one-line summary>

   Tests: <list of test functions added/modified>
   Coverage: <new % / total %>
   ```

For trivial mechanical work (rename a file, fix a typo in a comment), no test is needed. For any logic change, tests come first.

### 0.2 Test placement and naming

* Go: tests live next to code as `foo.go` + `foo_test.go` in the same package. Test function names: `TestFoo_Bar_Baz` (table-driven subtests use `t.Run("case name", ...)`).
* C++ (ESP-IDF): tests live in `components/<name>/test/test_<name>.cpp`. Test function names: `test_<thing>_<expected>`. Use ESP-IDF's bundled Unity framework.
* C++ (PlatformIO/bridge): tests in `firmware/bridge/test/test_<name>.cpp`. Use the PlatformIO `unity` framework, registered via `lib/Unity`.

### 0.3 Coverage gate

* **Go:** `go test -coverprofile=cover.out -covermode=atomic ./...` then `go tool cover -func=cover.out` parsed in `scripts/cover.sh`. Gate: **80 % statement coverage across `./...`**.
* **C++ ESP-IDF:** `idf.py -DTOOLCHAIN_GCOV=1 build flash monitor` then `lcov --capture --directory build --output-file coverage.info` then `lcov --summary coverage.info`. Gate: **80 % line coverage per component**; aggregate must also be ≥ 80 %.
* **C++ PlatformIO/bridge:** `pio test --coverage` (uses `gcov` + `lcov`). Gate: **80 % line coverage**.

Coverage is **enforced in CI** (`.github/workflows/ci.yml`). A PR that drops below 80 % is blocked. To raise coverage, write more tests, do not delete the production code.

### 0.4 Fuzz testing (protocol parser)

The protocol parser is the only code that is adversarial-facing (LoRa packets can be corrupted in transit; attackers may try to crash the parser). Apply `go test -fuzz=FuzzParse -fuzztime=60s` to `go/pkg/protocol` on every PR. A regression that crashes the fuzzer blocks the PR.

### 0.5 Mocking strategy

All external dependencies are interfaces, with a real and a mock implementation:

| External dep | Go interface | Mock lives at | Real lives at |
|---|---|---|---|
| LoRa radio | `radio.Radio` | `internal/radio/mock.go` | `internal/radio/sx1262.go` |
| Serial port | `serial.Port` | `internal/serial/mock_port.go` | `internal/serial/usb.go` |
| Opus codec | `codec.Opus` | `internal/codec/mock_opus.go` | `internal/codec/opus.go` |
| STT engine | `stt.Transcriber` | `internal/stt/mock.go` | `internal/stt/parakeet.go` |
| TTS engine | `tts.Synthesizer` | `internal/tts/mock.go` | `internal/tts/piper.go` |
| Audio sink | `audio.Sink` | `internal/audio/file.go` (WAV writer) | `internal/audio/pulse.go` |
| Matrix client | `matrix.Client` | `internal/matrix/mock_client.go` | `internal/matrix/appservice.go` |
| Forge client | `forge.Client` | `internal/forge/mock_client.go` | `internal/forge/http.go` |
| Conversation store | `conv.Store` | `internal/conv/mem_store.go` | `internal/conv/lfs_store.go` |

**The mock comes first.** When a new external dependency is introduced, the first commit in that area is the interface + the mock. Production code that uses the interface is the second commit. The real implementation is the third.

### 0.6 Commit cadence

* One commit per red→green cycle (one TDD iteration).
* One commit for a refactor.
* A phase is closed by a single squash-merge PR with all its commits.
* Commit message subject ≤ 72 chars; body explains *why* not *what*.

### 0.7 Branch and review model

* `main` is always green, always ≥ 80 % coverage, always builds.
* One branch per phase: `phase/0-tooling`, `phase/1-data-plane`, `phase/2-bridge`, `phase/3-m5-skeleton`, `phase/4-epd`, `phase/5-stt-tts`, `phase/6-matrix`, `phase/7-forge`, `phase/8-hardening`, `phase/9-polish`.
* Each phase ends with a PR back to `main`. CI runs full test + coverage + lint. PR requires green CI + reviewer (the operator, or a second AI agent).

### 0.8 Lint gates

* Go: `golangci-lint run --config go/.golangci.yml`. Gates: `govet`, `staticcheck`, `gofmt`, `goimports`, `errcheck`, `gosec`, `revive`. Failures block.
* C++: `clang-format` on every modified file (CI runs `clang-format --dry-run --Werror`), plus `cppcheck` on `firmware/`.

---

## 1. Phase 0 — Tooling, schemas, test infrastructure

**Goal:** All tooling, CI, schema definitions, and test scaffolding in place. Zero production code, but the test harness can be invoked end-to-end and the protocol schema is locked in code.

**Exit criteria:**
* `go test ./...` runs and passes (with zero or trivial tests, all green).
* `idf.py build` runs the empty ESP-IDF project and produces a binary.
* `pio test` runs the empty PlatformIO project and passes.
* `scripts/cover.sh` runs, reports a coverage number, fails below 80 %.
* `scripts/ci.sh` runs the full matrix locally and exits 0.
* `.github/workflows/ci.yml` runs on push and PR, blocking merges on failure.
* `proto/tether.proto` is committed, generates Go and C++ code, has unit tests verifying encode/decode round-trip.

### 1.1 Task 0.1 — Repository skeleton (no tests yet)

**Files to create:**

| Path | Purpose |
|---|---|
| `go/go.mod` | Go module `github.com/jbutlerdev/tether/go` |
| `go/.golangci.yml` | Linter config (see §0.8) |
| `firmware/m5/CMakeLists.txt` | ESP-IDF top-level CMake, `PROJECT_NAME=tether-m5` |
| `firmware/m5/sdkconfig.defaults` | ESP32-S3 defaults: `CONFIG_ESP32S3_DEFAULT_CPU_FREQ_240=y`, `CONFIG_SPIRAM=y`, `CONFIG_SPIRAM_MODE_OCT=y`, `CONFIG_FREERTOS_HZ=1000`, `CONFIG_ESP_TASK_WDT_EN=y` |
| `firmware/m5/main/idf_component.yml` | Component manifest; depends on `esphome/micro-opus`, `espressif/littlefs`, `j gromes/RadioLib` |
| `firmware/m5/main/main.cpp` | Empty `app_main()` — `ESP_LOGI("tether", "boot")` then `vTaskDelete(NULL)` |
| `firmware/m5/main/Kconfig.projbuild` | Empty `menu "Tether"` for runtime config |
| `firmware/bridge/platformio.ini` | PlatformIO project; `env:rak4631` with `platform = nordicnrf52`, `framework = arduino`, `lib_deps = jgromes/RadioLib` |
| `firmware/bridge/src/main.cpp` | Empty `setup()`/`loop()` Arduino sketch — `Serial.begin(921600);` then `delay(1000);` then idle |
| `firmware/bridge/test/README.md` | Notes on how to run `pio test` |
| `proto/tether.proto` | Protocol schema (see §1.4) |
| `proto/gen.go` | `//go:build ignore` — `//go:generate` directives for `protoc` |
| `proto/gen.sh` | Bash script invoking `protoc` to generate Go and C++ |
| `scripts/cover.sh` | Coverage gate (see §0.3) |
| `scripts/ci.sh` | Local CI: `go test`, `go vet`, `golangci-lint`, `idf.py build`, `pio test` |
| `scripts/fetch-models.sh` | Downloads Parakeet-TDT 0.6B v2 int8 + Piper voice to `/var/lib/tether/` |
| `scripts/format-cpp.sh` | Runs `clang-format -i` on changed C++ files |
| `.github/workflows/ci.yml` | CI matrix (see §1.3) |
| `.github/workflows/firmware-build.yml` | Builds M5 and bridge firmware on push |
| `.github/dependabot.yml` | Weekly Go + PlatformIO dep bumps |
| `.gitignore` | Ignores `build/`, `node_modules/`, `*.bin`, `*.elf`, `*.map`, `coverage/`, `cover.out`, `.pio/`, `sdkconfig`, `sdkconfig.old`, `dependencies.lock` |
| `docker/dev.Dockerfile` | Reproducible dev env: Go 1.22+, ESP-IDF v5.2+, PlatformIO core 6+, sherpa-onnx 1.12+, piper1-gpl 1.x, protoc 25+ |
| `docker/docker-compose.yml` | `tether-dev` service with the Dockerfile, mounts the repo at `/src` |
| `docs/TESTING.md` | TDD conventions, how to run tests, how to write fuzzer harnesses |
| `docs/ARCHITECTURE.md` | High-level architecture diagram (link to `research.md` for detail) |

**Test:** none. This is mechanical setup.

**Commit:** `chore: scaffold go/firmware/proto/CI directories`

**Coverage gate:** N/A (no code yet).

### 1.2 Task 0.2 — Coverage tooling

**Files to create:**

| Path | Lines | Purpose |
|---|---|---|
| `scripts/cover.sh` | 1–15: shebang, set -euo pipefail, print usage | Script header |
| `scripts/cover.sh` | 17–28: `go test -coverprofile=/tmp/cov.out -covermode=atomic ./...` | Run Go tests with coverage |
| `scripts/cover.sh` | 30–45: parse `go tool cover -func=/tmp/cov.out`, extract total % | Compute coverage |
| `scripts/cover.sh` | 47–58: compare against `MIN=80.0`; exit 1 if below | Enforce gate |

**Test:** `scripts/cover.sh` itself is tested by `go test ./scripts/...` (lightweight, just verifies the script exists and is executable and parses the help text).

**Commit:** `chore(scripts): add Go coverage gate`

### 1.3 Task 0.3 — CI workflow

**File:** `.github/workflows/ci.yml` (≈ 90 lines)

Jobs:

1. **`go-test`** (ubuntu-latest, Go 1.22):
   * `actions/checkout@v4`
   * `actions/setup-go@v5` with `go-version: '1.22'`
   * `cd go && go mod download`
   * `go test -race -coverprofile=cover.out -covermode=atomic ./...`
   * `bash scripts/cover.sh cover.out 80` (newly extended to take path + threshold as args)
2. **`go-lint`**:
   * `golangci-lint run --config go/.golangci.yml`
3. **`cpp-format`**:
   * `clang-format --version`
   * `find firmware/ -name '*.cpp' -o -name '*.h' | xargs clang-format --dry-run --Werror`
4. **`proto-verify`**:
   * `bash proto/gen.sh`
   * `git diff --exit-code proto/` (regenerated code must match what's committed)
5. **`firmware-build-m5`** (ubuntu-latest, ESP-IDF container):
   * `espressif/esp-idf-docker` action
   * `cd firmware/m5 && idf.py build`
6. **`firmware-test-bridge`**:
   * `cd firmware/bridge && pio test`

Required status checks: all six. PRs blocked if any fail.

**Test:** none (CI config). Lint-checked by `actionlint` if installed.

**Commit:** `ci: add go-test, lint, format, proto, firmware-build matrix`

### 1.4 Task 0.4 — Protocol schema

**File:** `proto/tether.proto`

```protobuf
syntax = "proto3";
package tether.v1;
option go_package = "github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb";

// All packets on the LoRa link share this envelope.
message Envelope {
  fixed32 protocol_version = 1;     // = 1
  NodeId target_id = 2;              // 0xFFFF = broadcast
  NodeId sender_id = 3;
  bytes conversation_id = 4;         // 16 bytes (UUID)
  fixed32 message_id = 5;            // monotonic per (target, conversation)
  fixed32 seq_num = 6;               // chunk index, 0-based
  fixed32 total_seqs = 7;            // total chunks in this message
  MsgType msg_type = 8;              // START / DATA / END / ACK / TTS_DATA / TTS_END / UI_UPDATE
  AudioKind audio_kind = 9;          // 0=mic, 1=tts, 2=beep
  fixed32 flags = 10;                // bit0=RETRANSMIT, bit1=LAST_TTS_CHUNK
  bytes payload = 11;                // ≤ 227 bytes
  fixed32 header_crc = 12;           // CRC-16/CCITT, but stored as uint32
}

enum MsgType {
  MSG_TYPE_UNSPECIFIED = 0;
  MSG_TYPE_START = 1;
  MSG_TYPE_DATA = 2;
  MSG_TYPE_END = 3;
  MSG_TYPE_ACK = 4;
  MSG_TYPE_TTS_DATA = 5;
  MSG_TYPE_TTS_END = 6;
  MSG_TYPE_UI_UPDATE = 7;
}

enum AudioKind {
  AUDIO_KIND_UNSPECIFIED = 0;
  AUDIO_KIND_MIC = 1;
  AUDIO_KIND_TTS = 2;
  AUDIO_KIND_BEEP = 3;
}

// START payload
message StartInfo {
  AudioCodec codec = 1;
  fixed32 sample_rate_hz = 2;        // 8000
  fixed32 bitrate_bps = 3;           // 16000
  fixed32 duration_ms = 4;           // total recording length
  fixed32 payload_size_bytes = 5;    // bytes per chunk (post-fragmentation)
}

// ACK payload
message Ack {
  bytes conversation_id = 1;        // 16 bytes
  fixed32 message_id = 2;
  fixed32 next_expected_seq = 3;
  fixed32 ack_bitmap_lo = 4;         // 16 bits covering [next..next+15]
  fixed32 ack_bitmap_hi = 5;         // 16 bits covering [next+16..next+31]
  fixed32 crc = 6;
}

enum AudioCodec {
  AUDIO_CODEC_UNSPECIFIED = 0;
  AUDIO_CODEC_OPUS_8K_16KBPS = 1;
}

// UI_UPDATE payload — pushes a new conversation to the M5
message ConvInfo {
  bytes conversation_id = 1;
  string name = 2;                   // ≤ 24 chars (truncated by M5)
  ConvKind kind = 3;
  string target = 4;                // matrix room_id or forge session UUID
  bytes encryption_key = 5;          // 16 bytes; HKDF-derived
  fixed64 last_activity_unix_ms = 6;
  fixed32 unread_count = 7;
  bool remove = 8;                   // true = remove this conversation
}

enum ConvKind {
  CONV_KIND_UNSPECIFIED = 0;
  CONV_KIND_MATRIX = 1;
  CONV_KIND_FORGE = 2;
  CONV_KIND_BROADCAST = 3;
}

message NodeId { fixed32 value = 1; }
```

**Tests (TDD):** `go/pkg/protocol/protocol_test.go`

* `TestEnvelope_RoundTrip` — encode → decode → equality for a known-good envelope
* `TestEnvelope_MaxPayload` — payload exactly 227 bytes succeeds
* `TestEnvelope_OverSizedPayload` — payload 228 bytes is rejected at encode time
* `TestEnvelope_BadCRC` — decode of a 1-bit-flipped envelope returns `ErrBadCRC`
* `TestEnvelope_Truncated` — decode of a 5-byte buffer returns `ErrTruncated`
* `TestAck_BitmapEdges` — bitmap rolling from `next_expected_seq = 0xFFFFFFE0` to `0xFFFFFFFF` is correct
* `TestStartInfo_DurationField` — `DurationMs * (BitrateBps/8) / PayloadSizeBytes == TotalSeqs` for known inputs
* `TestConvInfo_TruncateName` — name > 24 chars is rejected with `ErrNameTooLong`
* **Fuzz test:** `FuzzEnvelopeDecode(data []byte)`. Seeds from `proto/testdata/fuzz_seed/*.bin` (10 known-good envelopes). Fuzz for 60 s on every PR.

**Implementation:** generated by `protoc` via `proto/gen.sh`. Hand-written helpers in `go/pkg/protocol/header.go`:
* `crc16ccitt(buf []byte) uint16` — bitwise CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no reflect, no xorout)
* `Encode(env *Envelope) ([]byte, error)` — calls generated marshal, computes CRC, appends
* `Decode(buf []byte) (*Envelope, error)` — checks length, checks CRC, calls generated unmarshal

**Commit:** `proto: define v1 tether protocol schema with TDD fuzz tests`

**Coverage gate after this task:** 100 % (this is a leaf module; trivial to fully cover).

### 1.5 Task 0.5 — Mock infrastructure (interfaces only, no impls)

**File:** `go/internal/radio/radio.go`

```go
// Package radio defines the LoRa radio interface and a mock implementation.
package radio

import (
    "context"
    "github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Radio is the abstract LoRa radio. Implementations: SX1262 (real), Mock (test).
type Radio interface {
    // Init configures the radio for the given preset. Idempotent.
    Init(ctx context.Context, preset Preset) error

    // Send queues a packet for transmission. Returns when the packet has
    // been handed to the radio (not when the air-time is complete).
    Send(ctx context.Context, env *protocolpb.Envelope) error

    // Receive blocks until a packet is available or ctx is canceled.
    // Returns io.EOF on context cancel.
    Receive(ctx context.Context) (*protocolpb.Envelope, error)

    // SetChannel switches to a new US915 channel. Takes effect on next TX/RX.
    SetChannel(ctx context.Context, ch Channel) error

    // Close releases the radio. Idempotent.
    Close() error
}

type Preset struct {
    SpreadingFactor uint8  // 7..12
    BandwidthHz     uint32 // 125000, 250000, 500000
    CodingRate      uint8  // 5..8 (meaning 4/5 .. 4/8)
    TxPowerDbm      int8   // -9..+22
    SyncWord        uint8  // 0xF3 for private
}

type Channel struct {
    Index uint8  // 0..63
    Hz    uint64 // computed from index
}
```

**Test:** `go/internal/radio/mock_test.go`

* `TestMockRadio_SendReceive` — send one envelope, receive it back
* `TestMockRadio_SendDropsOnFullQueue` — mock has `MaxQueueSize=2`; third send returns `ErrQueueFull`
* `TestMockRadio_ConcurrentSendReceive` — race-detector clean with 100 goroutines
* `TestMockRadio_ChannelSwitch` — SetChannel is observable on next TX
* `TestMockRadio_ContextCancel` — Receive returns `io.EOF` when ctx is canceled
* `TestMockRadio_CloseIdempotent` — calling Close twice does not panic

**Implementation:** `go/internal/radio/mock.go` (~ 80 lines). Backing storage is a `chan *protocolpb.Envelope` with configurable buffer; optional artificial delay to simulate airtime.

**Commit:** `radio: define Radio interface + mock with race-detector tests`

**Coverage gate:** 100 % on the mock.

### 1.6 Task 0.6 — Documentation baseline

**File:** `docs/TESTING.md`

Sections (≈ 100 lines):
1. TDD discipline (links to `IMPLEMENTATION.md` §0.1)
2. How to run tests: `go test ./...`, `go test -race ./...`, `go test -cover ./...`
3. How to write a fuzzer: `FuzzXxx` function shape, `testdata/fuzz/<seed>` corpus
4. Coverage gates: 80 % statement, `scripts/cover.sh` enforcement
5. Mocking: list of all interfaces, where to find the mocks
6. CI: required status checks, how to read a failure
7. Common gotchas (see `AGENTS.md` §9)

**File:** `docs/ARCHITECTURE.md`

Sections (≈ 200 lines):
1. System diagram (ASCII art, same as `README.md` but more detail)
2. Component map with one-line purpose for each component
3. Wire format overview (link to `research.md` §8 and `proto/tether.proto`)
4. State machines (link to `research.md` §10 for PTT, §7.1 for FreeRTOS)
5. Data flow: voice-in, text-in, voice-out, text-out
6. Failure modes and recovery (link to `research.md` §16)

**Tests:** none.

**Commit:** `docs: add TESTING.md and ARCHITECTURE.md baselines`

---

## 2. Phase 1 — Go data plane (loopback)

**Goal:** All Go code for the wire format, fragmentation, and ACK state machine is implemented, fully unit-tested, with a loopback test that exercises the full round-trip across two in-process Go routines acting as "bridge emulator" and "M5 emulator". No embedded work, no hardware, no real serial port.

**Exit criteria:**
* `go test -race ./...` passes with 100 % success
* `scripts/cover.sh cover.out 80` reports ≥ 80 % coverage across `./...`
* `go test -fuzz=FuzzEnvelopeDecode -fuzztime=300s` runs clean for 5 minutes
* `go run ./tools/tether-loopback` sends a 60 s synthetic audio blob from "M5" to "bridge" to "STT pipeline" and back, with no loss, in < 10 s wall-clock on a laptop
* `go vet ./...`, `golangci-lint run` all clean

### 2.1 Task 1.1 — CRC + envelope encode/decode

**Files:**

| Path | Lines | Purpose |
|---|---|---|
| `go/pkg/protocol/crc.go` | 1–30 | `crc16ccitt(b []byte) uint16` table-driven impl |
| `go/pkg/protocol/header.go` | 1–60 | `Encode`, `Decode`, errors |
| `go/pkg/protocol/crc_test.go` | 1–80 | table-driven tests for CRC, including the official CRC-16/CCITT-FALSE test vector 0xFFFF → 0 |
| `go/pkg/protocol/header_test.go` | 1–150 | round-trip, size, CRC mismatch, truncation |

**Tests first (red):**

```go
// crc_test.go
func TestCrc16CCITT_KnownVectors(t *testing.T) {
    cases := []struct{ in []byte; want uint16 }{
        {[]byte{}, 0xFFFF},
        {[]byte{0x00}, 0xE1F0},
        {[]byte{0xFF, 0xFF, 0xFF, 0xFF}, 0x1B2A},
        // ... 10+ vectors from https://www.lammertbies.nl/comm/info/crc-calculation
    }
    for _, c := range cases { /* assert */ }
}

// header_test.go
func TestEnvelope_RoundTrip(t *testing.T) { /* encode, decode, assert equal */ }
func TestEnvelope_RejectsPayloadOverMax(t *testing.T) { /* 228 bytes → ErrPayloadTooLarge */ }
func TestEnvelope_DetectsBitFlip(t *testing.T) { /* flip 1 bit, decode returns ErrBadCRC */ }
```

**Implementation (green):** trivial, ~ 60 LOC.

**Coverage target:** 100 % on `crc.go`, ≥ 95 % on `header.go` (proto-generated code is excluded from coverage).

**Commit:** `protocol: crc-16/ccitt + envelope encode/decode with TDD tests`

### 2.2 Task 1.2 — Fragmentation

**File:** `go/pkg/protocol/fragment.go` (≈ 200 LOC)

Public API:

```go
// Fragment splits a payload into a sequence of Envelopes ready for transmission.
// Returns the Envelopes in order; seq_num is 0-based; total_seqs is set on all.
// The caller appends START/END control messages around the sequence.
func Fragment(payload []byte, msgID uint32, convID []byte, msgType protocolpb.MsgType, audioKind protocolpb.AudioKind) ([]*protocolpb.Envelope, error)

// Reassemble is the inverse: given a sorted slice of Envelopes with the same
// (message_id, conversation_id), validate the sequence and concatenate payloads.
func Reassemble(envs []*protocolpb.Envelope) ([]byte, error)

var (
    ErrOutOfOrder   = errors.New("fragment out of order")
    ErrMissingChunk = errors.New("missing chunk in sequence")
    ErrDuplicateSeq = errors.New("duplicate seq_num")
    ErrSeqMismatch  = errors.New("seq_num does not match expected")
)
```

**Tests first:**

* `TestFragment_EmptyPayload` — 0 bytes → 0 envelopes, no error
* `TestFragment_SingleChunk` — 100 bytes → 1 envelope, `total_seqs=1`
* `TestFragment_MultipleChunks` — 1000 bytes → 5 envelopes, `total_seqs=5`, sizes [227, 227, 227, 227, 92]
* `TestFragment_ExactlyMaxPerChunk` — 227 bytes → 1 envelope
* `TestFragment_OneOverMax` — 228 bytes → error
* `TestReassemble_Happy` — 5 envelopes in order → 1000 bytes
* `TestReassemble_OutOfOrder` — 5 envelopes shuffled → ErrOutOfOrder
* `TestReassemble_MissingChunk` — 4 of 5 envelopes → ErrMissingChunk, indicates which seq
* `TestReassemble_Duplicate` — same seq twice → ErrDuplicateSeq
* `TestReassamble_TotalSeqsMismatch` — 5 envelopes but `total_seqs=6` on first → ErrSeqMismatch
* `TestRoundTrip_RandomSizes` — property test: random sizes 1–1 MB, fragment → reassemble → equal
* `TestFragment_StableSeqOrder` — 1000 chunks, sort by seq_num, must be in insertion order

**Implementation (green):** straightforward; ~ 100 LOC of slicing.

**Property test (use `testing/quick`):**

```go
func TestProperty_FragmentReassemble(t *testing.T) {
    f := func(payload []byte) bool {
        if len(payload) > 1<<20 { return true } // skip huge
        envs, err := Fragment(payload, 1, []byte("conv-uuid-1234"), protocolpb.MsgType_DATA, protocolpb.AudioKind_MIC)
        if err != nil { return false }
        got, err := Reassemble(envs)
        return err == nil && bytes.Equal(got, payload)
    }
    if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
        t.Fatal(err)
    }
}
```

**Coverage target:** 100 %.

**Commit:** `protocol: fragmentation + reassembly with property-based tests`

### 2.3 Task 1.3 — Cumulative bitmap ACK

**File:** `go/pkg/protocol/ack.go` (≈ 150 LOC)

```go
// AckBitmap is a 32-bit rolling ACK window starting at NextExpectedSeq.
type AckBitmap struct {
    NextExpectedSeq uint32
    Bitmap          uint32 // bit i set ⇒ seq (NextExpectedSeq + i) is acked, i ∈ [0, 31]
}

// Set marks seq as acked. Returns true if seq is in window [Next, Next+31].
// Returns false if seq is below window (re-transmit not needed) or above (window
// advanced; caller should rebase).
func (a *AckBitmap) Set(seq uint32) (inWindow bool, advanced bool)

// Has reports whether seq is acked.
func (a *AckBitmap) Has(seq uint32) bool

// Encode returns the 8-byte on-the-wire payload (next + lo + hi).
func (a *AckBitmap) Encode() (next, lo, hi uint32)

// DecodeAck parses the on-the-wire payload.
func DecodeAck(payload []byte) (AckBitmap, error)
```

**Tests first:**

* `TestAckBitmap_SetInWindow` — Next=0, Set(5) → Has(5)=true, Next stays 0
* `TestAckBitmap_RebaseAdvancesNext` — Next=0, Set(0,1,2,3,...,31) → Next=32
* `TestAckBitmap_SetBelowWindow` — Next=10, Set(5) → returns false, no change
* `TestAckBitmap_SetAboveWindow_Advance` — Next=10, Set(50) → returns true, true; rebase to 18 (next un-acked after 50)
* `TestAckBitmap_EncodeDecode` — known bitmap, encode, decode, equal
* `TestAckBitmap_Full` — all 32 bits set
* `TestAckBitmap_Wraparound` — Next=0xFFFFFFF0, Set(0xFFFFFFF0..0xFFFFFFFF) → Next wraps to 0x00000010 (uint32)

**Coverage target:** 100 %.

**Commit:** `protocol: cumulative 32-bit ack bitmap with wraparound tests`

### 2.4 Task 1.4 — Sender state machine

**File:** `go/internal/radio/sender.go` (≈ 250 LOC)

```go
type Sender struct {
    radio     radio.Radio
    convID    []byte
    msgID     uint32
    envs      []*protocolpb.Envelope  // pre-fragmented
    acks      *protocol.AckBitmap
    timeout   time.Duration
    maxRetry  int
    onAcked   func(seq uint32)
    onFailed  func(env *protocolpb.Envelope, retries int)
    onSuccess func()  // all acked
    logger    *slog.Logger
}

// NewSender builds a sender for a pre-fragmented sequence.
//   envs must already be Fragmented; the Sender does not re-fragment.
func NewSender(r radio.Radio, envs []*protocolpb.Envelope, opts ...SenderOpt) *Sender

// Run blocks until all envelopes are acked or one exceeds maxRetry.
// Returns the count of acked envelopes, the first failed envelope (if any),
// and the total retry count.
func (s *Sender) Run(ctx context.Context) (acked int, failed *protocolpb.Envelope, retries int, err error)
```

State machine: `IDLE → TX(seq) → WAIT_ACK → (ack received | timeout) → TX(next) | RETRY | DONE/FAILED`.

**Tests first (using the mock radio):**

* `TestSender_HappyPath` — 5 envelopes, all acked immediately → 5 acked, 0 retries
* `TestSender_OneRetry` — 5 envelopes, 1 timeout then ack → 1 retry, 5 acked
* `TestSender_MaxRetries` — same envelope times out 5 times → failed set, Run returns
* `TestSender_ContextCancel` — cancel mid-wait → returns ctx.Err(), no further TX
* `TestSender_OutOfOrderAck` — ack for seq 3 before seq 2 → ignored; sender continues
* `TestSender_DuplicateAck` — same ack arrives twice → no double-count
* `TestSender_Pacing` — after an ack for seq N, only seq N+1 is sent next (no reordering)
* `TestSender_RaceDetector` — 10 goroutines, 100 messages each, no data race
* `TestSender_CallbacksFire` — onAcked, onFailed, onSuccess fire in order

**Implementation (green):** event loop driven by a `select` on (radio.Receive, time.After, ctx.Done). Internal slice tracks per-envelope retry count.

**Coverage target:** ≥ 90 %.

**Commit:** `radio: sender state machine with retry, timeout, ack bitmap`

### 2.5 Task 1.5 — Receiver state machine

**File:** `go/internal/radio/receiver.go` (≈ 250 LOC)

```go
type Receiver struct {
    radio    radio.Radio
    convs    map[string]*reassemblyState  // keyed by conversation_id
    onMsg    func(msg *IncomingMessage)
    onAck    func(ack *OutgoingAck)
    logger   *slog.Logger
}

type IncomingMessage struct {
    ConversationID []byte
    MessageID      uint32
    Payload        []byte
    AudioKind      protocolpb.AudioKind
    CompletedAt    time.Time
}

type OutgoingAck struct {
    ConversationID []byte
    MessageID      uint32
    NextExpected   uint32
    Bitmap         uint32
}

// Run blocks until ctx is canceled, dispatching complete messages to onMsg
// and partial acks to onAck.
func (r *Receiver) Run(ctx context.Context) error
```

State per message: `RECEIVING (chunks_so_far) → COMPLETE (all total_seqs received) → EMITTED (handed to onMsg)`.

**Tests first:**

* `TestReceiver_HappyPath` — 5 envelopes in order, START, then 5 DATA, then END → one IncomingMessage with full payload
* `TestReceiver_DuplicateData` — same seq twice → second ignored, no error
* `TestReceiver_MissingChunk` — DATA seq 2 missing → no emit after 100 ms timeout
* `TestReceiver_OutOfOrder` — DATA arrives in order 1, 0, 2 → 0 and 1 buffered, 2 completes the message
* `TestReceiver_EmitsAckAfterEachChunk` — after each DATA, onAck called with bitmap containing that seq
* `TestReceiver_MultipleMessagesConcurrent` — two message_ids interleaved → both emit in order
* `TestReceiver_GarbageConversationID` — 15-byte conv_id rejected (must be 16)
* `TestReceiver_StartWithoutData` — START only, no DATA within 5 s → message abandoned, no emit
* `TestReceiver_RaceDetector` — concurrent emits safe

**Coverage target:** ≥ 90 %.

**Commit:** `radio: receiver state machine with per-conversation reassembly`

### 2.6 Task 1.6 — In-process loopback transport

**File:** `go/internal/serial/loopback.go` (≈ 100 LOC)

A `Port` interface that wraps a `Radio` and lets two "devices" (M5 emulator and bridge emulator) talk to each other through an in-memory pipe that simulates airtime, drop, and reorder.

```go
type Port interface {
    radio.Radio  // inherits Send/Receive/Close
    SetPacketLoss(float64)  // 0.0..1.0
    SetReorderProbability(float64)
    SetBaseLatency(time.Duration)
    SetJitter(time.Duration)
}
```

**Tests:**

* `TestLoopback_RoundTrip` — 100 packets, 0% loss, all delivered in order
* `TestLoopback_PacketLoss` — 100 packets, 10% loss → ~90 delivered; receiver times out on the lost
* `TestLoopback_Reorder` — 5% reorder probability → eventual delivery, but out of order
* `TestLoopback_ConcurrentClose` — two goroutines, one closes mid-send → no panic

**Implementation:** a `*loopbackPort` that wraps a buffered `chan *protocolpb.Envelope`. The "wire" is just a goroutine that drains the chan and delivers to the receiver's chan with optional artificial delay, drop, and reorder (using a small reorder buffer).

**Commit:** `serial: in-process loopback port with loss/reorder simulation`

### 2.7 Task 1.7 — End-to-end loopback tool

**File:** `go/tools/tether-loopback/main.go` (≈ 200 LOC)

Two modes:
* `tether-loopback send` — pretends to be the M5: records a synthetic audio blob (sine wave of configurable frequency/duration), Opus-encodes (mock codec for v1), fragments, sends.
* `tether-loopback recv` — pretends to be the bridge: listens, reassembles, decodes (mock), prints stats.

**Tests:**

* `TestLoopback_RoundTrip_60sSyntheticAudio` — generates 60 s of 440 Hz sine at 8 kHz, "sends" via loopback, "receives", verifies equal (under tolerance for Opus mock).
* `TestLoopback_Stats` — counts sent, received, acked, retries; all match expected.

**Commit:** `tools: tether-loopback CLI for Phase 1 end-to-end test`

### 2.8 Task 1.8 — Codec wrapper (mock + interface)

**File:** `go/internal/codec/opus.go` (≈ 150 LOC)

```go
type Opus interface {
    Encode(pcm []int16) (opus []byte, err error)
    Decode(opus []byte) (pcm []int16, err error)
    FrameSize() int  // 160 samples at 8 kHz
    SampleRate() int // 8000
    Close() error
}
```

Two implementations:
* `mockOpus` — identity codec: pcm → raw bytes, opus → pcm. Used in unit tests.
* `realOpus` — cgo wrapper around `opus_multistream_encode_float` (libopus). Empty file in Phase 1; cgo build in Phase 5.

**Tests for mock:**

* `TestMockOpus_RoundTrip` — encode → decode → equal
* `TestMockOpus_FrameSize` — 160 samples
* `TestMockOpus_EmptyInput` — 0 samples → empty output, no error
* `TestMockOpus_OversizeFrame` — 320 samples → error (must be exact multiples of 160)
* `TestMockOpus_Concurrent` — race-detector clean

**Commit:** `codec: opus interface + mock (real impl deferred to Phase 5)`

### 2.9 Task 1.9 — Phase 1 exit gate

* `go test -race -coverprofile=cov.out ./...`
* `scripts/cover.sh cov.out 80` — must report ≥ 80 %
* `go test -fuzz=FuzzEnvelopeDecode -fuzztime=300s` — clean
* `go run ./tools/tether-loopback` — 60 s round-trip succeeds
* `golangci-lint run` — clean

If any fails, fix and re-run. Coverage must be ≥ 80 % before the phase can be closed. If coverage is below, add tests, do not delete code.

**Commit:** `chore(phase-1): coverage gate passes, fuzz clean, lint clean`

**Phase 1 PR:** squash-merge `phase/1-data-plane` → `main`.

---

## 3. Phase 2 — RAK4631 bridge firmware

**Goal:** The bridge is fully functional. Speaks the `proto/tether.proto` envelope over LoRa on one side, the line-framed binary protocol over USB-Serial on the other side. C++ implementation, tested with `pio test` on the bench.

**Exit criteria:**
* `pio test` passes with ≥ 80 % line coverage per component
* `idf.py build` for the bridge binary succeeds
* Bench test: bridge connected to PC, M5 with a "ping" sketch, sees 100 % round-trip for 100 packets at 1 m distance
* `clang-format --dry-run` clean on all bridge files
* `cppcheck` clean

### 3.1 Task 2.1 — Frame protocol over USB-Serial

**Files:** `firmware/bridge/src/frame.h`, `frame.cpp`, `frame_test.cpp`

```cpp
namespace tether::bridge {

constexpr uint8_t kMagic0 = 0xAA;
constexpr uint8_t kMagic1 = 0x55;
constexpr uint16_t kMaxFrameSize = 256;

enum class FrameType : uint8_t {
    kTxDone      = 0x01,
    kRxPacket    = 0x02,
    kAck         = 0x03,
    kCadResult   = 0x04,
    kSetConfig   = 0x10,
    kLog         = 0x80,
    kError       = 0xFF,
};

struct Frame {
    FrameType type;
    std::array<uint8_t, 2> length;  // LE
    std::vector<uint8_t> payload;   // 0..65535 bytes
    std::array<uint8_t, 2> crc;     // LE, CRC-16/CCITT over type..payload
};

// Encode a Frame to bytes for transmission over Serial.
// Throws std::invalid_argument if payload > 65535 bytes.
std::vector<uint8_t> EncodeFrame(const Frame& f);

// Decode bytes from Serial into a Frame. Returns nullopt on bad magic,
// bad length, bad CRC, or truncated.
std::optional<Frame> DecodeFrame(std::span<const uint8_t> bytes);

// Streaming decoder: accumulates bytes, emits complete frames.
class FrameDecoder {
public:
    void Feed(std::span<const uint8_t> bytes);
    std::optional<Frame> Next();
};

}  // namespace tether::bridge
```

**Tests first (Unity):**

```cpp
// test_frame.cpp
void test_encode_decode_round_trip();      // 10 frames of varying size
void test_decode_rejects_bad_magic();      // 0xAA 0x56 → nullopt
void test_decode_rejects_bad_crc();        // flip 1 bit → nullopt
void test_decode_rejects_truncated();      // 5 bytes when 7 expected
void test_decode_streaming_partial();      // feed 3, then 4 bytes → 1 frame emitted
void test_decode_streaming_two_frames();   // 14 bytes fed across 3 chunks → 2 frames
void test_encode_rejects_oversized();      // 70_000 byte payload → throws
```

**Implementation (green):** ~ 100 LOC. Streaming decoder is a small state machine: WAIT_MAGIC0 → WAIT_MAGIC1 → WAIT_TYPE → WAIT_LEN_LO → WAIT_LEN_HI → WAIT_PAYLOAD → WAIT_CRC_LO → WAIT_CRC_HI → emit.

**Coverage target:** 100 % on `frame.cpp`.

**Commit:** `bridge: line-framed binary protocol with streaming decoder`

### 3.2 Task 2.2 — RadioLib wrapper

**Files:** `firmware/bridge/src/lora.h`, `lora.cpp`, `lora_test.cpp`

```cpp
namespace tether::bridge {

class LoRaRadio {
public:
    LoRaRadio(int8_t pin_nss, int8_t pin_reset, int8_t pin_dio1, int8_t pin_busy);

    void Init(radio::Preset preset);
    void SetChannel(uint8_t ch);
    bool StartCAD();
    void Transmit(std::span<const uint8_t> packet);
    std::optional<std::vector<uint8_t>> ReceiveBlocking(uint32_t timeout_ms);
    void Sleep();
    void Standby();

private:
    RADIOLIB_SX1262 radio_;
    int8_t pin_busy_;
};

}  // namespace tether::bridge
```

**Tests:** Most are integration tests on real hardware. Unit tests cover the **non-RadioLib** parts:

* `test_lora_set_channel_frequency` — channel 0 → 902.3 MHz, ch 1 → 902.425, …, ch 63 → 914.9
* `test_lora_preset_sf11_bw125` — preset struct fields map to RadioLib setSpreadingFactor, setBandwidth, setCodingRate
* `test_lora_busy_pin_polled_before_xfer` — verify sequence: read BUSY, then CS low, then transfer (mock SPI)

The RadioLib calls themselves are tested on hardware in the bench test.

**Commit:** `bridge: LoRa radio wrapper with channel + preset mapping`

### 3.3 Task 2.3 — Serial link task

**Files:** `firmware/bridge/src/serial_link.h`, `serial_link.cpp`, `serial_link_test.cpp`

A FreeRTOS task that:
1. Reads from `Serial` (USB) into the `FrameDecoder`.
2. On `kSetConfig`: applies to LoRaRadio.
3. On `kAck`: queues for TX.
4. Reads from LoRaRadio.ReceiveBlocking.
5. Encodes packet as `kRxPacket` frame, writes to `Serial`.

Uses a `QueueHandle_t<Frame>` for inbound and another for outbound.

**Tests:**

* `test_serial_link_rx_packet_to_serial` — feed LoRa a packet → Serial output is `0xAA 0x55 0x02 <len> <data> <crc>`
* `test_serial_link_serial_to_tx` — feed `kAck` frame to Serial → LoRa TX fires
* `test_serial_link_cad_result_to_serial` — internal CAD completes → Serial gets `kCadResult` frame

**Implementation:** ~ 150 LOC. Tasks pinned to core 0. Priorities: radio (high), serial (med-high), watchdog (low).

**Commit:** `bridge: serial link FreeRTOS task with frame ↔ radio bridge`

### 3.4 Task 2.4 — Main + watchdog

**Files:** `firmware/bridge/src/main.cpp`

* `setup()`: init serial, init LoRa (SF11/BW125/CR4/8, sync 0xF3, ch 0, +20 dBm), start tasks, enable watchdog.
* `loop()`: feed watchdog every 1 s; nothing else.

**Tests:** N/A (sketch is glue code).

**Commit:** `bridge: main + watchdog feeder`

### 3.5 Task 2.5 — Bench test rig

**File:** `firmware/bridge/test/test_bench.cpp` (PlatformIO, `pio test -e native` then `pio test -e rak4631`)

* `test_native_loopback` — runs the same code on Linux, mock SPI, mock radio (faked `RadioLib` via interface) — full round-trip 100 packets, no loss
* `test_rak4631_real_radio` — runs on the real RAK4631 with two nodes, 1 m apart, 100 packets

**Commit:** `bridge: bench test (native + on-device)`

### 3.6 Task 2.6 — Phase 2 exit gate

* `pio test` all pass
* Coverage ≥ 80 % per component
* `clang-format --dry-run` clean
* `cppcheck` clean
* Hardware smoke test: ping the bridge from a Go tool, see response

**PR:** `phase/2-bridge` → `main`

---

## 4. Phase 3 — M5 firmware skeleton

**Goal:** The M5 boots, captures audio on PTT, encodes to Opus, saves to SD, fragments and transmits. Beep tones work. No EPD, no multi-conversation yet — straight PTT → TX → done.

**Exit criteria:**
* `idf.py build` succeeds, binary < 1.5 MB
* `idf.py -p /dev/ttyUSB0 flash` deploys
* `pio test` on every component (host-side native build) ≥ 80 % coverage
* Hardware smoke: press PTT, record 5 s, release, see LED blink during TX, see packet on the bridge
* Watchdog does not trigger under any tested scenario
* EPD displays "boot OK" on idle

### 4.1 Task 3.1 — SPI bus mutex

**Files:** `firmware/m5/components/spi_bus/spi_bus.h`, `spi_bus.cpp`, `test/test_spi_bus.cpp`

```cpp
namespace tether::m5 {

class SpiBus {
public:
    SpiBus(spi_host_device_t host, gpio_num_t pin_mosi, gpio_num_t pin_miso, gpio_num_t pin_sclk);
    void AddDevice(int cs_pin, int clock_hz, int queue_size = 4);
    spi_device_handle_t Handle(int cs_pin) const;
    void Lock();
    void Unlock();
private:
    SemaphoreHandle_t mutex_;
    std::map<int, spi_device_handle_t> handles_;
};

extern SpiBus& Bus();  // singleton

}  // namespace tether::m5
```

**Tests:**

* `test_spi_bus_init` — initializes with 3 devices (SD, LoRa, EPD), all handles non-null
* `test_spi_bus_lock_unlock` — recursive lock NOT allowed (returns false)
* `test_spi_bus_lock_blocks_other_core` — core 0 takes lock, core 1 try-lock blocks
* `test_spi_bus_lookup` — `Handle(SD_CS)` returns the same handle each time
* `test_spi_bus_unknown_device` — `Handle(99)` returns null

**Commit:** `m5: SPI bus singleton with mutex and per-CS device handles`

### 4.2 Task 3.2 — SX1262 driver

**Files:** `firmware/m5/components/lora_sx1262/lora_sx1262.h`, `lora_sx1262.cpp`, `test/test_lora_sx1262.cpp`

A thin wrapper that:
* Configures BUSY pin (input), IRQ pin (input edge), CS pin (output high), RST pin (output high)
* Implements `Init`, `SetChannel`, `StartCAD`, `Transmit`, `ReceiveBlocking`, `Sleep`
* All RadioLib calls are wrapped in `Bus().Lock()` / `Bus().Unlock()`

**Tests:**

* `test_lora_init_sets_preset` — after Init(SF11, BW125, CR4/8), registers match
* `test_lora_set_channel_0` — ch 0 → 902.3 MHz
* `test_lora_set_channel_63` — ch 63 → 914.9 MHz
* `test_lora_cad_returns_busy_or_clear` — CAD returns within 100 ms either way
* `test_lora_transmit_blocks_until_done` — TX of 100-byte packet blocks ≤ 200 ms
* `test_lora_sleep_lowers_current` — after Sleep, current draw measurable on test rig

Most tests run on hardware in the bench rig. Unit tests on host mock the SPI bus with a fake that records byte sequences.

**Commit:** `m5: SX1262 driver with SPI mutex integration`

### 4.3 Task 3.3 — SD card (LittleFS mount)

**Files:** `firmware/m5/components/sd_card/sd_card.h`, `sd_card.cpp`, `test/test_sd_card.cpp`

Uses `esp_littlefs` with the SD card SPI device. Provides:

```cpp
class SdCard {
public:
    esp_err_t Mount();   // idempotent
    esp_err_t Unmount();
    FILE* Open(const char* path, const char* mode);  // returns NULL on error
    int Remove(const char* path);
    int Rename(const char* from, const char* to);
    size_t TotalBytes();
    size_t FreeBytes();
};
```

**Tests (host-side with a tmpfs):**

* `test_sd_mount_unmount` — Mount succeeds, Unmount succeeds
* `test_sd_open_close` — Open existing file returns non-null handle
* `test_sd_open_missing` — Open missing file returns NULL
* `test_sd_write_read` — Write 1 KB, read back, equal
* `test_sd_remove` — Remove existing file succeeds
* `test_sd_rename` — Rename existing file succeeds
* `test_sd_freespace` — FreeBytes > 0 after mount

**Commit:** `m5: SD card LittleFS mount with POSIX file API`

### 4.4 Task 3.4 — PSRAM ring buffer (SPSC)

**Files:** `firmware/m5/components/psram_ring/psram_ring.h`, `psram_ring.cpp`, `test/test_psram_ring.cpp`

```cpp
class PsramRing {
public:
    // capacity must be a power of 2
    PsramRing(size_t capacity);
    // Returns number of bytes written (0 if full).
    size_t Write(const uint8_t* data, size_t len);
    // Returns number of bytes read (0 if empty).
    size_t Read(uint8_t* out, size_t len);
    // Returns number of bytes available to read.
    size_t Available() const;
    size_t Capacity() const { return capacity_; }
private:
    uint8_t* buf_;
    std::atomic<size_t> head_;  // writer
    std::atomic<size_t> tail_;  // reader
    size_t capacity_;
};
```

**Tests:**

* `test_ring_write_read_round_trip` — write 1 KB, read 1 KB, equal
* `test_ring_write_full` — write capacity, second write returns 0
* `test_ring_read_empty` — read empty returns 0
* `test_ring_wraparound` — write/read cycles 1000 times, final read matches last write
* `test_ring_concurrent_spsc` — race-detector clean (host side uses threads; embedded uses FreeRTOS task pair)
* `test_ring_partial_reads` — write 1 KB, read 256 bytes four times
* `test_ring_is_power_of_2` — constructor rejects non-power-of-2

**Commit:** `m5: PSRAM ring buffer (single-producer, single-consumer)`

### 4.5 Task 3.5 — Opus encoder

**Files:** `firmware/m5/components/opus_enc/opus_enc.h`, `opus_enc.cpp`, `test/test_opus_enc.cpp`

Wraps `micro-opus` (or `esp-libopus`).

```cpp
class OpusEncoder {
public:
    OpusEncoder(int sample_rate = 8000, int bitrate = 16000, int complexity = 5);
    ~OpusEncoder();
    // PCM: int16, mono, 160 samples (20 ms at 8 kHz).
    // Returns encoded bytes; length varies (VBR).
    std::vector<uint8_t> EncodeFrame(const int16_t* pcm);
    int FrameSize() const { return 160; }
    int SampleRate() const { return sample_rate_; }
private:
    OpusEncoder* enc_;  // Opaque pointer to libopus state
    int sample_rate_;
};
```

**Tests (host-side with libopus installed):**

* `test_opus_init` — constructor returns non-null
* `test_opus_encode_zero_pcm` — silent frame encodes to ≤ 30 bytes
* `test_opus_encode_sine` — 440 Hz sine encodes to 30–60 bytes
* `test_opus_encode_60s_voice` — 60 s of synthetic speech (sine + envelope), total ≤ 130 KB
* `test_opus_frame_size_constant` — every EncodeFrame call processes exactly 160 samples
* `test_opus_concurrent_encode` — race-detector clean (though not designed for concurrent use, lock the door)

**Commit:** `m5: Opus encoder wrapper with bitrate/sample-rate/frame-size config`

### 4.6 Task 3.6 — I2S mic + amp (shared full-duplex bus)

**Files:** `firmware/m5/components/i2s_mic/i2s_mic.h`, `i2s_mic.cpp`, `i2s_amp/i2s_amp.h`, `i2s_amp.cpp`, `test/test_i2s_amp.cpp`

* `i2s_mic` and `i2s_amp` share a **single I2S0 bus in full-duplex
  mode**. The mic and amp both use the same BCLK and WS signals;
  the mic drives its SD line into the ESP32's DIN, and the amp
  reads the ESP32's DOUT.
* Pin map (from `board.h`):
  * **WS (LRC)**: GPIO 12 (shared)
  * **BCLK (SCK)**: GPIO 10 (shared)
  * **Mic SD (DIN)**: GPIO 18
  * **Amp DIN (DOUT)**: GPIO 9
* **REQUIRES 3 HARDWARE MODS.** The M5 has only one natively free
  pin (GPIO 18). To free GPIO 9, 10, 12 we have to:
  1. Bypass the L76K load switch and sever the trace back to
     GPIO 10 (the GPS "Always-On" hack).
  2. Desolder the SMD buzzer (frees GPIO 9).
  3. Sever the trace from the USB voltage divider to GPIO 12
     (frees GPIO 12 for the WS line).
  See `docs/HARDWARE-MODS.md` for the full execution plan.
* `i2s_amp` also has `PlayTone(freq_hz, duration_ms)` for beep
  tones (sine generator).
* Both classes share a single set of `g_i2s_tx_handle` /
  `g_i2s_rx_handle` globals (defined in `i2s_amp.cpp`).
  Whichever `Init()` runs first creates them; the second is a
  no-op.

**Tests:**

* `test_amp_sine_440hz` — generate 440 Hz, 100 ms, captures a 100 ms buffer, FFT shows peak at 440 ± 5 Hz
* `test_amp_sine_1khz` — same, 1 kHz
* `test_amp_silence_when_not_playing` — buffer is zero
* `test_amp_concurrent_play_stop` — Start, then Stop, buffer zeros immediately

**Commit:** `m5: I2S mic capture + amp playback with tone generator`

### 4.7 Task 3.7 — Buttons (PTT, Menu) with long-press

**Files:** `firmware/m5/components/buttons/buttons.h`, `buttons.cpp`, `test/test_buttons.cpp`

> **Note (v0.1.0):** The ThinkNode M5 has **2 physical buttons** (not
> 3). The third "control" on the case is a GPS *switch* (slider),
> not a button — see the Meshtastic variant.h for this board which
> defines `PIN_BUTTON1=21` and `PIN_BUTTON2=14` and no third
> `PIN_BUTTON3`. The v0.1.0 button model:
> - A (GPIO 21) = PTT.
> - B (GPIO 14) = Menu / cycle (short = next conv, long = settings).
> - The 3rd "Prev" button from earlier drafts does not exist. Inside
>   the settings menu, kPtt acts as the "back / decrease" affordance.
>   See AGENTS.md §3.4, hardware.md §1.1, and the comment block at
>   the top of `buttons.h`.

```cpp
enum class Button { kPtt, kMenu };                // 2 physical buttons
enum class Event {
    kPress, kRelease,
    kLongPressPtt,    // 3 s hold
    kLongPressMenu,   // 2 s hold (settings entry / exit)
};
// Backwards-compat aliases: kNext == kMenu, kLongPressNext ==
// kLongPressMenu. Pre-v0.1.0 code that referenced kPrev has been
// updated; new code should use kMenu.
class Buttons {
public:
    using Handler = std::function<void(ButtonEvent)>;
    bool Init(Handler h);
private:
    static void IRAM_ATTR IsrPtt(void*);
    static void IRAM_ATTR IsrMenu(void*);
    void DebounceTask();
    Handler handler_;
    QueueHandle_t events_;
    // Pin map in firmware/m5/components/board/include/board.h.
    // kButtonCount = 2.
};
```

**Tests:**

* `test_buttons_press_release` — simulate press, debounce expires, event fires
* `test_buttons_debounce` — 50 ms bouncing, only one event fires
* `test_buttons_long_press_ptt` — 3 s hold → kLongPressPtt fires
* `test_buttons_long_press_next` — 2 s hold → kLongPressMenu fires (alias)
* `test_buttons_menu_long_press` — exercises the kMenu alias
* `test_buttons_release_after_long_press` — no kRelease event after long press

Host-side tests inject virtual GPIO events.

**Commit:** `m5: button handling with debounce and long-press detection`

### 4.8 Task 3.8 — FreeRTOS task skeleton

**Files:** `firmware/m5/components/audio_capture/`, `storage_flush/`, `radio_task/`, `ptt/`, `ui_state/`, `power_mgmt/`, `watchdog/`

Each task: header, source, test. Per `research.md` §7.1.

**TDD order:**

1. **`ptt` state machine** (≈ 200 LOC, ≈ 12 tests):
   * `test_ptt_idle_to_recording_on_press` — Press A → state=RECORDING
   * `test_ptt_recording_to_queued_on_release` — Release A → state=QUEUED
   * `test_ptt_recording_to_idle_on_long_press` — A held 3s → state=IDLE
   * `test_ptt_queued_to_transmitting` — when radio task accepts → state=TRANSMITTING
   * `test_ptt_transmitting_to_acked` — on all-chunks-acked → state=ACKED
   * `test_ptt_acked_to_idle_after_2s` — auto-clear
   * `test_ptt_transmitting_to_failed` — on retry budget exceeded
   * `test_ptt_cancel_during_transmitting` — A held 3s → state=CANCELED
   * `test_ptt_no_press_during_tts_playback` — TTS state suppresses PTT
   * `test_ptt_illegal_transitions_rejected` — RECORDING → ACKED is a no-op

2. **`audio_capture` task** (≈ 150 LOC, ≈ 6 tests):
   * `test_capture_writes_to_ring` — 1 s of synthetic PCM produces 50 Opus frames in the ring
   * `test_capture_handles_ring_full` — when ring is full, oldest frame is overwritten (or producer blocks; design decision: drop with counter)
   * `test_capture_backpressure_from_storage` — storage slow → audio still runs at real-time (verified by ring size)
   * `test_capture_dma_underrun_recovers` — if DMA underruns (test-injected), task resets
   * `test_capture_idle_low_power` — when not recording, I2S is stopped
   * `test_capture_no_alloc_in_task` — `-Wl,--wrap=malloc` test: 0 mallocs during 1 s of capture

3. **`storage_flush` task** (≈ 150 LOC, ≈ 6 tests):
   * `test_flush_writes_ring_to_sd` — fill ring with 50 frames, flush, file on SD has them
   * `test_flush_does_not_block_audio` — audio task runs at priority 23, storage at 15; under load, audio still hits deadline
   * `test_flush_handles_sd_full` — when free < threshold, log error and stop
   * `test_flush_handles_sd_missing` — SD unmount mid-write → task retries mount
   * `test_flush_powers_down_sd_idle` — after flush, SD SPI device unregisters
   * `test_flush_atomic_rename` — write to `.tmp`, rename to final

4. **`radio_task`** (≈ 400 LOC, ≈ 14 tests):
   * Mirrors the Go sender/receiver state machine in C++
   * `test_radio_picks_pending_from_queue` — on idle, next pending file is dequeued
   * `test_radio_sends_start_3x` — START packet sent 3 times with 50 ms gaps
   * `test_radio_sends_data_with_acks` — DATA + ACK loop, on timeout retransmit
   * `test_radio_max_5_retransmits` — 5 retransmits → mark failed
   * `test_radio_acks_received` — on ACK, advance bitmap
   * `test_radio_receives_tts` — incoming TTS_DATA queued for amp
   * `test_radio_receives_ui_update` — UI_UPDATE updates conversation DB
   * `test_radio_cad_busy_backoff` — CAD busy → backoff 100-500 ms, retry
   * `test_radio_cad_clear_tx` — CAD clear → transmit
   * `test_radio_handles_msg_id_gap` — message_id 5, 7 (skip 6) — both processed
   * `test_radio_replay_drop` — duplicate msg_id dropped
   * `test_radio_aes_encrypt` — setEncryption + transmit, RX side decrypts (loopback test on bench)
   * `test_radio_idle_low_power` — no pending, LoRa in sleep
   * `test_radio_no_alloc_in_isr` — ISR does 0 mallocs

5. **`ui_state` task** (≈ 300 LOC, ≈ 10 tests):
   * `test_ui_idle_screen_renders` — current conv + last 3 messages
   * `test_ui_recording_screen` — REC + timer
   * `test_ui_queued_screen` — TX progress
   * `test_ui_tts_screen` — playback progress
   * `test_ui_settings_screen` — B held 2s
   * `test_ui_partial_refresh_threshold` — after 50 partials, full refresh
   * `test_ui_advance_conv` — B press → next conv
   * `test_ui_prev_conv` — C press → prev conv
   * `test_ui_volume_change` — settings screen, B/C adjust volume
   * `test_ui_low_battery_warning` — VBat < 3.4 V → warning screen

6. **`watchdog` task** (≈ 80 LOC, ≈ 4 tests):
   * `test_watchdog_feeds_all_tasks` — every 500 ms, all task handles feed
   * `test_watchdog_triggers_on_hung_task` — if any task misses a feed, WDT resets after 5 s
   * `test_watchdog_excludes_isr` — ISR not required to feed
   * `test_watchdog_panic_resets` — simulated hang → reset, logs reason

7. **`power_mgmt` task** (≈ 100 LOC, ≈ 4 tests):
   * `test_power_deep_sleep_after_30s_idle` — no buttons, no RX, 30 s → deep sleep
   * `test_power_wake_on_ptt` — PTT press in deep sleep → wake
   * `test_power_wake_on_timer` — RTC timer wake for periodic beacon (v2)
   * `test_power_light_sleep_during_idle` — no buttons, RX off → light sleep (10 mA)

**Coverage target:** ≥ 80 % per task component, ≥ 80 % aggregate.

**Commit cadence:** one commit per task, TDD red→green.

### 4.9 Task 3.9 — main.cpp wiring

**File:** `firmware/m5/main/main.cpp` (~ 200 LOC)

* `app_main()`: init NVS, mount SD, init SPI bus, init I2S, init PSRAM ring, init buttons, init LoRa, init EPD, start all tasks, log "tether ready", feed watchdog forever.

**Tests:** integration test `firmware/m5/test/test_smoke.cpp` runs the full task set on host (mocked hardware), verifies no deadlocks and no task-starvation under synthetic load.

**Commit:** `m5: app_main wiring + smoke integration test`

### 4.10 Task 3.10 — Phase 3 exit gate

* `idf.py build` clean, binary < 1.5 MB
* `pio test` (host-side native) all pass with ≥ 80 % coverage
* Hardware smoke test: PTT 5 s message received and decoded on bridge
* Watchdog does not trigger during 1-hour idle test

**PR:** `phase/3-m5-skeleton` → `main`

---

## 5. Phase 4 — EPD + multi-conversation

**Goal:** The M5 has a working EPD with all 5+ screens, persistent conversation DB, and conversation switching. No Matrix or Forge integration yet — conversations are pre-populated via a CLI.

**Exit criteria:**
* All EPD screens render correctly (visual check on hardware, screenshot diff in CI)
* Conversation DB persists across reboots
* 16 conversations can be added, all show on idle screen, scroll works
* `test/test_epd_render.cpp` golden-image tests pass in CI
* Coverage ≥ 80 % on conv_db, epd, buttons, ptt, ui_state components

### 5.1 Task 4.1 — LittleFS VFS component

(Already started in Phase 3 task 3.3. Refactor in Phase 4 to add a typed file API.)

**File:** `firmware/m5/components/littlefs_vfs/`

```cpp
class LfsVfs {
public:
    esp_err_t Mount(const char* root = "/lfs");
    FILE* Open(const char* path, const char* mode);
    bool Exists(const char* path);
    int Remove(const char* path);
    int Rename(const char* from, const char* to);
    int Mkdir(const char* path);
    int Rmdir(const char* path);
    std::vector<std::string> ListDir(const char* path);
    size_t TotalBytes();
    size_t FreeBytes();
};
```

**Tests:**

* `test_lfs_mount_unmount_idempotent`
* `test_lfs_write_read_binary`
* `test_lfs_write_read_text`
* `test_lfs_overwrite`
* `test_lfs_listdir_returns_sorted`
* `test_lfs_persistence_across_mount` — write, unmount, remount, file still there
* `test_lfs_atomic_rename`
* `test_lfs_remove_nonexistent` — no error
* `test_lfs_free_bytes_decreases_on_write`

**Commit:** `m5: typed LittleFS VFS wrapper`

### 5.2 Task 4.2 — Conversation DB

**Files:** `firmware/m5/components/conv_db/conv_db.h`, `conv_db.cpp`, `test/test_conv_db.cpp`

```cpp
struct ConvInfo {
    uint8_t id[16];        // UUID
    char name[24];         // null-terminated, 24 chars max
    uint8_t kind;          // 0=Matrix, 1=Forge, 2=Broadcast
    char target[128];      // matrix room_id or forge session UUID
    uint8_t enc_key[16];   // HKDF-derived AES key
    int64_t last_activity_ms;
    uint16_t unread;
    bool exists;           // for the "removed" tombstone
};

struct HistoryEntry {
    uint32_t msg_id;
    int64_t timestamp_ms;
    uint8_t direction;     // 0=out, 1=in, 2=system
    char text[64];         // truncated preview
    uint8_t status;        // 0=pending, 1=acked, 2=failed
};

class ConvDb {
public:
    esp_err_t Init();
    // Up to 16 conversations
    esp_err_t Upsert(const ConvInfo& c);
    esp_err_t Remove(const uint8_t id[16]);
    esp_err_t Get(const uint8_t id[16], ConvInfo* out);
    std::vector<ConvInfo> List();
    esp_err_t AppendHistory(const uint8_t id[16], const HistoryEntry& e);
    std::vector<HistoryEntry> GetHistory(const uint8_t id[16], size_t max);
    esp_err_t ClearHistory(const uint8_t id[16]);
private:
    SemaphoreHandle_t mutex_;
};
```

**Layout on LittleFS:**

```
/conv/<uuid>/meta.bin       # ConvInfo struct
/conv/<uuid>/history.bin    # ring buffer of HistoryEntry, max 50
/conv/<uuid>/ratchet.bin    # AES counter for the conversation
```

**Tests (≈ 18):**

* `test_convdb_init_creates_dir`
* `test_convdb_upsert_get_round_trip`
* `test_convdb_upsert_overwrites`
* `test_convdb_remove`
* `test_convdb_get_missing`
* `test_convdb_list_empty`
* `test_convdb_list_one`
* `test_convdb_list_many` — 16 conversations
* `test_convdb_list_more_than_16_returns_first_16`
* `test_convdb_history_append_get`
* `test_convdb_history_ring_buffer_wraps_at_50`
* `test_convdb_persistence_across_init`
* `test_convdb_concurrent_upsert` — race-detector on host
* `test_convdb_name_truncated_to_24`
* `test_convdb_name_rejects_longer` — Upsert returns error if `name[24] = "x"` overflows
* `test_convdb_invalid_uuid` — Upsert rejects id with all zeros
* `test_convdb_atomic_write` — kill power mid-Upsert → old meta is intact
* `test_convdb_history_clear`

**Commit:** `m5: conversation DB with per-conv history ring buffer`

### 5.3 Task 4.3 — EPD screens

**Files:** `firmware/m5/components/epd/`

* `epd.h` — `EPD::Init()`, `EPD::Clear()`, `EPD::PartialRefresh(region, bitmap)`, `EPD::FullRefresh(bitmap)`
* `screens.h/.cpp` — one function per screen:
  * `void RenderIdle(const IdleState& s, uint8_t* out_buf)` — 200×200 monochrome bitmap
  * `void RenderRecording(...)`
  * `void RenderQueued(...)`
  * `void RenderTransmitting(...)`
  * `void RenderTtsPlayback(...)`
  * `void RenderSettings(...)`
  * `void RenderLowBattery(...)`

**Tests (`test/test_epd_render.cpp`):**

Golden-image tests. Pre-render PNG fixtures in `testdata/screens/`, render to bitmap in test, compare byte-for-byte.

* `test_render_idle_default`
* `test_render_idle_with_unread`
* `test_render_idle_no_conversations`
* `test_render_recording`
* `test_render_queued`
* `test_render_transmitting_with_progress`
* `test_render_tts`
* `test_render_settings`
* `test_render_low_battery`
* `test_render_long_conv_name_truncated`
* `test_render_long_message_truncated`

**Coverage target:** ≥ 90 % on screens.cpp.

**Commit:** `m5: EPD screens with golden-image regression tests`

### 5.4 Task 4.4 — UI state machine + conv switcher

**Files:** `firmware/m5/components/ui_state/ui_state.h`, `ui_state.cpp`, `test/test_ui_state.cpp`

Already partially built in Phase 3. Phase 4 adds:
* Conv-switcher state (current conv index, scroll position)
* `EPD::PartialRefresh` rate limiter (full refresh every 50 partials)
* Settings mode (B held 2 s)
* Watchdog for EPD controller (block partials if EPD driver not responding)

**Tests (≈ 12):**

* `test_ui_advance_conv_wraps`
* `test_ui_prev_conv_wraps`
* `test_ui_settings_entry_on_long_press`
* `test_ui_settings_exit`
* `test_ui_volume_change_via_b_c`
* `test_ui_partial_refresh_counter`
* `test_ui_full_refresh_after_50_partials`
* `test_ui_render_idle_called_on_idle_state`
* `test_ui_render_recording_called_on_press`
* `test_ui_history_scroll_in_settings`
* `test_ui_no_alloc_in_render` — `-Wl,--wrap=malloc` test
* `test_ui_concurrent_render_safe` — race-detector

**Commit:** `m5: UI state machine with conv switcher and refresh management`

### 5.5 Task 4.5 — Conv manager task

(Refactor of Phase 3 `conv_manager` placeholder.)

**File:** `firmware/m5/components/conv_manager/`

* `conv_manager.h/.cpp` — task that:
  * On `UI_UPDATE` packet (incoming from bridge): Upsert or Remove conv
  * On startup: send "sync request" packet to base, wait for response with all convs
  * Periodic "ping" (every 5 min while awake) to keep conv list fresh

**Tests:**

* `test_conv_manager_handles_ui_update_add`
* `test_conv_manager_handles_ui_update_remove`
* `test_conv_manager_sync_request_on_startup`
* `test_conv_manager_dedup_upsert` — same id twice doesn't duplicate
* `test_conv_manager_persists_state` — task restart preserves convs

**Commit:** `m5: conv manager task with UI_UPDATE handling`

### 5.6 Task 4.6 — Phase 4 exit gate

* `pio test` all green
* Coverage ≥ 80 % on conv_db, epd, ui_state, conv_manager
* EPD golden images match in CI
* Hardware: 16 convs can be added via CLI, scrollable, persistent across reboot
* `idf.py build` clean

**PR:** `phase/4-epd` → `main`

---

## 6. Phase 5 — STT (Parakeet) + TTS (Piper)

**Goal:** Voice in → text out, text in → voice out, working end-to-end on the base station. Real Parakeet-TDT 0.6B v2 int8 via sherpa-onnx, real Piper TTS via subprocess.

**Exit criteria:**
* `go test ./internal/stt/...` ≥ 80 % coverage with **WER ≤ 10 %** on a held-out test set (TIMIT subset or LibriSpeech test-clean)
* `go test ./internal/tts/...` ≥ 80 % coverage with **MOS ≥ 3.5** subjective, **intelligibility 100 %** on a held-out sentence list
* End-to-end: 5 s of synthetic speech → text within 5 s wall-clock on a CPU-only laptop
* End-to-end: text → Opus → LoRa → M5 → speaker within 1 s per sentence
* All mautrix-go and forge integration mocked (no real network)

### 6.1 Task 5.1 — STT interface + mock

**Files:** `go/internal/stt/transcribe.go`, `mock.go`, `mock_test.go`

```go
type Transcriber interface {
    Transcribe(ctx context.Context, pcm []float32, sampleRate int) (text string, err error)
    Close() error
}
```

**Tests:**

* `TestMockTranscriber_EchoesFilename` — mock returns the filename as text (deterministic)
* `TestMockTranscriber_Records` — every Transcribe call is recorded
* `TestMockTranscriber_SimulatesLatency` — config-able delay
* `TestMockTranscriber_SimulatesError` — config-able error

**Commit:** `stt: Transcriber interface + mock`

### 6.2 Task 5.2 — Parakeet via sherpa-onnx cgo

**Files:** `go/internal/stt/parakeet.go`, `parakeet_test.go`

```go
// #cgo LDFLAGS: -L/usr/local/lib -lsherpa-onnx-c-api -lonnxruntime
// #include <sherpa-onnx/c-api.h>
import "C"

type ParakeetConfig struct {
    ModelDir  string  // contains encoder.int8.onnx, decoder.int8.onnx, joiner.int8.onnx, tokens.txt
    NumThreads int
}

func NewParakeet(cfg ParakeetConfig) (*Parakeet, error) { ... }
func (p *Parakeet) Transcribe(ctx context.Context, pcm []float32, sampleRate int) (string, error) { ... }
```

**Test setup:**

* `testdata/parakeet/hello_8k.wav` — 1 s, "hello world" spoken
* `testdata/parakeet/digits_8k.wav` — 5 s, "one two three four five"

**Tests (real model required, gated by `//go:build parakeet`):**

* `TestParakeet_Hello` — Transcribe(hello_8k.wav) == "hello world" (case-insensitive, trimmed)
* `TestParakeet_Digits` — Transcribe(digits_8k.wav) contains "one", "two", "three", "four", "five"
* `TestParakeet_Latency` — Transcribe on a 5 s clip completes in < 5 s wall-clock
* `TestParakeet_Concurrent` — race-detector clean with 2 goroutines (model is single-threaded internally; goroutines serialize)
* `TestParakeet_Resample_8kTo16k` — input 8 kHz, internally resampled to 16 kHz
* `TestParakeet_Empty` — empty PCM returns "", nil
* `TestParakeet_Close` — Close then Transcribe returns error

**Test setup script:** `scripts/fetch-models.sh` downloads:
```bash
wget -q https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8.tar.bz2
tar xjf sherpa-onnx-nemo-parakeet-tdt-0.6b-v2-int8.tar.bz2 -C /var/lib/tether/
wget -q https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium.onnx
wget -q https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium.onnx.json
mv en_US-amy-medium.onnx{,.json} /var/lib/tether/piper-voices/
```

**Coverage target:** ≥ 80 % on parakeet.go (excluding cgo glue).

**Commit:** `stt: Parakeet-TDT 0.6B v2 int8 via sherpa-onnx cgo`

### 6.3 Task 5.3 — STT WER benchmark

**File:** `go/internal/stt/benchmark_test.go`

* Downloads LibriSpeech test-clean subset (or uses a checked-in 50-utterance sample)
* Runs Parakeet on every utterance, computes word error rate against ground truth
* `go test -bench=BenchmarkParakeet_WER -benchtime=1x`
* Asserts WER ≤ 10 % (or whatever the model achieves; document the number)

**Commit:** `stt: WER benchmark against LibriSpeech sample`

### 6.4 Task 5.4 — TTS interface + mock

**Files:** `go/internal/tts/synthesize.go`, `mock.go`, `mock_test.go`

```go
type Synthesizer interface {
    Synthesize(ctx context.Context, text string) (pcm []float32, sampleRate int, err error)
    Close() error
}
```

**Tests:**

* `TestMockSynthesizer_Echoes` — returns the input text as a hash → deterministic PCM
* `TestMockSynthesizer_SampleRate` — default 22050
* `TestMockSynthesizer_Stream` — exposes a streaming `SynthesizeStream(text, callback)` for sentence-by-sentence TTS
* `TestMockSynthesizer_Concurrent` — race-detector clean

**Commit:** `tts: Synthesizer interface + mock with streaming support`

### 6.5 Task 5.5 — Piper subprocess wrapper

**Files:** `go/internal/tts/piper.go`, `piper_test.go`

```go
type PiperConfig struct {
    BinaryPath string
    VoicePath  string  // .onnx
    UseGPU     bool
}

func NewPiper(cfg PiperConfig) (*Piper, error)
func (p *Piper) Synthesize(ctx context.Context, text string) ([]float32, int, error)
func (p *Piper) SynthesizeStream(ctx context.Context, sentences <-chan string, out chan<- []float32) error
```

**Tests (real model required, gated by `//go:build piper`):**

* `TestPiper_Hello` — Synthesize("Hello world") returns ~0.8 s of 22050 Hz PCM
* `TestPiper_Latency` — 10-word sentence completes in < 1 s
* `TestPiper_LongInput` — 1000-char input completes in < 30 s
* `TestPiper_StreamChunks` — SynthesizeStream emits ≥ 1 chunk
* `TestPiper_BadVoice` — voice path not found → error at NewPiper
* `TestPiper_BinaryMissing` — binary path not found → error
* `TestPiper_ContextCancel` — cancel mid-synth → process killed
* `TestPiper_Close` — Close terminates the underlying piper process

**Coverage target:** ≥ 80 %.

**Commit:** `tts: Piper subprocess wrapper with stream support`

### 6.6 Task 5.6 — TTS intelligibility benchmark

**File:** `go/internal/tts/benchmark_test.go`

* 50 sentences (held-out, not in training)
* Synthesize each, save as WAV
* Play through a speaker, manually transcribe, compare
* Assert 100 % word intelligibility on the held-out set (manual gate; document results in `docs/TTS-EVAL.md`)

**Commit:** `tts: intelligibility benchmark + eval doc`

### 6.7 Task 5.7 — PCM resampler (8 kHz ↔ 16 kHz ↔ 22 kHz)

**Files:** `go/internal/codec/resample.go`, `resample_test.go`

Parakeet wants 16 kHz, Piper emits 22 kHz, LoRa audio is 8 kHz. We need a high-quality resampler.

Use [`github.com/zaf/resample`](https://github.com/zaf/resample) (Cgo wrapper) or implement polyphase.

**Tests:**

* `TestResample_8kTo16k_Sine` — 1 kHz sine at 8 kHz → 1 kHz sine at 16 kHz (FFT check)
* `TestResample_22kTo8k_Sine` — 1 kHz sine at 22 kHz → 1 kHz sine at 8 kHz
* `TestResample_Passthrough_8kTo8k` — no change
* `TestResample_Silent` — all zeros, no NaN
* `TestResample_LongInput` — 60 s of audio resamples without artifacts
* `TestResample_RatioEdge` — 22050 → 8000 ratio = 2.75625, exact rational

**Commit:** `codec: high-quality polyphase resampler 8↔16↔22 kHz`

### 6.8 Task 5.8 — Audio sink (PulseAudio, VB-Cable, file)

**Files:** `go/internal/audio/sink.go`, `pulse.go`, `vbcable.go`, `file.go`, `sink_test.go`

```go
type Sink interface {
    Write(pcm []int16) error
    SampleRate() int
    Channels() int
    Close() error
}
```

Implementations:
* `file` — writes 16-bit LE mono PCM with WAV header. Used in tests and for "save to disk" mode.
* `pulse` — writes to a PulseAudio null sink via `pulse.Simple` (cgo or `github.com/jfreymann/pulse`).
* `vbcable` — Windows; uses `github.com/youpy/go-coremidi` (no), actually `github.com/go-ole/go-ole` to drive the MME API. Defer to v2 if v1 doesn't have a Windows target.

**Tests:**

* `TestFileSink_RoundTrip` — write 1 s, read back, equal
* `TestFileSink_WAVHeader` — first 44 bytes are valid RIFF/WAVE
* `TestFileSink_Appendable` — open in append mode, second write continues
* `TestPulseSink_Smoke` — only runs on Linux, with a real pulseaudio (CI: skip)

**Commit:** `audio: Sink interface + file impl + PulseAudio impl`

### 6.9 Task 5.9 — End-to-end voice pipeline tool

**File:** `go/tools/tether-voice-test/main.go`

Reads a WAV from stdin, runs it through the full pipeline (Opus encode → LoRa fragment → reassemble → Opus decode → STT), prints the text. Then takes text from stdin, runs it through (TTS → Opus encode → save to WAV), prints the path.

**Tests:**

* `TestVoicePipeline_HelloWorld_RoundTrip` — known WAV → STT → text matches; text → TTS → audio file written

**Commit:** `tools: tether-voice-test CLI for Phase 5 end-to-end test`

### 6.10 Task 5.10 — Phase 5 exit gate

* `go test -race -coverprofile=cov.out ./...` passes
* `scripts/cover.sh cov.out 80` ≥ 80 %
* `go test ./internal/stt/ -tags=parakeet` passes (real model)
* `go test ./internal/tts/ -tags=piper` passes (real model)
* WER ≤ 10 % on LibriSpeech sample
* Intelligibility 100 % on held-out TTS set (manual gate)
* End-to-end voice round-trip on a CPU-only laptop completes in < 5 s

**PR:** `phase/5-stt-tts` → `main`

---

## 7. Phase 6 — Matrix appservice

**Goal:** Tether is a Matrix puppet. Bidirectional text. New Matrix rooms create new Tether conversations; new Tether conversations appear in Matrix.

**Exit criteria:**
* `go test ./internal/matrix/...` ≥ 80 % coverage
* Appservice registers with a Synapse test instance, puppet user appears
* Voice from M5 → STT → Matrix room → reply from Element → TTS → M5 within 2 s
* `/tether add` in a room creates a conversation on the M5
* `/tether remove` removes it
* E2EE: not in v1 (deferred to v2)
* No real homeserver in CI — all tests use a mock Matrix client

### 7.1 Task 6.1 — Matrix client interface + mock

**Files:** `go/internal/matrix/client.go`, `mock_client.go`, `mock_client_test.go`

```go
type Client interface {
    SendText(ctx context.Context, roomID id.RoomID, text string) (*mautrix.RespSendEvent, error)
    JoinRoom(ctx context.Context, roomID id.RoomID) error
    LeaveRoom(ctx context.Context, roomID id.RoomID) error
    Subscribe(ctx context.Context) (<-chan Event, error)
    GetRoomState(ctx context.Context, roomID id.RoomID) (*mautrix.RoomStateMap, error)
}

type Event struct {
    Type   string
    Sender id.UserID
    Room   id.RoomID
    Body   string
    Time   time.Time
}
```

**Tests:**

* `TestMockClient_SendText`
* `TestMockClient_JoinLeave`
* `TestMockClient_SubscribeReceives` — buffered chan with synthetic events
* `TestMockClient_Reconnect` — mock simulates disconnect, client auto-reconnects

**Commit:** `matrix: Client interface + mock with synthetic event injection`

### 7.2 Task 6.2 — mautrix-go appservice

**Files:** `go/internal/matrix/appservice.go`, `appservice_test.go`

Uses `maunium.net/go/mautrix/appservice` to:
* Listen on `/transactions` and `/rooms/{roomAlias}` for incoming events
* Send messages via the `Intent` API
* Handle room invites (auto-join)

**Tests (mock client):**

* `TestAppservice_Registers`
* `TestAppservice_ReceivesRoomMessage` — fires `OnMessage` callback
* `TestAppservice_IgnoresOwnMessages` — events from own user_id are dropped
* `TestAppservice_HandlesRoomInvite`
* `TestAppservice_HandlesRoomLeave` — sets conversation to "removed" status
* `TestAppservice_ReconnectOnError` — mock client returns error, appservice retries

**Commit:** `matrix: mautrix-go appservice with on-message and on-invite handlers`

### 7.3 Task 6.3 — Room → conversation mapping

**Files:** `go/internal/matrix/room_to_conv.go`, `room_to_conv_test.go`

```go
type Mapper struct {
    db *conv.Store
}

func (m *Mapper) OnRoomMessage(roomID id.RoomID, body string) {
    convID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(roomID.String())).String()
    // (or simpler: just use the roomID directly hashed)
    m.db.Upsert(convID, ConvInfo{
        Name:   fetchRoomName(roomID),
        Kind:   KindMatrix,
        Target: roomID.String(),
    })
}
```

**Tests:**

* `TestMapper_RoomIDStableUUID` — same room ID → same conv ID (deterministic)
* `TestMapper_UpdatesRoomName` — `/tether rename Foo` updates
* `TestMapper_IgnoresEmptyMessages` — m.notice and m.image dropped

**Commit:** `matrix: room_id → conversation_id mapping`

### 7.4 Task 6.4 — UI_UPDATE push to M5

**Files:** `go/internal/conv/sync.go`, `sync_test.go`

When a conversation is created/updated/removed, send a `UI_UPDATE` packet (msg_type=7) over the LoRa link to the M5 via the radio.

**Tests:**

* `TestSync_NewConv_TriggersUIUpdate`
* `TestSync_RemovedConv_TriggersUIUpdateRemove`
* `TestSync_PiggybacksOnExistingConnection` — no extra handshake
* `TestSync_BatchUpates` — 5 convs → 5 UI_UPDATE packets, in order

**Commit:** `conv: UI_UPDATE sync to M5 on conversation changes`

### 7.5 Task 6.5 — E2E Matrix voice test (integration)

**File:** `go/tools/tether-matrix-test/main.go`

Spins up a test Synapse (via `matrix-docker-ansible-deploy` or a simple in-process mock), registers the appservice, simulates: M5 voice → STT → Matrix send → Element reply (via mock Event) → TTS → file output.

**Tests:**

* `TestMatrixE2E_VoiceToRoom` — full round-trip
* `TestMatrixE2E_RoomToVoice` — Element sends message → M5 would receive TTS
* `TestMatrixE2E_NewRoomAutoConv` — Element creates room with appservice user → conv appears in DB

**Commit:** `tools: tether-matrix-test CLI for Phase 6 end-to-end test`

### 7.6 Task 6.6 — Phase 6 exit gate

* `go test -race -coverprofile=cov.out ./...` passes
* `scripts/cover.sh cov.out 80` ≥ 80 %
* Manual: real Element client, real Synapse, real M5, full round-trip in < 2 s
* `golangci-lint run` clean

**PR:** `phase/6-matrix` → `main`

---

## 8. Phase 7 — Forge integration

**Goal:** Tether is a forge client. Voice → STT → forge `POST /messages`, SSE `text_delta` events → TTS → voice, tool output streamed through TTS.

**Exit criteria:**
* `go test ./internal/forge/...` ≥ 80 % coverage
* End-to-end: M5 voice → forge agent reply in < 5 s after agent_end
* Tool output (`cargo test`) streamed as TTS within 1 s of first line
* Idle session resumption works (M5 shows "thinking" during resume)
* All forge calls mocked in CI

### 8.1 Task 7.1 — Forge HTTP client + mock

**Files:** `go/internal/forge/client.go`, `http.go`, `mock_client.go`, `http_test.go`

```go
type Client interface {
    Login(ctx context.Context, apiKey string) (userID string, err error)
    CreateSession(ctx context.Context, profile string) (sessionID string, err error)
    ListSessions(ctx context.Context) ([]Session, error)
    DeleteSession(ctx context.Context, id string) error
    SendMessage(ctx context.Context, sessionID, text string) error
    SubscribeEvents(ctx context.Context, sessionID string, since int64) (<-chan Event, io.Closer, error)
}

type Event struct {
    Type    string  // "text_delta", "tool_call_start", "tool_call_end", "agent_end", "error"
    Content string
    Seq     int64
}
```

**Tests (mock client):**

* `TestMockClient_Login`
* `TestMockClient_CreateSession_ReturnsUUID`
* `TestMockClient_SendMessage_Accepted202`
* `TestMockClient_SubscribeEvents` — synthetic events
* `TestMockClient_Reconnect` — connection drops, auto-retry with `since=last_seq`

**Real client tests (gated by `//go:build forge`):**

* `TestHTTPClient_Login` — against a real forge instance
* `TestHTTPClient_CreateSession`
* `TestHTTPClient_SendMessage_202`
* `TestHTTPClient_SSE_TextDelta`

**Commit:** `forge: HTTP client + mock (real impl deferred to integration)`

### 8.2 Task 7.2 — Forge SSE consumer

**Files:** `go/internal/forge/sse.go`, `sse_test.go`

Uses `github.com/r3labs/sse/v2` to subscribe to `GET /sessions/{id}/events?since=N`.

**Tests:**

* `TestSSEConsumer_EmitsTextDelta`
* `TestSSEConsumer_EmitsToolCallStartEnd`
* `TestSSEConsumer_EmitsAgentEnd`
* `TestSSEConsumer_ReconnectPreservesSince`
* `TestSSEConsumer_Heartbeat` — comments and heartbeats don't crash
* `TestSSEConsumer_BadJSON` — malformed event → log + skip, don't crash

**Commit:** `forge: SSE event consumer with reconnect + backoff`

### 8.3 Task 7.3 — Forge session → conversation

**Files:** `go/internal/forge/session_to_conv.go`, `session_to_conv_test.go`

```go
func SessionToConvID(sessionUUID string) []byte {
    h := sha1.New()
    h.Write([]byte("forge:" + sessionUUID))
    var id [16]byte
    copy(id[:], h.Sum(nil)[:16])
    return id[:]
}
```

**Tests:**

* `TestSessionToConvID_Deterministic`
* `TestSessionToConvID_Different`
* `TestSessionToConvID_NotMatrixRoom` — forge and matrix namespaces don't collide

**Commit:** `forge: session UUID → conversation_id mapping`

### 8.4 Task 7.4 — Voice → forge pipeline

**File:** `go/internal/forge/pipeline.go` (≈ 300 LOC)

Glue: reassembled audio → STT → `POST /messages` → subscribe SSE → for each `text_delta`, buffer; on sentence boundary, TTS → Opus → fragment → send to M5.

```go
type Pipeline struct {
    stt     stt.Transcriber
    forge   forge.Client
    radio   radio.Radio
    tts     tts.Synthesizer
    codec   codec.Opus
    conv    *conv.Store
    logger  *slog.Logger
}

func (p *Pipeline) HandleIncomingAudio(ctx context.Context, msg *radio.IncomingMessage) error
func (p *Pipeline) HandleSSETextDelta(ctx context.Context, ev forge.Event) error
```

**Tests:**

* `TestPipeline_OpusDecodeSTTPost` — incoming audio → STT text → POST /messages
* `TestPipeline_TextDeltaToTTSFragment` — SSE text delta → sentence → TTS → Opus → fragment
* `TestPipeline_AgentEndTriggersTTSEnd`
* `TestPipeline_ToolCallStartTTSPrefix` — "running tool: bash" spoken
* `TestPipeline_ToolCallEndClears`
* `TestPipeline_BashStdoutStreamed` — 10 lines of bash output → 10 TTS chunks
* `TestPipeline_AgentError_Spoken`
* `TestPipeline_SessionExpired_Resume`
* `TestPipeline_ConcurrentIncoming` — 2 simultaneous voice messages

**Coverage target:** ≥ 85 %.

**Commit:** `forge: voice ↔ forge pipeline with streaming TTS`

### 8.5 Task 7.5 — Forge CLI

**File:** `go/cmd/tether-forge/main.go`

```bash
tether forge list
tether forge create [--profile coder]  # → conversation_id
tether forge rename <id> "name"
tether forge say <id> "stop the build"
tether forge delete <id>
```

**Tests:**

* `TestCLI_List` — uses mock forge client
* `TestCLI_Create_ReturnsConvID`
* `TestCLI_Rename_UpdatesConv`
* `TestCLI_Say_PostsMessage`
* `TestCLI_Delete_RemovesConv`
* `TestCLI_BadArgs` — exit 1 with usage

**Commit:** `cli: tether forge subcommand`

### 8.6 Task 7.6 — Phase 7 exit gate

* `go test -race -coverprofile=cov.out ./...` passes
* `scripts/cover.sh cov.out 80` ≥ 80 %
* Manual: M5 voice → forge agent → "yes I'll run cargo test" TTS, all in real-time
* `golangci-lint run` clean

**PR:** `phase/7-forge` → `main`

---

## 9. Phase 8 — Hardening

**Goal:** Production-readiness: security, reliability, power, OTA.

**Exit criteria:**
* AES-128-CTR per-conversation encryption end-to-end
* Watchdog across all M5 tasks (no resets during 1-hour test)
* OTA update path works (LoRa + USB)
* 6-hour battery life under normal use
* All NVS keys documented in `docs/NVS.md`
* Crash logs dump to LittleFS on panic
* Power: deep sleep draws < 50 µA

### 9.1 Task 8.1 — Per-conversation AES-128-CTR

**Files:** `go/internal/crypto/hkdf.go`, `firmware/m5/components/aes_link/`

* Go: `HKDF-Extract-Expand(masterPSK, salt=convID, info="tether-link-v1")` → 16-byte key
* M5: same, runs on boot when loading conv from LittleFS
* Both ends: `SX1262::setEncryption(key, nonce)`, nonce = msg_id (monotonic 32-bit, starts at 0 per session)
* Bridge firmware: `radio.setEncryption(key, nonce)` before each TX
* Go daemon: trusts the bridge (decryption happens in firmware)

**Tests:**

* `TestHKDF_RFC5869_Vector1`
* `TestHKDF_RFC5869_Vector2`
* `TestHKDF_RFC5869_Vector3`
* `TestHKDF_DifferentConvIDs_DifferentKeys`
* `TestHKDF_Deterministic` — same inputs → same key
* `TestEncryption_DecryptionRoundTrip` — on bench, with two SX1262 nodes
* `TestEncryption_TamperedPacket_Rejected` — bit flip in ciphertext → CRC fails

**Commit:** `crypto: per-conversation HKDF-SHA256 + SX1262 AES-128-CTR`

### 9.2 Task 8.2 — Watchdog on all M5 tasks

(Started in Phase 3; hardened here.)

* Each task registers with `esp_task_wdt_add()` on init
* `watchdog_feeder` task runs every 500 ms, calls `esp_task_wdt_reset()` for each registered task
* If any task is starved > 5 s, WDT resets
* Reset reason captured in NVS, exposed in startup log

**Tests:**

* `TestWDT_TaskHung_TriggersReset` — fake a hung task, verify reset after 5 s
* `TestWDT_ResetReasonPersistsAcrossReboot`
* `TestWDT_NormalOperation_NoReset` — 10 min run, no resets

**Commit:** `m5: watchdog across all tasks with reset-reason capture`

### 9.3 Task 8.3 — Crash log to LittleFS

**Files:** `firmware/m5/components/crash_log/`

* On panic, dump: task name, PC, LR, backtrace, registers → `/crash/<timestamp>.bin`
* On boot, check for new crash log → push to base via dedicated `kLog` frame
* Base station parses, includes in `slog` log

**Tests:**

* `TestCrashLog_TriggeredOnPanic` — fake panic in a test task, verify file written
* `TestCrashLog_UploadedOnBoot` — boot with existing crash log, verify upload
* `TestCrashLog_ParseFormat` — base station parses correctly

**Commit:** `m5: crash log to LittleFS, uploaded to base on boot`

### 9.4 Task 8.4 — OTA update path

**Files:** `firmware/m5/components/ota/`, `go/internal/ota/`

Two paths:
1. **USB** (v1): `idf.py -p /dev/ttyUSB0 flash` from the dev machine.
2. **LoRa-Bluetooth** (v2): the M5 advertises BLE, the base station pushes the new firmware over LoRa (slow but works) or directly over BLE (fast).

v1 implements USB only. v2 deferred.

**Tests (USB):**

* `TestOTA_USB_Flash_Success` — push a known-good image, M5 boots into it
* `TestOTA_USB_Flash_BadImage_Refused` — push garbage, M5 refuses and stays on old

**Commit:** `ota: USB flash path with image signature check`

### 9.5 Task 8.5 — Power optimization

**Files:** `firmware/m5/components/power_mgmt/`

* Deep sleep when idle > 30 s
* LoRa in `Sleep` mode (not `Standby`) when no RX window open
* I2S peripheral gated (powered down when not recording/playing)
* EPD partial refresh where possible
* PSRAM clock reduced to 80 MHz when not in active TX (Saves ~10 mA)

**Tests:**

* `TestPower_DeepSleepCurrentDraw` — measure < 50 µA on bench
* `TestPower_WakeOnPTT` — PTT press in deep sleep → wake in < 50 ms
* `TestPower_BatteryLifeEstimate` — model predicts ≥ 6 hours normal use

**Commit:** `m5: deep sleep + peripheral gating for 6h+ battery life`

### 9.6 Task 8.6 — NVS schema

**File:** `docs/NVS.md`

Documents all NVS keys:
* `node.id` (uint16) — this M5's node ID
* `node.master_psk` (16 bytes) — link-layer master key
* `radio.channel` (uint8) — default 0
* `radio.preset` (uint8) — encoded SF/BW/CR
* `ui.volume` (uint8) — 0..255
* `ui.last_conv_id` (16 bytes) — last active conv
* `ota.pending` (uint8) — 1 = boot into OTA mode

**Tests:**

* `TestNVS_RoundTrip_AllKeys`
* `TestNVS_FactoryReset` — erase all known keys
* `TestNVS_BadValue_DefaultsApplied` — read `volume=255`, clamp to 100

**Commit:** `docs: NVS schema + factory-reset routine`

### 9.7 Task 8.7 — Phase 8 exit gate

* `go test -race -coverprofile=cov.out ./...` passes
* `scripts/cover.sh cov.out 80` ≥ 80 %
* `pio test` all pass
* 6-hour battery life verified on bench
* 1-hour watchdog soak test passes
* OTA push works
* Crash log round-trips to base

**PR:** `phase/8-hardening` → `main`

---

## 10. Phase 9 — Polish

**Goal:** Operator UX, documentation, v2 hooks.

**Exit criteria:**
* Bubbletea TUI shows live conversation list, RF stats, manual TTS replay
* CLI documented in `docs/CLI.md`
* `README.md` updated with screenshots, quick-start, troubleshooting
* All "v2 hooks" (E2EE, frequency hopping, M5 playback, OTA-LoRa) marked with `// v2:` comments in code
* All `go test` / `pio test` / coverage / lint / format gates green
* Release tag `v0.1.0` cut

### 10.1 Task 9.1 — Bubbletea TUI

**Files:** `go/internal/ui/tui.go`, `tui_test.go`

Layout:
```
┌─ Tether ─────────────────────────────────────────┐
│ Conversations (4 active, 2 unread)                │
│  ► Forge: build-fix   14:32  ●2                   │
│    Alice (Matrix)      14:28                       │
│    Bob (Matrix)        13:55  ●1                   │
│    Forge: research     yesterday                   │
├──────────────────────────────────────────────────┤
│ RF: SF11 BW125 SNR -8 dBm  TX 14mA                │
│ Models: parakeet-tdt 0.6b v2 (640 MB), piper amy  │
│ Quiescent: 12 mA   Battery: 3.92V  (78%)         │
├──────────────────────────────────────────────────┤
│ [r] Replay last  [m] Mute mic  [q] Quit           │
└──────────────────────────────────────────────────┘
```

**Tests:**

* `TestTUI_RendersConversations`
* `TestTUI_RFStats_Update`
* `TestTUI_QuitCleanly`

**Commit:** `ui: bubbletea TUI for operator`

### 10.2 Task 9.2 — CLI documentation

**File:** `docs/CLI.md`

Full reference for `tether`, `tether forge`, `tether conv`, `tether say`, `tether daemon`, etc. With examples.

**Commit:** `docs: CLI reference`

### 10.3 Task 9.3 — README polish

**File:** `README.md` — update with:
* Animated GIF of TUI (use `vhs` or just a screenshot)
* Updated quick-start
* Troubleshooting section
* "What this is NOT" section (PTT radio ≠ IP radio, real-time ≠ async)

**Commit:** `docs: README polish with screenshots and troubleshooting`

### 10.4 Task 9.4 — v2 hooks (deferred features, marked in code)

For each deferred feature, add a clearly-marked stub:
* `// v2: mautrix-go E2EE (Megolm)` in `internal/matrix/appservice.go`
* `// v2: frequency hopping` in `firmware/m5/components/lora_sx1262/`
* `// v2: M5-side TTS playback` in `firmware/m5/components/i2s_amp/`
* `// v2: OTA-LoRa` in `firmware/m5/components/ota/`

These stubs are unit-tested to ensure the future hook point is reachable, but the implementation is empty.

**Tests:**

* `TestV2Hook_E2EE_StubExists`
* `TestV2Hook_FrequencyHopping_StubExists`
* ...

**Commit:** `*: v2 hook stubs for E2EE, hopping, M5 playback, OTA-LoRa`

### 10.5 Task 9.5 — v0.1.0 release

* `git tag -a v0.1.0 -m "Tether v0.1.0"`
* `git push origin v0.1.0`
* GitHub release with notes (auto-generated)
* Update `CHANGELOG.md`

**Commit:** `release: v0.1.0`

---

## 11. Cross-cutting coverage summary

Coverage targets per phase (must be met before phase closes):

| Phase | Go | ESP-IDF C++ | Bridge C++ | Aggregate |
|---|---|---|---|---|
| 0 | 100 % (trivial) | N/A | N/A | N/A |
| 1 | ≥ 80 % | N/A | N/A | ≥ 80 % |
| 2 | N/A | N/A | ≥ 80 % | ≥ 80 % |
| 3 | N/A | ≥ 80 % | N/A | ≥ 80 % |
| 4 | N/A | ≥ 80 % | N/A | ≥ 80 % |
| 5 | ≥ 80 % | N/A | N/A | ≥ 80 % |
| 6 | ≥ 80 % | N/A | N/A | ≥ 80 % |
| 7 | ≥ 80 % | N/A | N/A | ≥ 80 % |
| 8 | ≥ 80 % | ≥ 80 % | ≥ 80 % | ≥ 80 % |
| 9 | ≥ 80 % | ≥ 80 % | ≥ 80 % | ≥ 80 % |

CI blocks any PR that drops a package below 80 %. To raise coverage, **write more tests** — never delete the production code.

### 11.1 Fuzz testing

* `go test -fuzz=FuzzEnvelopeDecode -fuzztime=60s` runs on every PR for the protocol package.
* `go test -fuzz=FuzzAckBitmap -fuzztime=60s` for the ACK state.
* `go test -fuzz=FuzzFragment -fuzztime=60s` for the fragmentation.
* Fuzz corpus is committed to `go/pkg/protocol/testdata/fuzz_corpus/` and grown over time.

### 11.2 Property-based tests

* `testing/quick` is used for the protocol round-trip, fragmentation, ACK bitmap, and encryption derivation.
* 1000 random inputs per property test.

### 11.3 Hardware-in-the-loop tests (HIL)

For phases 2–8, a HIL test rig is required:
* 2 × RAK4631 (one as bridge-under-test, one as golden radio)
* 1 × M5 (or 2 × M5 for sender-receiver loopback)
* Bench PSU + multimeter for power tests
* Scripted scenarios in `scripts/hil/`

HIL tests run nightly via a self-hosted runner (not in this PR cycle). The HIL rig is a separate machine; the workflow `.github/workflows/hil.yml` triggers on `workflow_dispatch` and a label `run-hil`.

---

## 12. Test-first discipline — worked example

To make the TDD pattern concrete, here is the full red→green→refactor cycle for one task. This is the shape every task in this plan should follow.

### 12.1 Example: Task 1.1 — CRC + envelope

**Red (commit 1):**

```bash
$ git checkout -b task/1.1-crc
$ cat > go/pkg/protocol/crc.go <<'EOF'
package protocol

func crc16ccitt(b []byte) uint16 {
    // TODO
    return 0
}
EOF
$ cat > go/pkg/protocol/crc_test.go <<'EOF'
package protocol

import "testing"

func TestCrc16CCITT_KnownVectors(t *testing.T) {
    cases := []struct {
        in   []byte
        want uint16
    }{
        {[]byte{}, 0xFFFF},
        {[]byte{0x00}, 0xE1F0},
        {[]byte{0xFF, 0xFF, 0xFF, 0xFF}, 0x1B2A},
    }
    for _, c := range cases {
        if got := crc16ccitt(c.in); got != c.want {
            t.Errorf("crc16ccitt(%x) = %x, want %x", c.in, got, c.want)
        }
    }
}
EOF
$ cd go && go test ./pkg/protocol/
# FAIL: crc16ccitt(0x00) = 0, want 0xE1F0
```

**Green (commit 2):**

```bash
$ cat > go/pkg/protocol/crc.go <<'EOF'
package protocol

var crc16ccittTable = func() [256]uint16 {
    var t [256]uint16
    for i := 0; i < 256; i++ {
        crc := uint16(i) << 8
        for j := 0; j < 8; j++ {
            if crc&0x8000 != 0 {
                crc = (crc << 1) ^ 0x1021
            } else {
                crc <<= 1
            }
        }
        t[i] = crc
    }
    return t
}()

func crc16ccitt(b []byte) uint16 {
    crc := uint16(0xFFFF)
    for _, x := range b {
        crc = (crc << 8) ^ crc16ccittTable[byte(crc>>8)^x]
    }
    return crc
}
EOF
$ go test ./pkg/protocol/
# PASS
```

**Refactor (commit 3, optional):**

```bash
$ # Add 10 more test vectors, add benchmarks, add a TableLookup-vs-Bitwise-Bench comparison
$ go test -bench=. ./pkg/protocol/
$ git add -A && git commit -m "protocol(crc): expand tests + benchmark table-driven impl"
```

**Commit messages:**

```
protocol(crc): add failing test for CRC-16/CCITT-FALSE known vectors

Tests: TestCrc16CCITT_KnownVectors
Coverage: 0% → 0% (test only)

protocol(crc): implement CRC-16/CCITT-FALSE table-driven

Tests: TestCrc16CCITT_KnownVectors
Coverage: 0% → 100%
```

---

## 13. File path index (every file mentioned in this plan)

For quick navigation, every file the plan creates or modifies:

```
tether/
├── README.md
├── AGENTS.md
├── hardware.md
├── research.md
├── IMPLEMENTATION.md
├── LICENSE
├── .gitignore
├── .github/
│   ├── dependabot.yml
│   └── workflows/
│       ├── ci.yml
│       ├── firmware-build.yml
│       ├── coverage.yml
│       └── hil.yml
├── docs/
│   ├── preview.png
│   ├── ARCHITECTURE.md
│   ├── TESTING.md
│   ├── TTS-EVAL.md
│   ├── CLI.md
│   ├── NVS.md
│   └── images/
├── docker/
│   ├── dev.Dockerfile
│   └── docker-compose.yml
├── scripts/
│   ├── ci.sh
│   ├── cover.sh
│   ├── format-cpp.sh
│   ├── fetch-models.sh
│   ├── provision-base-station.sh
│   ├── flash-m5.sh
│   └── hil/
├── proto/
│   ├── tether.proto
│   ├── gen.go
│   ├── gen.sh
│   └── testdata/
│       ├── header_v1.bin
│       ├── data_packet_v1.bin
│       └── fuzz_seed/
├── go/
│   ├── go.mod
│   ├── go.sum
│   ├── .golangci.yml
│   ├── tetherd.toml
│   ├── cmd/
│   │   └── tetherd/
│   │       └── main.go
│   ├── pkg/
│   │   └── protocol/
│   │       ├── protocol.go            (generated)
│   │       ├── protocolpb/
│   │       │   └── protocol.pb.go     (generated)
│   │       ├── header.go
│   │       ├── header_test.go
│   │       ├── fragment.go
│   │       ├── fragment_test.go
│   │       ├── ack.go
│   │       ├── ack_test.go
│   │       ├── crc.go
│   │       ├── crc_test.go
│   │       ├── fuzzer_test.go
│   │       └── testdata/
│   │           ├── header_v1.bin
│   │           ├── data_packet_v1.bin
│   │           ├── hello_8k.wav
│   │           ├── digits_8k.wav
│   │           └── fuzz_corpus/
│   ├── internal/
│   │   ├── radio/
│   │   │   ├── radio.go
│   │   │   ├── mock.go
│   │   │   ├── mock_test.go
│   │   │   ├── sender.go
│   │   │   ├── sender_test.go
│   │   │   ├── receiver.go
│   │   │   └── receiver_test.go
│   │   ├── serial/
│   │   │   ├── loopback.go
│   │   │   ├── loopback_test.go
│   │   │   ├── bridge.go
│   │   │   └── bridge_test.go
│   │   ├── codec/
│   │   │   ├── opus.go
│   │   │   ├── opus_test.go
│   │   │   ├── wav.go
│   │   │   ├── wav_test.go
│   │   │   ├── resample.go
│   │   │   └── resample_test.go
│   │   ├── stt/
│   │   │   ├── transcribe.go
│   │   │   ├── mock.go
│   │   │   ├── parakeet.go
│   │   │   ├── parakeet_test.go
│   │   │   └── benchmark_test.go
│   │   ├── tts/
│   │   │   ├── synthesize.go
│   │   │   ├── mock.go
│   │   │   ├── piper.go
│   │   │   ├── piper_test.go
│   │   │   └── benchmark_test.go
│   │   ├── audio/
│   │   │   ├── sink.go
│   │   │   ├── file.go
│   │   │   ├── pulse.go
│   │   │   └── sink_test.go
│   │   ├── matrix/
│   │   │   ├── client.go
│   │   │   ├── mock_client.go
│   │   │   ├── appservice.go
│   │   │   ├── appservice_test.go
│   │   │   ├── room_to_conv.go
│   │   │   └── room_to_conv_test.go
│   │   ├── forge/
│   │   │   ├── client.go
│   │   │   ├── mock_client.go
│   │   │   ├── http.go
│   │   │   ├── http_test.go
│   │   │   ├── sse.go
│   │   │   ├── sse_test.go
│   │   │   ├── session_to_conv.go
│   │   │   ├── pipeline.go
│   │   │   └── pipeline_test.go
│   │   ├── conv/
│   │   │   ├── conv.go
│   │   │   ├── conv_test.go
│   │   │   ├── store.go
│   │   │   ├── store_test.go
│   │   │   ├── mem_store.go
│   │   │   ├── history.go
│   │   │   ├── history_test.go
│   │   │   ├── sync.go
│   │   │   └── sync_test.go
│   │   ├── crypto/
│   │   │   ├── hkdf.go
│   │   │   └── hkdf_test.go
│   │   ├── tetherd.go
│   │   └── ui/
│   │       ├── tui.go
│   │       └── tui_test.go
│   └── tools/
│       ├── tether-cli/
│       │   └── main.go
│       ├── tether-loopback/
│       │   └── main.go
│       ├── tether-voice-test/
│       │   └── main.go
│       ├── tether-matrix-test/
│       │   └── main.go
│       └── tether-fuzz/
│           └── main.go
├── firmware/
│   ├── shared/
│   │   └── protocol.h
│   ├── m5/
│   │   ├── CMakeLists.txt
│   │   ├── sdkconfig.defaults
│   │   ├── main/
│   │   │   ├── main.cpp
│   │   │   ├── idf_component.yml
│   │   │   └── Kconfig.projbuild
│   │   ├── test/
│   │   │   └── test_smoke.cpp
│   │   └── components/
│   │       ├── protocol/
│   │       │   ├── include/protocol.h
│   │       │   ├── src/protocol.cpp
│   │       │   ├── test/test_protocol.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── spi_bus/
│   │       │   ├── include/spi_bus.h
│   │       │   ├── src/spi_bus.cpp
│   │       │   ├── test/test_spi_bus.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── lora_sx1262/
│   │       │   ├── include/lora_sx1262.h
│   │       │   ├── src/lora_sx1262.cpp
│   │       │   ├── test/test_lora_sx1262.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── sd_card/
│   │       │   ├── include/sd_card.h
│   │       │   ├── src/sd_card.cpp
│   │       │   ├── test/test_sd_card.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── i2s_mic/
│   │       │   ├── include/i2s_mic.h
│   │       │   ├── src/i2s_mic.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── i2s_amp/
│   │       │   ├── include/i2s_amp.h
│   │       │   ├── src/i2s_amp.cpp
│   │       │   ├── test/test_beep_gen.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── opus_enc/
│   │       │   ├── include/opus_enc.h
│   │       │   ├── src/opus_enc.cpp
│   │       │   ├── test/test_opus_enc.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── psram_ring/
│   │       │   ├── include/psram_ring.h
│   │       │   ├── src/psram_ring.cpp
│   │       │   ├── test/test_psram_ring.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── littlefs_vfs/
│   │       │   ├── include/littlefs_vfs.h
│   │       │   ├── src/littlefs_vfs.cpp
│   │       │   ├── test/test_littlefs_vfs.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── epd/
│   │       │   ├── include/epd.h
│   │       │   ├── src/epd.cpp
│   │       │   ├── src/screens.cpp
│   │       │   ├── test/test_epd_render.cpp
│   │       │   ├── testdata/screens/
│   │       │   │   ├── idle_default.png
│   │       │   │   ├── idle_unread.png
│   │       │   │   ├── recording.png
│   │       │   │   ├── queued.png
│   │       │   │   ├── transmitting.png
│   │       │   │   ├── tts.png
│   │       │   │   ├── settings.png
│   │       │   │   └── low_battery.png
│   │       │   └── CMakeLists.txt
│   │       ├── buttons/
│   │       │   ├── include/buttons.h
│   │       │   ├── src/buttons.cpp
│   │       │   ├── test/test_buttons.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── ptt/
│   │       │   ├── include/ptt.h
│   │       │   ├── src/ptt.cpp
│   │       │   ├── test/test_ptt_state.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── conv_db/
│   │       │   ├── include/conv_db.h
│   │       │   ├── src/conv_db.cpp
│   │       │   ├── test/test_conv_db.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── conv_manager/
│   │       │   ├── include/conv_manager.h
│   │       │   ├── src/conv_manager.cpp
│   │       │   ├── test/test_conv_manager.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── ui_state/
│   │       │   ├── include/ui_state.h
│   │       │   ├── src/ui_state.cpp
│   │       │   ├── test/test_ui_state.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── audio_capture/
│   │       │   ├── include/audio_capture.h
│   │       │   ├── src/audio_capture.cpp
│   │       │   ├── test/test_audio_capture.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── storage_flush/
│   │       │   ├── include/storage_flush.h
│   │       │   ├── src/storage_flush.cpp
│   │       │   ├── test/test_storage_flush.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── radio_task/
│   │       │   ├── include/radio_task.h
│   │       │   ├── src/radio_task.cpp
│   │       │   ├── test/test_radio_task.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── watchdog/
│   │       │   ├── include/watchdog.h
│   │       │   ├── src/watchdog.cpp
│   │       │   ├── test/test_watchdog.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── power_mgmt/
│   │       │   ├── include/power_mgmt.h
│   │       │   ├── src/power_mgmt.cpp
│   │       │   ├── test/test_power_mgmt.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── aes_link/
│   │       │   ├── include/aes_link.h
│   │       │   ├── src/aes_link.cpp
│   │       │   ├── test/test_aes_link.cpp
│   │       │   └── CMakeLists.txt
│   │       ├── crash_log/
│   │       │   ├── include/crash_log.h
│   │       │   ├── src/crash_log.cpp
│   │       │   ├── test/test_crash_log.cpp
│   │       │   └── CMakeLists.txt
│   │       └── ota/
│   │           ├── include/ota.h
│   │           ├── src/ota.cpp
│   │           ├── test/test_ota.cpp
│   │           └── CMakeLists.txt
│   └── bridge/
│       ├── platformio.ini
│       ├── src/
│       │   ├── main.cpp
│       │   ├── frame.h
│       │   ├── frame.cpp
│       │   ├── frame_test.cpp
│       │   ├── lora.h
│       │   ├── lora.cpp
│       │   ├── lora_test.cpp
│       │   ├── serial_link.h
│       │   ├── serial_link.cpp
│       │   └── serial_link_test.cpp
│       └── test/
│           └── test_bench.cpp
```

---

## 14. Acceptance criteria summary (the bar for "v0.1.0 done")

When all nine phases are merged to `main` with green CI, the system must demonstrate:

1. **Cold start to first transmission in < 5 s** on the M5 (boot → idle screen → PTT functional).
2. **60 s voice message round-trip from M5 mic to base station Opus-decoded PCM in < 8 minutes wall-clock** (dominated by airtime; ~6 min for 500 packets at SF11/BW125).
3. **STT WER ≤ 10 %** on LibriSpeech test-clean sample.
4. **TTS intelligibility 100 %** on a held-out sentence list (manual gate, documented).
5. **Multi-conversation:** 4 simultaneous conversations (2 Matrix, 2 Forge) with no cross-talk, no mis-routed messages.
6. **Range ≥ 2 km LOS** with stock antennas.
7. **Battery life ≥ 6 hours** of normal use (10 messages/hour, 30 s each).
8. **No watchdog resets** during 1-hour soak test.
9. **No memory leaks** during 24-hour soak test (PSRAM heap, Go RSS).
10. **Coverage ≥ 80 %** on every package, enforced in CI.
11. **All fuzz tests clean** for 5 minutes minimum.
12. **No `// TODO`, `// FIXME`, `// v2:` unmarked** comments in shipped code (v2 hooks are explicitly tagged).
13. **No commented-out code** in shipped code.
14. **All `// go:generate` directives resolve** in a fresh `make all` from a clean clone.
15. **Release tag `v0.1.0` cut** with changelog.

Anything that fails this list is a bug. Fix it before tagging.

---

## 15. Anti-patterns to avoid

* **Writing production code before the test.** This plan exists to prevent that. If you find yourself writing `.cpp` without a corresponding `test_*.cpp` first, stop and write the test.
* **Mocking the production code instead of the dependencies.** The mock is for the external dependency (radio, opus, parakeet, piper, etc.), not for your own code.
* **Coverage by deletion.** If coverage is below 80 %, write tests, do not delete code.
* **Skipping CI on "small changes".** All PRs run full CI. No exceptions.
* **Committing the proto-generated code with hand edits.** If you need to change the proto, regenerate.
* **Mixing refactors with feature work.** One PR = one logical change.
* **Tests that only test the mock.** A test of `mockTranscriber.Echoes()` is fine; a test of `parakeet.go` that asserts `parakeet.transcribe() == mockTranscriber.transcribe()` is not — the test must exercise the real path or be marked as testing the mock.
* **Sleep-based tests.** Use synchronization primitives or `time.After` with a real timeout, not `time.Sleep`.
* **Long test files (> 500 lines).** Split by feature/function.
* **Commented-out code.** Delete it. Git remembers.

---

*End of plan. Begin with Phase 0, Task 0.1.*
