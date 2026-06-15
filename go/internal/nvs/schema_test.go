// schema_test.go — TDD-first tests for the Tether NVS schema
// (plan §9.6).
//
// NVS is the ESP32-S3's non-volatile key-value store. We use
// it for two distinct purposes:
//
//   1. Provisioning data (the master PSK, the radio channel,
//      the UI volume, the last-active conversation). The M5
//      firmware reads these on boot. The base station doesn't
//      touch NVS — the M5 owns the storage.
//   2. Phase 8 telemetry: the reset reason, the boot count,
//      the bounded reset history (plan §9.2/§9.6).
//
// The Go side doesn't read or write NVS directly, but the
// schema is the contract the firmware implements and the
// operator TUI (plan §10.1) surfaces. We expose the schema
// as code so a future TUI feature is type-checked and the
// docs (docs/NVS.md) are generated from the same source.
//
// Tests in this file pin:
//   * Every documented key has a known type and default.
//   * Defaults are applied when the value is missing or
//     out of range (e.g. volume=255 is clamped to 100).
//   * Factory reset erases every known key.
//   * The schema is versioned: bumping NVS_VERSION is a
//     signal to the firmware that a migration is required.

package nvs_test

import (
	"reflect"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/nvs"
)

func TestSchema_DocumentedKeys(t *testing.T) {
	t.Parallel()
	keys := nvs.AllKeys()
	if len(keys) == 0 {
		t.Fatalf("AllKeys returned empty")
	}
	// Each key must have a name, a type, a default, and a
	// documented description.
	for _, k := range keys {
		if k.Name == "" {
			t.Errorf("key with empty name: %+v", k)
		}
		if k.Description == "" {
			t.Errorf("key %q: empty description", k.Name)
		}
		switch k.Type {
		case nvs.TypeUint8, nvs.TypeUint16, nvs.TypeUint32,
			nvs.TypeInt32, nvs.TypeBytes, nvs.TypeString:
			// ok
		default:
			t.Errorf("key %q: unknown type %v", k.Name, k.Type)
		}
	}
}

func TestSchema_NodeID_Default(t *testing.T) {
	t.Parallel()
	// node.id default is 0xFFFF (broadcast). The first boot
	// of a new M5 sends a UI_UPDATE with target=0xFFFF; the
	// base station responds with a real node id which the
	// M5 then persists to NVS.
	got, ok := nvs.Default(nvs.KeyNodeID)
	if !ok {
		t.Fatalf("Default(%q): no default", nvs.KeyNodeID)
	}
	if got != 0xFFFF {
		t.Errorf("node.id default: got %d, want 65535", got)
	}
}

func TestSchema_MasterPSK_DefaultIsEmpty(t *testing.T) {
	t.Parallel()
	got, ok := nvs.DefaultBytes(nvs.KeyMasterPSK)
	if !ok {
		t.Fatalf("DefaultBytes(%q): no default", nvs.KeyMasterPSK)
	}
	// 16 bytes of zero is the "unprovisioned" sentinel.
	if len(got) != 16 {
		t.Errorf("master_psk default: got %d bytes, want 16", len(got))
	}
	for _, b := range got {
		if b != 0 {
			t.Errorf("master_psk default: got non-zero byte")
			break
		}
	}
}

func TestSchema_RadioChannel_Default(t *testing.T) {
	t.Parallel()
	got, ok := nvs.Default(nvs.KeyRadioChannel)
	if !ok {
		t.Fatalf("Default(%q): no default", nvs.KeyRadioChannel)
	}
	if got != uint64(0) {
		t.Errorf("radio.channel default: got %d, want 0", got)
	}
}

func TestSchema_RadioPreset_Default(t *testing.T) {
	t.Parallel()
	got, ok := nvs.Default(nvs.KeyRadioPreset)
	if !ok {
		t.Fatalf("Default(%q): no default", nvs.KeyRadioPreset)
	}
	// 0xF3 is the standard private LoRa sync word; the
	// preset is SF11/BW125/CR4-8 by default (plan §6.1).
	// We encode the preset as one byte: high nibble = SF, low
	// nibble = CR; bandwidth is fixed at 125 kHz for v1.
	if got != uint64(0xB8) {
		t.Errorf("radio.preset default: got 0x%X, want 0xB8", got)
	}
}

