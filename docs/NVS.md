# Tether NVS Schema

This document describes the keys the Tether M5 firmware reads
and writes in the ESP32-S3's NVS (non-volatile storage)
partition. The M5 owns the storage; the Go daemon never
touches NVS directly. The schema is the source of truth for
both the firmware and the operator TUI (plan §10.1).

**Version:** 1 (see [Versioning](#versioning))

## Provisioning keys

| Key                | Type    | Bytes | Default | Description |
|--------------------|---------|-------|---------|-------------|
| `node.id`          | uint16  | 2     | 0xFFFF  | M5's 16-bit node id. 0xFFFF = unprovisioned (broadcast). |
| `node.master_psk`  | bytes   | 16    | zeros   | 16-byte master PSK. The per-conversation AES-128 key is `HKDF-SHA256(master, salt=convID, info="tether-link-v1")` (see [crypto](../go/internal/crypto/hkdf.go) and plan §9.1). |
| `radio.channel`    | uint8   | 1     | 0       | US915 uplink channel index (0..63). Default 0 = 902.3 MHz. |
| `radio.preset`     | uint8   | 1     | 0xB8    | Encoded SF/BW/CR preset. High nibble = SF (0xB = SF11), low nibble = CR (0x8 = 4/8). Bandwidth is fixed at 125 kHz in v1. |
| `ui.volume`        | uint8   | 1     | 100     | Speaker volume 0..255; the firmware clamps values > 100 to 100 (see [`ClampVolume`](../go/internal/nvs/schema.go)). |
| `ui.last_conv_id`  | bytes   | 16    | zeros   | 16-byte UUID of the last-active conversation. Empty (16 zero bytes) on first boot. |
| `ota.pending`      | uint8   | 1     | 0       | 1 = boot into OTA mode (a new image is pending verification). |

## Telemetry keys (Phase 8)

| Key                  | Type   | Bytes | Default | Description |
|----------------------|--------|-------|---------|-------------|
| `diag.reset_reason`  | uint8  | 1     | 0       | Last `ResetReason` value (see `firmware/m5/components/watchdog/include/watchdog.h`). 0=unknown, 1=power-on, 2=soft-restart, 3=task-wdt, 4=panic, 5=brownout. |
| `diag.boot_count`    | uint32 | 4     | 0       | Number of boots since last factory-reset. Monotonically increasing. |
| `diag.reset_history` | bytes  | 128   | zeros   | Bounded reset history: 16 entries × 8 bytes (reason:1 + boot_count:4 + timestamp_unix_ms:3 = 8 bytes each). |

## Provisioning workflow

1. **Out-of-band master PSK** (research.md §14.1). Each M5 is
   shipped with a printed QR card containing its master PSK
   and node id. The operator scans the card with a phone, runs
   `tether provision` (a future CLI; see plan §10), and the
   daemon pushes the values to the M5 over LoRa (or USB for
   v1). The M5 persists them to NVS.
2. **First boot**: the M5 reads `node.id`. If it is 0xFFFF
   (unprovisioned), the M5 sends a UI_UPDATE packet with
   `target=0xFFFF` to the base station. The base station
   allocates a real id and replies; the M5 persists the new
   id to NVS.
3. **Channel / preset**: defaults are SF11/BW125/CR4-8 on US915
   channel 0. The operator can change these via the CLI
   (`tether radio set --channel 4`).

## Default-value policy

* `node.id` defaults to 0xFFFF (broadcast). The first boot
  triggers provisioning.
* `node.master_psk` defaults to 16 zero bytes. The firmware
  refuses to transmit if the master PSK is all zeros (the
  AES-128 key derived from a zero PSK is itself a fixed
  value, so an unprovisioned M5 cannot impersonate another).
* `radio.preset` defaults to 0xB8 (SF11/BW125/CR4-8). The
  link budget math is in research.md §6.1.
* `ui.volume` defaults to 100 (≈ 40% of max). Values > 100
  are clamped; the firmware logs a warning and uses 100.

## Factory reset

A factory reset erases every key in the schema. The reset is
triggered by holding the PTT button for 10 seconds during
boot; the M5 logs `factory-reset: erasing NVS` and
re-initialises the partition. The base station is informed
via a `UI_UPDATE` packet with `remove=true` for every
conversation; it deletes its side of the conversation DB.

## Versioning

`NVSVersion` is currently `1`. A schema change (new key,
type change, length change) bumps the version and requires a
firmware-side migration: read the old value, transform, write
to the new key, erase the old. The migration runs once on
boot if the on-disk version is less than the firmware's
expected version.

The Go-side schema is the contract: changing a key in
`go/internal/nvs/schema.go` is a wire-level change that
affects every M5 on every deployment. The CI test
`TestSchema_FactoryResetKeys` and the version pin
`TestSchema_Version_Pinned` catch accidental drift.

## See also

* `firmware/m5/components/watchdog/` — writes `diag.reset_reason`
  and `diag.boot_count` on every reset.
* `firmware/m5/components/crash_log/` — the on-disk crash
  record format (separate from NVS; lives on LittleFS at
  `/crash/<name>.bin`).
* `go/internal/crashlog/parser.go` — the Go-side parser for
  the M5 crash record (the M5's LittleFS is not NVS).
* `go/internal/crypto/hkdf.go` — the per-conversation AES-128
  key derivation that consumes `node.master_psk`.
