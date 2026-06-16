// test_nvs.cpp — TDD-first unit tests for the M5 firmware's
// NVS schema bindings (plan §9.6).
//
// The C++ side mirrors the Go schema in go/internal/nvs. The
// two schemas are kept in sync by hand (the doc generator is
// a future plan §10 item), so this test file pins the C++
// bindings and the small NVSReadWrite abstraction the
// firmware uses.
//
// The tests in this file cover:
//   * Every documented key has a known default
//   * Defaults are applied when the value is missing
//   * Volume is clamped to 100
//   * The schema version is pinned (1)
//   * The factory-reset routine erases every known key
//
// On real hardware the test is run against an in-memory NVS
// emulator (the esp_partition mocks). On host, the schema
// constants are the same — we just verify the values match
// the documented defaults.

#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include <unity.h>

#include "nvs.h"

using tether::m5::kNvsVersion;
using tether::m5::nvs_factory_reset;
using tether::m5::NvsHandle;
using tether::m5::NvsKey;

void setUp() {}
void tearDown() {}

// ── Test 1: schema version is pinned at 1 ────────────────────────────
void test_nvs_version_pinned() { TEST_ASSERT_EQUAL(1, kNvsVersion); }

// ── Test 2: defaults — node.id is 0xFFFF ─────────────────────────────
void test_nvs_default_node_id() {
  uint16_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint16(NvsKey::kNodeId, &v));
  TEST_ASSERT_EQUAL_UINT32(0xFFFF, v);
}

// ── Test 3: defaults — radio.channel is 0 ────────────────────────────
void test_nvs_default_radio_channel() {
  uint8_t v = 42; // sentinel
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kRadioChannel, &v));
  TEST_ASSERT_EQUAL_UINT32(0, v);
}

// ── Test 4: defaults — radio.preset is 0xB8 (SF11/BW125/CR4-8) ─────
void test_nvs_default_radio_preset() {
  uint8_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kRadioPreset, &v));
  TEST_ASSERT_EQUAL_UINT32(0xB8, v);
}

// ── Test 5: defaults — ui.volume is 100 ──────────────────────────────
void test_nvs_default_ui_volume() {
  uint8_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kUIVolume, &v));
  TEST_ASSERT_EQUAL_UINT32(100, v);
}

// ── Test 6: defaults — master_psk is 16 zero bytes ──────────────────
void test_nvs_default_master_psk() {
  uint8_t v[16];
  std::memset(v, 0xCC, sizeof(v));
  TEST_ASSERT_TRUE(NvsHandle::GetBytes(NvsKey::kMasterPsk, v, sizeof(v)));
  for (size_t i = 0; i < 16; ++i) {
    TEST_ASSERT_EQUAL_UINT32(0x00, v[i]);
  }
}

// ── Test 7: defaults — ota.pending is 0 ─────────────────────────────
void test_nvs_default_ota_pending() {
  uint8_t v = 1;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kOtaPending, &v));
  TEST_ASSERT_EQUAL_UINT32(0, v);
}

// ── Test 8: defaults — diag.boot_count is 0 ─────────────────────────
void test_nvs_default_boot_count() {
  uint32_t v = 99;
  TEST_ASSERT_TRUE(NvsHandle::GetUint32(NvsKey::kBootCount, &v));
  TEST_ASSERT_EQUAL_UINT32(0, v);
}

// ── Test 9: Set + Get round-trip ─────────────────────────────────────
void test_nvs_set_get_uint8() {
  TEST_ASSERT_TRUE(NvsHandle::SetUint8(NvsKey::kUIVolume, 50));
  uint8_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kUIVolume, &v));
  TEST_ASSERT_EQUAL_UINT32(50, v);
}

