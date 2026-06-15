// nvs.h — Tether M5 NVS schema bindings (plan §9.6).
//
// The C++ side mirrors the Go schema in go/internal/nvs. The
// two are kept in sync by hand (the doc generator is a future
// plan §10 item), so this header is small: it exposes the
// key enum, the default-value helpers, the NvsHandle
// abstraction, and the volume-clamping policy.
//
// On real hardware the implementation routes through
// nvs_open / nvs_get_*. The host build uses an in-memory
// FakeNvs so the unit tests run without ESP-IDF.

#pragma once

#include <cstddef>
#include <cstdint>
#include <cstring>

namespace tether::m5 {

// kNvsVersion is the schema version. Bump this on schema
// changes (new key, type change, length change); the
// firmware's migration logic reads the stored version and
// transforms the old layout.
inline constexpr int kNvsVersion = 1;

// NvsKey enumerates the well-known keys. The values are the
// NVS internal key ids; the strings ("node.id" etc.) are the
// on-disk key names (mirroring the Go schema in
// go/internal/nvs/schema.go).
enum class NvsKey : uint8_t {
  kNodeId = 1,
  kMasterPsk = 2,
  kRadioChannel = 3,
  kRadioPreset = 4,
  kUIVolume = 5,
  kLastConvId = 6,
  kOtaPending = 7,
  kResetReason = 8,
  kBootCount = 9,
  kResetHistory = 10,
};

// ClampVolume applies the firmware's volume-clamp policy:
// values > 100 are clamped to 100. The firmware's volume
// consumer calls this at the point of use; the raw NVS value
// is preserved for debugging.
inline uint8_t ClampVolume(uint8_t v) { return v > 100 ? 100 : v; }

// ── NvsHandle — facade around the platform NVS API ──────────────────
//
// The host build backs this with an in-memory map; the real
// hardware build backs it with nvs_open / nvs_get_*. The
// interface is the same.
class NvsHandle {
public:
  // GetUint8 / GetUint16 / GetUint32 / GetBytes / Set* read
  // and write through the underlying NVS. They all return
  // true on success.
  static bool GetUint8(NvsKey k, uint8_t *out);
  static bool GetUint16(NvsKey k, uint16_t *out);
  static bool GetUint32(NvsKey k, uint32_t *out);
  static bool GetBytes(NvsKey k, uint8_t *out, size_t len);
  static bool SetUint8(NvsKey k, uint8_t v);
  static bool SetUint16(NvsKey k, uint16_t v);
  static bool SetUint32(NvsKey k, uint32_t v);
  static bool SetBytes(NvsKey k, const uint8_t *in, size_t len);

  // Test-only API: reset every key to its default. The
  // production firmware exposes a similar routine through
  // the conv manager (factory reset, plan §9.6).
  static void ResetForTest();
};

// nvs_factory_reset erases every key in the schema. The
// production implementation calls nvs_erase_all; the host
// build calls NvsHandle::ResetForTest.
void nvs_factory_reset();

} // namespace tether::m5