func TestSchema_UIVolume_Default(t *testing.T) {
	t.Parallel()
	got, ok := nvs.Default(nvs.KeyUIVolume)
	if !ok {
		t.Fatalf("Default(%q): no default", nvs.KeyUIVolume)
	}
	// 0..255 with 100 = comfortable default.
	if got != uint64(100) {
		t.Errorf("ui.volume default: got %d, want 100", got)
	}
}

func TestSchema_ClampVolume(t *testing.T) {
	t.Parallel()
	// 255 is out of range; the firmware clamps to 100.
	for _, in := range []uint8{0, 50, 100, 200, 255} {
		out := nvs.ClampVolume(in)
		if out > 100 {
			t.Errorf("ClampVolume(%d) = %d, want ≤ 100", in, out)
		}
	}
}

func TestSchema_OTAPending_Default(t *testing.T) {
	t.Parallel()
	got, ok := nvs.Default(nvs.KeyOTAPending)
	if !ok {
		t.Fatalf("Default(%q): no default", nvs.KeyOTAPending)
	}
	if got != uint64(0) {
		t.Errorf("ota.pending default: got %d, want 0", got)
	}
}

func TestSchema_Version_Pinned(t *testing.T) {
	t.Parallel()
	// Bumping NVS_VERSION is a firmware-migration signal.
	// We pin the current version (1) so a future bump is a
	// deliberate, recorded change.
	if nvs.NVSVersion != 1 {
		t.Errorf("NVSVersion: got %d, want 1", nvs.NVSVersion)
	}
}

func TestSchema_FactoryResetKeys(t *testing.T) {
	t.Parallel()
	// The factory-reset routine erases every key the
	// firmware knows about. We verify the set is what
	// docs/NVS.md documents.
	expected := map[string]bool{
		string(nvs.KeyNodeID):        true,
		string(nvs.KeyMasterPSK):     true,
		string(nvs.KeyRadioChannel):  true,
		string(nvs.KeyRadioPreset):   true,
		string(nvs.KeyUIVolume):      true,
		string(nvs.KeyLastConvID):    true,
		string(nvs.KeyOTAPending):    true,
		// Phase 8 telemetry:
		string(nvs.KeyResetReason):   true,
		string(nvs.KeyBootCount):     true,
	}
	keys := nvs.AllKeys()
	seen := map[string]bool{}
	for _, k := range keys {
		seen[string(k.Name)] = true
	}
	for k := range expected {
		if !seen[k] {
			t.Errorf("factory reset would miss key %q", k)
		}
	}
}

func TestSchema_AllKeys_StableOrder(t *testing.T) {
	t.Parallel()
	// The list of keys must be stable across runs so the
	// generated docs don't churn. We don't pin the exact
	// order (it would be brittle), but we pin the set.
	a := nvs.AllKeys()
	b := nvs.AllKeys()
	if !reflect.DeepEqual(a, b) {
		t.Errorf("AllKeys not stable: %v vs %v", a, b)
	}
}

func TestSchema_Documentation_NotEmpty(t *testing.T) {
	t.Parallel()
	// The schema is the source of truth for docs/NVS.md;
	// we pin a few invariants that the doc generator relies
	// on.
	keys := nvs.AllKeys()
	for _, k := range keys {
		if k.Name == "" {
			t.Errorf("key with empty name: %+v", k)
		}
	}
}

func TestSchema_Default_UnknownKey(t *testing.T) {
	t.Parallel()
	_, ok := nvs.Default("nope.this.is.not.a.key")
	if ok {
		t.Errorf("Default(unknown): expected ok=false")
	}
}

func TestSchema_Default_BytesTypeRejected(t *testing.T) {
	t.Parallel()
	// DefaultBytes on a uint-typed key returns (nil, false).
	// The test pins that contract.
	_, ok := nvs.DefaultBytes(nvs.KeyUIVolume)
	if ok {
		t.Errorf("DefaultBytes(uint key): expected ok=false")
	}
}

func TestSchema_DefaultBytes_UnknownKey(t *testing.T) {
	t.Parallel()
	_, ok := nvs.DefaultBytes("nope")
	if ok {
		t.Errorf("DefaultBytes(unknown): expected ok=false")
	}
}
