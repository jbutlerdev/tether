# Tether CLI Reference

This document is the canonical reference for every Tether command-line
binary. Each binary lives under `go/tools/` or `go/cmd/`; this document
describes what they do, the flags they accept, and the exit codes they
emit. See `plan.md` §10.2 (Task 9.2).

All binaries are produced by `go build ./...` from `go/`. The
binaries that ship in production builds are:

* `tether-forge` — manage Forge agent sessions (Phase 8, plan §8.5)
* `tether-loopback` — in-process loopback harness (Phase 2, plan §2.7)
* `tether-voice-test` — voice pipeline harness (Phase 5, plan §6.9)
* `tether-matrix-test` — Matrix appservice harness (Phase 6, plan §7.5)

The `tetherd` daemon (the production base-station binary) is built
from `go/cmd/tetherd/` and has its own flags described in the
[Daemon section](#tetherd) below.

> **Conventions used in this document:**
>
> * `[flags]` are zero or more command-line flags.
> * `<arg>` is a required argument.
> * `[arg]` is an optional argument.
> * Exit codes are summarised in [Exit codes](#exit-codes).

---

## Exit codes

Tether CLIs use a small, consistent exit-code vocabulary so scripts
can dispatch on the result without parsing stdout. The constants are
defined per-binary; the meanings are shared.

| Code | Name | Meaning |
|------|------|---------|
| `0`  | `exitOK` | Success. The requested operation completed; the output (if any) is on stdout. |
| `1`  | `exitUsage` | Bad arguments, missing flags, or a usage violation. The error is on stderr. |
| `2`  | `exitBackendError` | The transport (Matrix, Forge, serial, …) returned an error that is not recoverable from the CLI's side. The user should retry or inspect the daemon. |
| `3`  | `exitInternalError` | An unexpected error (e.g. a nil pointer that should never be nil). The CLI's bug; please file an issue. |

A CLI that does not match any of these specific cases uses the
default Go convention: `0` on success, non-zero on failure, with the
exact value chosen for compatibility with the existing test suite.

---

## `tetherd`

`tetherd` is the long-running base-station daemon. It owns the
serial port to the RAK4631 bridge, the Matrix appservice, the Forge
HTTP client, the STT and TTS engines, and the audio sink. Most
operators never invoke it by hand — it is started by the system
service unit (`tetherd.service`).

```text
tetherd [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `/etc/tether/tetherd.toml` | Path to the TOML config file. |
| `-log-level` | `info` | One of `debug`, `info`, `warn`, `error`. `debug` floods the log with per-packet detail. |
| `-serial-port` | (from config) | Override the serial port (e.g. `/dev/ttyACM0`). |
| `-no-audio` | `false` | Disable the PulseAudio sink. Useful for headless CI. |
| `-no-matrix` | `false` | Skip the Matrix appservice registration. Conv routing still works for Forge. |
| `-no-forge` | `false` | Skip Forge login. Conv routing still works for Matrix. |
| `-no-tui` | `false` | Do not start the Bubbletea TUI. Useful in containers. |

### Config

The TOML config is documented in `tetherd.toml.example`; the relevant
sections are:

```toml
[serial]
port = "/dev/ttyACM0"
baud = 921600

[matrix]
homeserver = "https://matrix.example.com"
appservice_id = "tether"
as_token = "..."
hs_token = "..."

[forge]
base_url = "https://forge.example.com"
api_key = "..."

[audio]
sink = "pulse"            # "pulse" | "file" | "null"
pulse_sink_name = "tether"

[stt]
model = "/var/lib/tether/parakeet-tdt-0.6b-v2-int8"

[tts]
binary = "/usr/local/bin/piper"
voice = "/var/lib/tether/piper-voices/en_US-amy-low.onnx"
```

### Signals

* `SIGINT` / `SIGTERM` — graceful shutdown. Drains in-flight messages,
  flushes the conv store, and exits with code 0.
* `SIGHUP` — reload the config file. Currently a no-op (the config
  is read at startup); reserved for the v1.1 release.

### Examples

```bash
# Production invocation.
sudo systemctl start tetherd

# Foreground, with debug logging.
tetherd -log-level debug

# CI / smoke test, no audio or TUI.
tetherd -no-audio -no-tui -serial-port /dev/null
```

---

## `tether forge`

`tether forge` is the operator front-end for managing Forge agent
sessions. It is a thin wrapper over the `forge.Client` interface; in
production the client is the real HTTP client (build tag `forge`),
and in tests it is the in-process `MockClient`.

```text
tether forge <subcommand> [flags] [args]
```

### Subcommands

* `tether forge list` — print every session the user owns.
* `tether forge create [--profile NAME]` — open a new session.
  Default profile is `coder`.
* `tether forge rename <conv_id> "name"` — change a conversation's
  display name. The name lives in the daemon's in-memory store
  today; on the next daemon reconnect the name is re-synced from
  Matrix / Forge. (See plan §8.5 for the v1.1 admin API.)
* `tether forge say <session_id> "text"` — post a user message to a
  session. The agent's reply is delivered via the SSE stream and is
  not echoed on this CLI's stdout.
* `tether forge delete <session_id>` — terminate a session.
  Idempotent: deleting an unknown id exits 0 and prints a warning.
* `tether forge help` — print the usage block.

### Flags

Only `tether forge create` accepts flags.

| Flag | Default | Description |
|------|---------|-------------|
| `--profile` | `coder` | Agent profile name. |

### Examples

```bash
# List all sessions.
tether forge list

# Open a new "researcher" session.
tether forge create --profile researcher
# prints: 7c3a2c40-9b1f-4d4f-9b1f-1e0b1f5e6b6c

# Send a message.
tether forge say 7c3a2c40-9b1f-4d4f-9b1f-1e0b1f5e6b6c "summarise the diff"

# Rename the conversation.
tether forge rename 0a1b2c3d4e5f60718293a4b5c6d7e8f9 "build-fix"

# Delete the session when done.
tether forge delete 7c3a2c40-9b1f-4d4f-9b1f-1e0b1f5e6b6c
```

---

## `tether-loopback`

`tether-loopback` runs the radio-side loopback harness end-to-end in
a single process. It is the fastest feedback loop for the data
plane: it synthesises a sine wave, encodes it with Opus, fragments
it over the radio, reassembles it, and prints a one-line summary.

```text
tether-loopback [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-duration` | `1s` | Duration of the synthetic audio. At SF11/BW125, a 60 s clip produces ~500 packets. |
| `-freq` | `440` | Sine wave frequency in Hz. The TTS intelligibility test uses 220 Hz (a typical male fundamental). |

### Output

A single line on stdout, followed by a newline:

```
tether-loopback: sent=<int> acked=<int> received=<int> retries=<int> duration=<duration>
```

Exit code is `0` on success (acked == sent), `1` if not all packets
were acknowledged, or any packet failed.

### Examples

```bash
# Default 1-second loopback.
tether-loopback

# 60-second loopback, the same duration as the soak test.
tether-loopback -duration 60s
```

---

## `tether-voice-test`

`tether-voice-test` exercises the full voice pipeline (audio in →
STT → TTS → audio out) on a single WAV file. The STT and TTS
engines are mocks by default; build with `-tags parakeet` and
`-tags piper` to wire in the real engines.

```text
tether-voice-test -in input.wav -out output.wav
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-in` | (required) | Path to a 16-bit mono PCM WAV file. Any sample rate is resampled internally to 16 kHz. |
| `-out` | (required) | Path to write the 16-bit mono PCM WAV at 8 kHz. |
| `-sink` | (none) | Optional `audio.Sink` override (used by the test harness; not for operators). |

### Output

A single line on stdout:

```
tether-voice-test: stt_chars=<int> tts_samples=<int>
```

The recognised text is also printed to stdout, between the banner
lines. The TTS output is written to `-out` as a WAV file.

### Examples

```bash
# Run with a recorded voice prompt.
tether-voice-test -in /var/lib/tether/test/digits_8k.wav \
                  -out /tmp/tether-voice-test-out.wav
```

---

## `tether-matrix-test`

`tether-matrix-test` is the Phase 6 end-to-end harness. It uses the
in-process `matrix.MockClient` and a `conv.MemStore` to simulate the
full "M5 voice → Matrix" and "Element reply → TTS → M5" flows
without a real homeserver.

```text
tether-matrix-test [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-out` | `/tmp/tether-matrix-test` | Output directory for TTS WAV files. |
| `-v` | `false` | Verbose logging. Sets the slog level to debug. |

### Examples

```bash
# Run with default output directory.
tether-matrix-test

# Verbose run to a custom directory.
tether-matrix-test -v -out /tmp/my-test
```

---

## Environment variables

| Variable | Affects | Default | Description |
|----------|---------|---------|-------------|
| `TETHER_CONFIG` | `tetherd` | `/etc/tether/tetherd.toml` | Config file path. Overrides the `-config` flag if both are set. |
| `TETHER_LOG_LEVEL` | `tetherd` | (from `-log-level`) | Log level. Useful for systemd `Environment=` lines. |
| `GOWORK` | any `go` invocation | (unset) | Set to `off` to disable the parent go.work file. CI sets this explicitly. |
| `GOPROXY` | `go mod` | (Go default) | Module proxy. CI pins this to ensure reproducible builds. |

---

## See also

* `docs/ARCHITECTURE.md` — how the binaries fit together.
* `docs/TESTING.md` — running the test suite.
* `plan.md` §10.2 — the task that produced this document.
