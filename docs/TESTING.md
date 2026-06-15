# Tether — Testing

This document is the canonical guide to writing and running tests
across the Tether codebase. It supplements `plan.md` §0 with concrete
recipes.

## 1. TDD discipline

For every non-trivial unit of work — and in particular every logic
change — the cycle is:

1. **Write a failing test** in the same commit that introduces the
   unit. The test must fail for the *right reason* (compile error,
   "function not defined", `assert.Equal` mismatch), not for an
   unrelated reason.
2. **See it fail** by running the test suite. Capture the failure in
   the commit message body.
3. **Write the minimum implementation** that makes the test pass.
4. **See it pass.** No previous tests may regress.
5. **Refactor** while green. Cleanup, rename, dedupe, extract.
6. **Commit.** One commit per red→green→refactor cycle. Commit
   message format:
   ```
   <area>: <one-line summary>

   Tests: <list of test functions added/modified>
   Coverage: <new % / total %>
   ```

For purely mechanical work (rename, comment fix) no test is needed.
For any logic change, the test comes first. See `plan.md` §0.1 for
the authoritative copy of this rule.

## 2. Running tests

The Tether Go daemon lives under `go/`. From the repo root:

```bash
cd go

# fast feedback loop
go test ./...

# with the race detector (slower; required for any concurrent code)
go test -race ./...

# with coverage (writes cover.out)
go test -coverprofile=cover.out -covermode=atomic ./...
go tool cover -func=cover.out     # per-function table
go tool cover -html=cover.out     # line-by-line HTML

# enforcement (Phase 0+)
bash ../scripts/cover.sh cover.out 80
```

The cover script defaults to an 80 % statement-coverage gate. The
script is portable and works with any profile path and threshold:

```bash
bash scripts/cover.sh <profile> <min-percent>
```

The Go module ships with its own `go.mod`. If a parent `go.work`
exists in the build environment, set `GOWORK=off` so the parent
workspace does not consume us (the dev shell and `scripts/ci.sh`
do this automatically).

## 3. Writing a fuzzer

The protocol parser is the only code that is adversarial-facing (LoRa
packets can be corrupted in transit; attackers may try to crash the
parser). It must be covered by `FuzzXxx` harnesses. The minimum
shape:

```go
func FuzzEnvelopeDecode(f *testing.F) {
    // 1. Seed with a few known-good inputs so the fuzzer starts with
    //    non-zero coverage of the interesting paths.
    for i := 0; i < 10; i++ {
        env := makeEnvelope()                // some known-good value
        env.MessageId = uint32(i + 1)
        f.Add(env)
    }

    // 2. The body must never panic. Document the acceptable error
    //    space in a comment so reviewers can audit it.
    f.Fuzz(func(t *testing.T, in []byte) {
        _, _ = protocol.Decode(in)
        // Acceptable: nil, ErrTruncated, ErrBadCRC, ErrPayloadTooLarge.
    })
}
```

Run a fuzz locally:

```bash
cd go
go test -fuzz=FuzzEnvelopeDecode -fuzztime=30s ./pkg/protocol/
```

In CI the fuzzer runs for 60 s on every PR. A crash blocks the PR.

> ⚠️ **Seeds live in `f.Add()`**, not in `testdata/fuzz/FuzzXxx/`.
> Files under `testdata/fuzz/FuzzXxx/` are interpreted by Go's
> testing framework as corpus entries in its *internal* fuzzer
> format (with a "must include version" header), not as raw bytes.
> Use `f.Add()` for raw seed input; let the fuzzer manage the
> persisted corpus under `testdata/fuzz/`.

## 4. Coverage gates

* **Go:** 80 % statement coverage across `./...`, enforced by
  `scripts/cover.sh`. Generated code under
  `go/pkg/protocol/protocolpb/` is excluded from the gate (it has
  no logic to cover).
* **C++ (ESP-IDF):** 80 % line coverage per component, aggregate
  ≥ 80 %. Not exercised in Phase 0.
