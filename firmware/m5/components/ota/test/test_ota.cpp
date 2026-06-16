// test_ota.cpp — TDD-first unit tests for the M5 USB OTA
// update path (plan §9.4).
//
// In v1 the only OTA channel is USB: the developer runs
// `idf.py -p /dev/ttyUSB0 flash` from the dev machine, and
// the M5's ROM bootloader (built into the ESP32-S3) accepts
// the image. The component we add here is a thin wrapper
// around the standard `esp_ota_*` API plus a small post-OTA
// hook that:
//
//   1. Verifies the new image's signature (a SHA-256 of the
//      image, supplied by the developer at flash time).
//   2. Sets the boot partition to the new image.
//   3. On the next reboot, verifies the new image boots; if
//      it crashes within 5 seconds, rolls back to the
//      previous image.
//
// The host build tracks the same state machine without
// touching the real flash. The tests in this file pin the
// public surface:
//
//   * Init() — opens the "OTA partition" abstraction.
//   * Begin() — selects the next free OTA partition.
//   * WriteChunk() — appends a chunk of the new image.
//   * VerifyAndCommit() — checks the SHA-256, sets the new
//     partition as bootable.
//   * Rollback() — reverts to the previous partition.
//   * State() — current state machine value.

#include <array>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include <unity.h>

#include "ota.h"
#include "ota_lora.h"

using tether::m5::kOtaLoraMaxChunkSize;
using tether::m5::OtaLoraBegin;
using tether::m5::OtaLoraFeed;
using tether::m5::OtaState;
using tether::m5::OtaUpdater;
using tether::m5::Sha256;

// The host-side implementation lives in src/ota.cpp and exposes
// three globals for tests to assert against. We redeclare them
// here (the production build does not include this file).
namespace tether::m5 {
extern std::vector<uint8_t> g_ota_bytes;
extern bool g_ota_marked_bootable;
extern bool g_ota_marked_invalid;
} // namespace tether::m5
using tether::m5::g_ota_bytes;
using tether::m5::g_ota_marked_bootable;
using tether::m5::g_ota_marked_invalid;

void setUp() {
  g_ota_bytes.clear();
  g_ota_marked_bootable = false;
  g_ota_marked_invalid = false;
}
void tearDown() {
  g_ota_bytes.clear();
  g_ota_marked_bootable = false;
  g_ota_marked_invalid = false;
}

// ── Test 1: default state is kIdle ─────────────────────────────────────
void test_ota_default_state() {
  OtaUpdater upd;
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kIdle),
                    static_cast<int>(upd.State()));
}

// ── Test 2: Begin transitions to kWriting ──────────────────────────────
void test_ota_begin_transitions_state() {
  OtaUpdater upd;
  TEST_ASSERT_TRUE(upd.Begin());
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kWriting),
                    static_cast<int>(upd.State()));
}

// ── Test 3: WriteChunk appends bytes to the partition ──────────────────
void test_ota_write_chunk_appends() {
  OtaUpdater upd;
  upd.Begin();
  TEST_ASSERT_TRUE(
      upd.WriteChunk(reinterpret_cast<const uint8_t *>("HELLO"), 5));
  TEST_ASSERT_TRUE(
      upd.WriteChunk(reinterpret_cast<const uint8_t *>("WORLD"), 5));
  TEST_ASSERT_EQUAL(10, g_ota_bytes.size());
  TEST_ASSERT_EQUAL_STRING("HELLOWORLD",
                           reinterpret_cast<const char *>(g_ota_bytes.data()));
}

// ── Test 4: VerifyAndCommit with correct SHA-256 transitions to kReady
// and marks the partition bootable ─────────────────────────────────────
void test_ota_verify_and_commit_good_hash() {
  OtaUpdater upd;
  upd.Begin();
  const char *payload = "tether firmware v1.0.0";
  upd.WriteChunk(reinterpret_cast<const uint8_t *>(payload),
                 std::strlen(payload));
  // SHA-256 of the payload, computed externally (sha256sum).
  // For "tether firmware v1.0.0" the SHA-256 is:
  //   (computed inline below to avoid an external dependency)
  // We compute it programmatically with the Sha256 helper.
  Sha256 hasher;
  hasher.Update(reinterpret_cast<const uint8_t *>(payload),
                std::strlen(payload));
  std::array<uint8_t, 32> digest = hasher.Finalize();
  TEST_ASSERT_TRUE(upd.VerifyAndCommit(digest));
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kReady),
                    static_cast<int>(upd.State()));
  TEST_ASSERT_TRUE(g_ota_marked_bootable);
  TEST_ASSERT_FALSE(g_ota_marked_invalid);
}

// ── Test 5: VerifyAndCommit with wrong SHA-256 fails and marks the
// partition invalid (so the bootloader refuses to boot it) ────────────
void test_ota_verify_and_commit_bad_hash() {
  OtaUpdater upd;
  upd.Begin();
  upd.WriteChunk(reinterpret_cast<const uint8_t *>("anything"), 8);
  std::array<uint8_t, 32> wrong{};
  for (size_t i = 0; i < wrong.size(); ++i) {
    wrong[i] = static_cast<uint8_t>(i);
  }
  TEST_ASSERT_FALSE(upd.VerifyAndCommit(wrong));
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kVerifyFailed),
                    static_cast<int>(upd.State()));
  TEST_ASSERT_TRUE(g_ota_marked_invalid);
  TEST_ASSERT_FALSE(g_ota_marked_bootable);
}

// ── Test 6: Rollback reverts to the previous partition ────────────────
void test_ota_rollback() {
  OtaUpdater upd;
  upd.Begin();
  upd.Rollback();
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kRolledBack),
                    static_cast<int>(upd.State()));
}