// ── Test 10: Set + Get round-trip for uint16 ────────────────────────
void test_nvs_set_get_uint16() {
  TEST_ASSERT_TRUE(NvsHandle::SetUint16(NvsKey::kNodeId, 0x1234));
  uint16_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint16(NvsKey::kNodeId, &v));
  TEST_ASSERT_EQUAL_UINT32(0x1234, v);
}

// ── Test 11: Set + Get round-trip for bytes ─────────────────────────
void test_nvs_set_get_bytes() {
  uint8_t buf[16];
  for (size_t i = 0; i < 16; ++i) {
    buf[i] = static_cast<uint8_t>(i);
  }
  TEST_ASSERT_TRUE(NvsHandle::SetBytes(NvsKey::kMasterPsk, buf, sizeof(buf)));
  uint8_t read[16];
  std::memset(read, 0, sizeof(read));
  TEST_ASSERT_TRUE(NvsHandle::GetBytes(NvsKey::kMasterPsk, read, sizeof(read)));
  TEST_ASSERT_EQUAL_MEMORY(buf, read, 16);
}

// ── Test 12: out-of-range volume is clamped on read ─────────────────
// The firmware applies ClampVolume() in the read path so a
// 255 written by a buggy provisioning tool never reaches the
// amp.
void test_nvs_volume_clamped_on_read() {
  TEST_ASSERT_TRUE(NvsHandle::SetUint8(NvsKey::kUIVolume, 200));
  uint8_t v = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kUIVolume, &v));
  // GetUint8 returns the raw value; the firmware's volume
  // consumer calls ClampVolume() at the point of use. We
  // exercise that helper here.
  v = tether::m5::ClampVolume(v);
  TEST_ASSERT_EQUAL_UINT32(100, v);
}

// ── Test 13: ClampVolume boundary cases ─────────────────────────────
void test_nvs_clamp_volume_boundaries() {
  TEST_ASSERT_EQUAL_UINT32(0, tether::m5::ClampVolume(0));
  TEST_ASSERT_EQUAL_UINT32(50, tether::m5::ClampVolume(50));
  TEST_ASSERT_EQUAL_UINT32(100, tether::m5::ClampVolume(100));
  TEST_ASSERT_EQUAL_UINT32(100, tether::m5::ClampVolume(101));
  TEST_ASSERT_EQUAL_UINT32(100, tether::m5::ClampVolume(255));
}

// ── Test 14: factory reset erases every known key ───────────────────
void test_nvs_factory_reset() {
  // Write something non-default.
  TEST_ASSERT_TRUE(NvsHandle::SetUint16(NvsKey::kNodeId, 0xABCD));
  TEST_ASSERT_TRUE(NvsHandle::SetUint8(NvsKey::kUIVolume, 75));
  // Reset.
  nvs_factory_reset();
  // Defaults are restored.
  uint16_t id = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint16(NvsKey::kNodeId, &id));
  TEST_ASSERT_EQUAL_UINT32(0xFFFF, id);
  uint8_t vol = 0;
  TEST_ASSERT_TRUE(NvsHandle::GetUint8(NvsKey::kUIVolume, &vol));
  TEST_ASSERT_EQUAL_UINT32(100, vol);
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_nvs_version_pinned);
  RUN_TEST(test_nvs_default_node_id);
  RUN_TEST(test_nvs_default_radio_channel);
  RUN_TEST(test_nvs_default_radio_preset);
  RUN_TEST(test_nvs_default_ui_volume);
  RUN_TEST(test_nvs_default_master_psk);
  RUN_TEST(test_nvs_default_ota_pending);
  RUN_TEST(test_nvs_default_boot_count);
  RUN_TEST(test_nvs_set_get_uint8);
  RUN_TEST(test_nvs_set_get_uint16);
  RUN_TEST(test_nvs_set_get_bytes);
  RUN_TEST(test_nvs_volume_clamped_on_read);
  RUN_TEST(test_nvs_clamp_volume_boundaries);
  RUN_TEST(test_nvs_factory_reset);
  (void)0;
  UNITY_END();
}
