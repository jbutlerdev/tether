// nvs.cpp — implementation of tether::m5::NvsHandle.
//
// The host build backs the schema with an in-memory std::map
// so the unit tests run on the dev machine without ESP-IDF.
// The production build routes through the real nvs_open /
// nvs_get_* / nvs_set_* APIs.

#include "nvs.h"

#include <cstring>
#include <map>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "nvs_flash.h"
#endif

namespace tether::m5 {

namespace {

// The on-host backing store. We key on the NvsKey enum and
// store a vector of bytes; the typed accessors decode.
struct NvsEntry {
  std::vector<uint8_t> bytes;
};

std::map<NvsKey, NvsEntry> &Store() {
  static std::map<NvsKey, NvsEntry> s;
  return s;
}

// Default values — must match go/internal/nvs/schema.go
// (plan §9.6: "the schema is the contract").
void ApplyDefaults() {
  auto &s = Store();
  // node.id = 0xFFFF (uint16 LE)
  s[NvsKey::kNodeId] = NvsEntry{{0xFF, 0xFF}};
  // master_psk = 16 zero bytes
  s[NvsKey::kMasterPsk] = NvsEntry{std::vector<uint8_t>(16, 0)};
  // radio.channel = 0
  s[NvsKey::kRadioChannel] = NvsEntry{{0x00}};
  // radio.preset = 0xB8
  s[NvsKey::kRadioPreset] = NvsEntry{{0xB8}};
  // ui.volume = 100
  s[NvsKey::kUIVolume] = NvsEntry{{0x64}};
  // last_conv_id = 16 zero bytes
  s[NvsKey::kLastConvId] = NvsEntry{std::vector<uint8_t>(16, 0)};
  // ota.pending = 0
  s[NvsKey::kOtaPending] = NvsEntry{{0x00}};
  // diag.reset_reason = 0
  s[NvsKey::kResetReason] = NvsEntry{{0x00}};
  // diag.boot_count = 0
  s[NvsKey::kBootCount] = NvsEntry{{0x00, 0x00, 0x00, 0x00}};
  // diag.reset_history = 128 zero bytes
  s[NvsKey::kResetHistory] = NvsEntry{std::vector<uint8_t>(16 * 8, 0)};
}

#ifdef TETHER_M5_HOST_TEST
// First-touch: apply defaults once. The host build doesn't
// persist; defaults are applied at static-init time so the
// test fixture doesn't have to.
struct InitDefaults {
  InitDefaults() { ApplyDefaults(); }
};
InitDefaults g_init;
#endif

} // namespace

// ── Read ───────────────────────────────────────────────────────────

bool NvsHandle::GetUint8(NvsKey k, uint8_t *out) {
  if (out == nullptr)
    return false;
  auto &s = Store();
  auto it = s.find(k);
  if (it == s.end() || it->second.bytes.empty())
    return false;
  *out = it->second.bytes[0];
  return true;
}

bool NvsHandle::GetUint16(NvsKey k, uint16_t *out) {
  if (out == nullptr)
    return false;
  auto &s = Store();
  auto it = s.find(k);
  if (it == s.end() || it->second.bytes.size() < 2)
    return false;
  // Little-endian on the wire and in memory.
  *out = static_cast<uint16_t>(it->second.bytes[0]) |
         (static_cast<uint16_t>(it->second.bytes[1]) << 8);
  return true;
}

bool NvsHandle::GetUint32(NvsKey k, uint32_t *out) {
  if (out == nullptr)
    return false;
  auto &s = Store();
  auto it = s.find(k);
  if (it == s.end() || it->second.bytes.size() < 4)
    return false;
  *out = static_cast<uint32_t>(it->second.bytes[0]) |
         (static_cast<uint32_t>(it->second.bytes[1]) << 8) |
         (static_cast<uint32_t>(it->second.bytes[2]) << 16) |
         (static_cast<uint32_t>(it->second.bytes[3]) << 24);
  return true;
}

bool NvsHandle::GetBytes(NvsKey k, uint8_t *out, size_t len) {
  if (out == nullptr || len == 0)
    return false;
  auto &s = Store();
  auto it = s.find(k);
  if (it == s.end() || it->second.bytes.size() < len)
    return false;
  std::memcpy(out, it->second.bytes.data(), len);
  return true;
}

// ── Write ──────────────────────────────────────────────────────────

bool NvsHandle::SetUint8(NvsKey k, uint8_t v) {
  Store()[k] = NvsEntry{{v}};
  return true;
}

bool NvsHandle::SetUint16(NvsKey k, uint16_t v) {
  Store()[k] = NvsEntry{
      {static_cast<uint8_t>(v & 0xFF), static_cast<uint8_t>((v >> 8) & 0xFF)}};
  return true;
}

bool NvsHandle::SetUint32(NvsKey k, uint32_t v) {
  Store()[k] = NvsEntry{{
      static_cast<uint8_t>(v & 0xFF),
      static_cast<uint8_t>((v >> 8) & 0xFF),
      static_cast<uint8_t>((v >> 16) & 0xFF),
      static_cast<uint8_t>((v >> 24) & 0xFF),
  }};
  return true;
}

bool NvsHandle::SetBytes(NvsKey k, const uint8_t *in, size_t len) {
  if (in == nullptr || len == 0)
    return false;
  Store()[k] = NvsEntry{std::vector<uint8_t>(in, in + len)};
  return true;
}

// ── Reset ──────────────────────────────────────────────────────────

void NvsHandle::ResetForTest() { Store().clear(); }

void nvs_factory_reset() {
  Store().clear();
  ApplyDefaults();
}

} // namespace tether::m5