// ── Test 7: WriteChunk with zero length is a no-op ────────────────────
void test_ota_write_zero_length() {
  OtaUpdater upd;
  upd.Begin();
  TEST_ASSERT_TRUE(upd.WriteChunk(nullptr, 0));
  TEST_ASSERT_EQUAL(0, g_ota_bytes.size());
}

// ── Test 8: WriteChunk before Begin fails (state machine) ────────────
void test_ota_write_before_begin() {
  OtaUpdater upd;
  TEST_ASSERT_FALSE(upd.WriteChunk(reinterpret_cast<const uint8_t *>("x"), 1));
  TEST_ASSERT_EQUAL(static_cast<int>(OtaState::kIdle),
                    static_cast<int>(upd.State()));
}

// ── Test 9: SHA-256 of the empty string is the canonical vector ──────
void test_ota_sha256_empty_string() {
  Sha256 hasher;
  std::array<uint8_t, 32> d = hasher.Finalize();
  // e3b0c442 98fc1c14 9afbf4c8 996fb924 27ae41e4 649b934c a495991b
  // 7852b855
  const uint8_t expected[32] = {
      0xe3, 0xb0, 0xc4, 0x42, 0x98, 0xfc, 0x1c, 0x14, 0x9a, 0xfb, 0xf4,
      0xc8, 0x99, 0x6f, 0xb9, 0x24, 0x27, 0xae, 0x41, 0xe4, 0x64, 0x9b,
      0x93, 0x4c, 0xa4, 0x95, 0x99, 0x1b, 0x78, 0x52, 0xb8, 0x55,
  };
  TEST_ASSERT_EQUAL_MEMORY(expected, d.data(), 32);
}

// ── Test 10: SHA-256 of "abc" matches the NIST FIPS 180-4 vector ─────
void test_ota_sha256_abc() {
  Sha256 hasher;
  hasher.Update(reinterpret_cast<const uint8_t *>("abc"), 3);
  std::array<uint8_t, 32> d = hasher.Finalize();
  // ba7816bf 8f01cfea 414140de 5dae2223 b00361a3 96177a9c
  // b410ff61 f20015ad
  const uint8_t expected[32] = {
      0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40,
      0xde, 0x5d, 0xae, 0x22, 0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17,
      0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad,
  };
  TEST_ASSERT_EQUAL_MEMORY(expected, d.data(), 32);
}

// ── Test 11: SHA-256 streaming — same hash for one-shot and chunked ─
void test_ota_sha256_streaming() {
  Sha256 a;
  a.Update(reinterpret_cast<const uint8_t *>("hello "), 6);
  a.Update(reinterpret_cast<const uint8_t *>("world"), 5);
  std::array<uint8_t, 32> hash_chunked = a.Finalize();
  Sha256 b;
  b.Update(reinterpret_cast<const uint8_t *>("hello world"), 11);
  std::array<uint8_t, 32> hash_one_shot = b.Finalize();
  TEST_ASSERT_EQUAL_MEMORY(hash_chunked.data(), hash_one_shot.data(), 32);
}

// ── v2 hook tests (plan §10.4) ────────────────────────────────────
// These tests pin the v2 OTA-LoRa hook surface. They assert
// the v1 build:
//   - OtaLoraBegin returns false (v1 has no OTA-LoRa path).
//   - OtaLoraFeed returns false for any input.
// v2 inverts both: the functions must return true on a
// successful invocation.

// Test 12: the v2 begin hook is callable in v1 and returns
// false.
void test_v2_ota_lora_begin_returns_false() {
  bool got = OtaLoraBegin();
  TEST_ASSERT_FALSE(got);
}

// Test 13: the v2 feed hook is callable in v1 and returns
// false for any input.
void test_v2_ota_lora_feed_returns_false() {
  const uint8_t chunk[16] = {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
                             0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F};
  bool got = OtaLoraFeed(chunk, sizeof(chunk));
  TEST_ASSERT_FALSE(got);
}

// Test 14: the v2 feed hook accepts a null pointer with
// zero length without crashing.
void test_v2_ota_lora_feed_null_zero() {
  bool got = OtaLoraFeed(nullptr, 0);
  TEST_ASSERT_FALSE(got);
}

// Test 15: the v2 feed hook handles a full-size chunk
// (kOtaLoraMaxChunkSize) without crashing.
void test_v2_ota_lora_feed_max_size() {
  std::vector<uint8_t> chunk(kOtaLoraMaxChunkSize, 0xCC);
  bool got = OtaLoraFeed(chunk.data(), chunk.size());
  TEST_ASSERT_FALSE(got);
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_ota_default_state);
  RUN_TEST(test_ota_begin_transitions_state);
  RUN_TEST(test_ota_write_chunk_appends);
  RUN_TEST(test_ota_verify_and_commit_good_hash);
  RUN_TEST(test_ota_verify_and_commit_bad_hash);
  RUN_TEST(test_ota_rollback);
  RUN_TEST(test_ota_write_zero_length);
  RUN_TEST(test_ota_write_before_begin);
  RUN_TEST(test_ota_sha256_empty_string);
  RUN_TEST(test_ota_sha256_abc);
  RUN_TEST(test_ota_sha256_streaming);
  RUN_TEST(test_v2_ota_lora_begin_returns_false);
  RUN_TEST(test_v2_ota_lora_feed_returns_false);
  RUN_TEST(test_v2_ota_lora_feed_null_zero);
  RUN_TEST(test_v2_ota_lora_feed_max_size);
  (void)0;
  UNITY_END();
}