* **C++ (PlatformIO/bridge):** 80 % line coverage. Not exercised in
  Phase 0.

A PR that drops below the gate is blocked by CI. To raise coverage,
add more tests; do not delete production code.

## 5. Mocking strategy

All external dependencies are interfaces, with a real and a mock
implementation. The mock comes first.

| External dep   | Go interface   | Mock                                  | Real                                    |
|----------------|----------------|---------------------------------------|-----------------------------------------|
| LoRa radio     | `radio.Radio`  | `internal/radio/mock.go`              | `internal/radio/sx1262.go` (Phase 3)    |
| Serial port    | `serial.Port`  | `internal/serial/mock_port.go`        | `internal/serial/usb.go`                |
| Opus codec     | `codec.Opus`   | `internal/codec/mock_opus.go`         | `internal/codec/opus.go`                |
| STT engine     | `stt.Transcriber` | `internal/stt/mock.go`              | `internal/stt/parakeet.go`              |
| TTS engine     | `tts.Synthesizer` | `internal/tts/mock.go`              | `internal/tts/piper.go`                 |
| Audio sink     | `audio.Sink`   | `internal/audio/file.go` (WAV)        | `internal/audio/pulse.go`               |
| Matrix client  | `matrix.Client`| `internal/matrix/mock_client.go`      | `internal/matrix/appservice.go`         |
| Forge client   | `forge.Client` | `internal/forge/mock_client.go`       | `internal/forge/http.go`                |
| Conversation store | `conv.Store`| `internal/conv/mem_store.go`          | `internal/conv/lfs_store.go`            |

When introducing a new external dependency, the first commit is the
interface + the mock. Production code that uses the interface is the
second commit. The real implementation is the third.

## 6. CI

`.github/workflows/ci.yml` runs the following required status
checks on every push and PR:

| Job                     | What it does                                          |
|-------------------------|-------------------------------------------------------|
| `go-test`               | `go test -race -coverprofile=cover.out -covermode=atomic ./...` and `scripts/cover.sh cover.out 80` |
| `go-lint`               | `golangci-lint run --config go/.golangci.yml`         |
| `cpp-format`            | `clang-format --dry-run --Werror` on `firmware/`      |
| `proto-verify`          | regenerate `proto/tether.proto` and assert `git diff --exit-code` |
| `firmware-build-m5`     | `idf.py build` inside the `espressif/idf:v5.2` image  |
| `firmware-test-bridge`  | `pio test` in `firmware/bridge/`                      |

To read a failure: open the failing job, copy the failing step's
log, and run the same command locally from the repo root with
`GOWORK=off`. CI is not a black box — anything that fails in CI
must be reproducible locally.

## 7. Common gotchas

These have bitten us in adjacent projects. See `AGENTS.md` §9 for
the full list.

* **Generated proto code must not be modified by hand.** If you
  change `proto/tether.proto`, run `bash proto/gen.sh` and commit
  the regenerated bindings.
* **proto.Clone, not struct copy.** Generated proto types embed a
  `sync.Mutex`. Copying them with `*env` trips `go vet` and
  produces subtle data races. Use `proto.Clone(env).(*YourType)`.
* **Race detector on radio/concurrent code.** Any change to
  `internal/radio/` must be exercised with `go test -race`.
* **GOWORK.** Set `GOWORK=off` whenever you run `go test` from
  inside `go/`. The build environment has a parent go.work that
  doesn't know about tether.
* **Clang-format on every C++ change.** CI runs
  `clang-format --dry-run --Werror`; local: `bash scripts/format-cpp.sh`.
* **Piper subprocess pipe can stall** if you don't drain it
  (Phase 5+).
* **Parakeet-TDT is non-streaming** (Phase 5+). Full clip must
  arrive before STT begins. Buffer forge text deltas at sentence
  boundary.

## 8. References

* `plan.md` §0 — cross-cutting rules
* `plan.md` §1.4 — protocol parser fuzz policy
* `AGENTS.md` — project conventions, environment rules
* `research.md` — design and rationale
