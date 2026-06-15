// Package nvs holds the Tether NVS schema (plan §9.6).
//
// NVS is the ESP32-S3's non-volatile key-value store. The M5
// firmware reads provisioning data on boot (master PSK, radio
// channel, UI volume, etc.) and writes Phase 8 telemetry
// (reset reason, boot count, bounded history). The base
// station doesn't read or write NVS directly — the M5 owns
// the storage — but the schema is the contract the firmware
// implements and the operator TUI surfaces.
//
// This Go package exposes the schema as code so a future TUI
// feature is type-checked and the docs (docs/NVS.md) are
// generated from the same source as the firmware's Kconfig
// bindings. A drift between firmware and docs is a
// compilation error here, not a runtime mystery.

package nvs

// NVSVersion is bumped on schema changes. The firmware reads
// this on boot; if the stored version is less than the
// firmware's expected version, a migration is run. The
// migration logic is the firmware's responsibility; this
// Go package just pins the current number.
const NVSVersion = 1

// KeyType enumerates the supported NVS data types. The
// underlying blob uses the ESP-IDF nvs_type_t enum; we keep
// only the subset Tether uses.
type KeyType int

const (
	TypeUint8 KeyType = iota + 1
	TypeUint16
	TypeUint32
	TypeInt32
	TypeBytes
	TypeString
)

// KeyName is a typed string alias. We export the well-known
// names as constants below.
type KeyName string

// Well-known NVS keys. The names are the NVS namespace +
// key string. The full path is `<namespace>:<key>`, but the
// Tether M5 firmware uses the default namespace for
// everything, so we just expose the key part.
const (
	// KeyNodeID is the M5's 16-bit node id (0xFFFF =
	// unprovisioned, broadcast).
	KeyNodeID KeyName = "node.id"
	// KeyMasterPSK is the 16-byte master PSK from which the
	// per-conversation AES-128 key is derived (plan §9.1).
	KeyMasterPSK KeyName = "node.master_psk"
	// KeyRadioChannel is the US915 channel index (0..63).
	KeyRadioChannel KeyName = "radio.channel"
	// KeyRadioPreset is the encoded SF/BW/CR preset. We use
	// one byte: high nibble = SF, low nibble = CR. Bandwidth
	// is fixed at 125 kHz for v1; a future migration can
	// widen the encoding.
	KeyRadioPreset KeyName = "radio.preset"
	// KeyUIVolume is the speaker volume (0..255, clamped to
	// 100 by the firmware).
	KeyUIVolume KeyName = "ui.volume"
	// KeyLastConvID is the 16-byte UUID of the last-active
	// conversation. The conv manager reads this on boot to
	// restore the user's selection.
	KeyLastConvID KeyName = "ui.last_conv_id"
	// KeyOTAPending is 1 if the next boot should enter OTA
	// mode (the operator pushed a new image but it hasn't
	// been verified yet).
	KeyOTAPending KeyName = "ota.pending"

	// Phase 8 telemetry:
	// KeyResetReason is the last ResetReason (plan §9.2). The
	// firmware writes this before the esp_restart; the next
	// boot reads it and surfaces it in the operator TUI.
	KeyResetReason KeyName = "diag.reset_reason"
	// KeyBootCount is the number of times the M5 has booted
	// since the NVS was last factory-reset. Monotonically
	// increasing.
	KeyBootCount KeyName = "diag.boot_count"
	// KeyResetHistory is the bounded list of recent resets
	// (16 entries × 8 bytes each, plan §9.2).
	KeyResetHistory KeyName = "diag.reset_history"
)

// Key describes one entry in the schema. The fields are
// stable: the docs/NVS.md doc is generated from this struct
// plus the per-key defaults below.
type Key struct {
	Name        KeyName
	Type        KeyType
	Default     interface{} // uint8/16/32, []byte, or string
	Description string
	// BytesLen is set for TypeBytes; it pins the on-disk
	// length so a misuse is a compile error.
	BytesLen int
}

// AllKeys returns the canonical (stable) list of schema
// entries. Adding a new key requires bumping NVSVersion
// (and the firmware migration).
func AllKeys() []Key {
	return []Key{
		{Name: KeyNodeID, Type: TypeUint16, Default: uint16(0xFFFF),
			Description: "M5's 16-bit node id. 0xFFFF = unprovisioned (broadcast)."},
		{Name: KeyMasterPSK, Type: TypeBytes, Default: make([]byte, 16),
			BytesLen:    16,
			Description: "16-byte master PSK. The per-conversation AES-128 key is HKDF-SHA256(master, salt=convID, info='tether-link-v1')."},
		{Name: KeyRadioChannel, Type: TypeUint8, Default: uint8(0),
			Description: "US915 uplink channel index (0..63). Default 0 = 902.3 MHz."},
		{Name: KeyRadioPreset, Type: TypeUint8, Default: uint8(0xB8),
			Description: "Encoded SF/BW/CR preset. High nibble = SF (0xB = SF11), low nibble = CR (0x8 = 4/8). Bandwidth fixed at 125 kHz."},
		{Name: KeyUIVolume, Type: TypeUint8, Default: uint8(100),
			Description: "Speaker volume 0..255; the firmware clamps values > 100 to 100."},
		{Name: KeyLastConvID, Type: TypeBytes, Default: make([]byte, 16),
			BytesLen:    16,
			Description: "16-byte UUID of the last-active conversation. Empty (16 zero bytes) on first boot."},
		{Name: KeyOTAPending, Type: TypeUint8, Default: uint8(0),
			Description: "1 = boot into OTA mode (a new image is pending verification)."},
		{Name: KeyResetReason, Type: TypeUint8, Default: uint8(0),
			Description: "Last ResetReason value (plan §9.2). 0=unknown, 1=power-on, 2=soft-restart, 3=task-wdt, 4=panic, 5=brownout."},
		{Name: KeyBootCount, Type: TypeUint32, Default: uint32(0),
			Description: "Number of boots since last factory-reset. Monotonically increasing."},
		{Name: KeyResetHistory, Type: TypeBytes,
			Default:     make([]byte, 16*8),
			BytesLen:    16 * 8,
			Description: "Bounded reset history: 16 entries × 8 bytes (reason:1 + boot_count:4 + timestamp_unix_ms:3 = 8 bytes each)."},
	}
}

// Default returns the default value of a uint-typed key as
// a uint64. The caller is responsible for casting to the
// correct width.
func Default(name KeyName) (uint64, bool) {
	for _, k := range AllKeys() {
		if k.Name == name {
			switch v := k.Default.(type) {
			case uint8:
				return uint64(v), true
			case uint16:
				return uint64(v), true
			case uint32:
				return uint64(v), true
			case int32:
				return uint64(v), true
			}
			return 0, false
		}
	}
	return 0, false
}

// DefaultBytes returns the default value of a bytes-typed
// key. The returned slice is a fresh copy; the caller may
// modify it without affecting the schema.
func DefaultBytes(name KeyName) ([]byte, bool) {
	for _, k := range AllKeys() {
		if k.Name == name {
			if b, ok := k.Default.([]byte); ok {
				out := make([]byte, len(b))
				copy(out, b)
				return out, true
			}
			return nil, false
		}
	}
	return nil, false
}

// ClampVolume applies the firmware's volume-clamp policy:
// values > 100 are clamped to 100. The firmware's NVS
// reader is the canonical place to apply this; we expose
// the helper so the operator TUI can show the clamped
// value before pushing the OTA update.
func ClampVolume(v uint8) uint8 {
	if v > 100 {
		return 100
	}
	return v
}
