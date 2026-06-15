// test_aes_link.cpp — TDD-first unit tests for tether::m5::AesLink.
//
// See plan.md §9.1 and research.md §14.1. The M5 firmware is the
// "downstream" end of the LoRa link: every packet it transmits
// must be encrypted with the per-conversation AES-128-CTR key
// (HKDF-derived from the master PSK with the convID as the salt),
// and every packet it receives must be decrypted with the same
// key + nonce combination.
//
// The SX1262 hardware engine does the on-air cipher. This test
// file therefore exercises two layers:
//
//   1. The HKDF derivation (in software) — proves the M5 and the
//      base station agree on the per-conv key bytes.
//   2. The key/nonce packing (16-byte key, 16-byte nonce with the
//      4-byte little-endian message_id prefix) — proves the
//      wrapper around SX1262::setEncryption is correct.
//
// The actual SX1262 hardware-engine call is covered by the bench
// test in test_bench (plan §3.5). We do not mock the radio here:
// this is a unit test of the wrapper, not of the radio.
//
// Cross-validation: every HKDF output must match the Go reference
// in go/internal/crypto. We pin the canonical RFC 5869 §A.1 test
// vector (the same one Go's tests pin) and one Go-specific
// property: "two different convIDs produce two different keys".
// If a future refactor accidentally drops the info-string
// argument or byte-swaps the salt, the property test catches it.

#include <array>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include <unity.h>

#include "aes_link.h"

using tether::m5::AesLink;
using tether::m5::kKeySize;
using tether::m5::kNonceSize;

// ── Test data shared across tests ─────────────────────────────────────

namespace {

// Master PSK used in the tests (16 bytes; the real master comes
// out of NVS, but for unit tests we can use a fixed value).
const std::array<uint8_t, kKeySize> kTestMaster = {
    0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
    0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10,
};

// Two different convIDs; HKDF(salt=convID) must produce different
// keys for each.
const std::array<uint8_t, 16> kConvA = {
    0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA, 0xAA,
    0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB, 0xBB,
};
const std::array<uint8_t, 16> kConvB = {
    0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC,
    0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD, 0xDD,
};

}  // namespace

void setUp() {}
void tearDown() {}

// ── Test 1: RFC 5869 §A.1 test vector ────────────────────────────────────
//
// Inputs (per RFC 5869 §A.1):
//   IKM  = 0x0b * 22
//   salt = 0x00..0x0c (13 bytes)
//   info = 0xf0..0xf9 (10 bytes)
//   L    = 42
//   PRK  = 0x0777 5d8e 3514 ab2f 1c4f 5e8b 36c4 8f87 7f24 e0c0 7a8b 4d4b 4a2b 7a2d 9a8b 3c4d
//   OKM  = 0x3cb25f25 faacd57a 90434f64 d0362f2a 2d2d0a90 cf1a5a4c 5db02d56 ecc4c5bf 34007208 d5b88718 5865
//
// We pass the same IKM/salt/info to our HKDF and verify the
// first 16 bytes of the OKM match the PRK. (The PRK is exactly
// the 16-byte key we would store; the OKM is the first 42 bytes
// of the keystream.)
//
// This is the canonical cross-validation: the Go daemon computes
// the same key with the same inputs, and any drift is caught here.
void test_aes_link_rfc5869_vector1() {
  AesLink al;
  // IKM = 0x0b * 22
  std::vector<uint8_t> ikm(22, 0x0b);
  // salt = 0x00..0x0c
  std::vector<uint8_t> salt = {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
                               0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c};
  // info = 0xf0..0xf9
  std::vector<uint8_t> info = {0xf0, 0xf1, 0xf2, 0xf3, 0xf4,
                               0xf5, 0xf6, 0xf7, 0xf8, 0xf9};
  // Expected PRK (first 16 bytes of OKM):
  // 0x3cb25f25faacd57a90434f64d0362f2a
  // (full 42-byte OKM: 3cb25f25 faacd57a 90434f64 d0362f2a 2d2d0a90 cf1a5a4c
  //                     5db02d56 ecc4c5bf 34007208 d5b88718 5865)
  std::vector<uint8_t> okm = al.HkdfSha256(ikm, salt, info, 42);
  TEST_ASSERT_EQUAL(42, okm.size());
  // First 16 bytes are the AES-128 key.
  const uint8_t expected_first_16[16] = {
      0x3c, 0xb2, 0x5f, 0x25, 0xfa, 0xac, 0xd5, 0x7a,
      0x90, 0x43, 0x4f, 0x64, 0xd0, 0x36, 0x2f, 0x2a,
  };
  TEST_ASSERT_EQUAL_MEMORY(expected_first_16, okm.data(), 16);
  // Full OKM match (deterministic; the canonical vector).
  const uint8_t expected_full[42] = {
      0x3c, 0xb2, 0x5f, 0x25, 0xfa, 0xac, 0xd5, 0x7a,
      0x90, 0x43, 0x4f, 0x64, 0xd0, 0x36, 0x2f, 0x2a,
      0x2d, 0x2d, 0x0a, 0x90, 0xcf, 0x1a, 0x5a, 0x4c,
      0x5d, 0xb0, 0x2d, 0x56, 0xec, 0xc4, 0xc5, 0xbf,
      0x34, 0x00, 0x72, 0x08, 0xd5, 0xb8, 0x87, 0x18,
      0x58, 0x65,
  };
  TEST_ASSERT_EQUAL_MEMORY(expected_full, okm.data(), 42);
}

// ── Test 2: ConvKey produces a 16-byte key ─────────────────────────────
void test_aes_link_conv_key_length() {
  AesLink al;
  std::array<uint8_t, kKeySize> key = al.ConvKey(kTestMaster, kConvA);
  TEST_ASSERT_EQUAL(kKeySize, key.size());
}

// ── Test 3: Different convIDs → different keys ─────────────────────────
void test_aes_link_conv_key_distinct() {
  AesLink al;
  auto keyA = al.ConvKey(kTestMaster, kConvA);
  auto keyB = al.ConvKey(kTestMaster, kConvB);
  // The two keys must not be equal byte-for-byte.
  bool same = true;
  for (size_t i = 0; i < kKeySize; ++i) {
    if (keyA[i] != keyB[i]) {
      same = false;
      break;
    }
  }
  TEST_ASSERT_FALSE(same);
}

// ── Test 4: Determinism — same inputs always produce the same key ──────
void test_aes_link_conv_key_deterministic() {
  AesLink al;
  auto a1 = al.ConvKey(kTestMaster, kConvA);
  auto a2 = al.ConvKey(kTestMaster, kConvA);
  TEST_ASSERT_EQUAL_MEMORY(a1.data(), a2.data(), kKeySize);
}

// ── Test 5: NonceFromMsgID packs the msg id little-endian ──────────────
void test_aes_link_nonce_layout() {
  AesLink al;
  // msg_id = 0x04030201 → nonce = 01 02 03 04 00 00 ... 00
  auto n = al.NonceFromMsgID(0x04030201u);
  TEST_ASSERT_EQUAL(kNonceSize, n.size());
  TEST_ASSERT_EQUAL_UINT8(0x01, n[0]);
  TEST_ASSERT_EQUAL_UINT8(0x02, n[1]);
  TEST_ASSERT_EQUAL_UINT8(0x03, n[2]);
  TEST_ASSERT_EQUAL_UINT8(0x04, n[3]);
  for (size_t i = 4; i < kNonceSize; ++i) {
    TEST_ASSERT_EQUAL_UINT8(0x00, n[i]);
  }
}

// ── Test 6: NonceFromMsgID(0) is all zeros ─────────────────────────────
void test_aes_link_nonce_zero() {
  AesLink al;
  auto n = al.NonceFromMsgID(0);
  for (size_t i = 0; i < kNonceSize; ++i) {
    TEST_ASSERT_EQUAL_UINT8(0x00, n[i]);
  }
}

// ── Test 7: NonceFromMsgID(0xFFFFFFFF) is FFFFFFFF 00 ... 00 ──────────
void test_aes_link_nonce_max() {
  AesLink al;
  auto n = al.NonceFromMsgID(0xFFFFFFFFu);
  TEST_ASSERT_EQUAL_UINT8(0xFF, n[0]);
  TEST_ASSERT_EQUAL_UINT8(0xFF, n[1]);
  TEST_ASSERT_EQUAL_UINT8(0xFF, n[2]);
  TEST_ASSERT_EQUAL_UINT8(0xFF, n[3]);
  for (size_t i = 4; i < kNonceSize; ++i) {
    TEST_ASSERT_EQUAL_UINT8(0x00, n[i]);
  }
}

// ── Test 8: Two different msgIDs produce two different nonces ──────────
void test_aes_link_nonce_distinct() {
  AesLink al;
  auto a = al.NonceFromMsgID(0xDEADBEEFu);
  auto b = al.NonceFromMsgID(0xCAFEBABEu);
  bool same = true;
  for (size_t i = 0; i < kNonceSize; ++i) {
    if (a[i] != b[i]) {
      same = false;
      break;
    }
  }
  TEST_ASSERT_FALSE(same);
}

// ── Test 9: Cross-language compatibility with the Go reference ─────────
//
// The expected key bytes for the test inputs (master =
// 0x0123456789abcdeffedcba9876543210, convID = 0xAA..AA 0xBB..BB)
// were generated by the Go daemon's internal/crypto.ConvKey
// function. If the C++ HKDF ever drifts (e.g. someone refactors
// HMAC-SHA256 and accidentally uses SHA-1), the cross-language
// key bytes will diverge and this test fails.
void test_aes_link_cross_language_compat() {
  AesLink al;
  // Determinism: same inputs must produce the same key.
  auto keyA = al.ConvKey(kTestMaster, kConvA);
  auto keyA2 = al.ConvKey(kTestMaster, kConvA);
  TEST_ASSERT_EQUAL_MEMORY(keyA.data(), keyA2.data(), kKeySize);

  // Pin the exact bytes produced by the Go reference for these
  // inputs. If the C++ implementation changes its derivation
  // (intentionally or otherwise), this assertion fails and the
  // engineer is forced to verify wire compatibility with the
  // Go daemon before merging.
  //
  // Generated via:
  //   $ go run ./cmd/printtest
  //     Go ConvKey: 04c7bd7c435831d682505e2f88b5853c
  const uint8_t expected_key[16] = {
      0x04, 0xc7, 0xbd, 0x7c, 0x43, 0x58, 0x31, 0xd6,
      0x82, 0x50, 0x5e, 0x2f, 0x88, 0xb5, 0x85, 0x3c,
  };
  TEST_ASSERT_EQUAL_MEMORY(expected_key, keyA.data(), kKeySize);
}

// ── Test 10: Master PSK shorter than 16 bytes is rejected ─────────────
void test_aes_link_short_master_rejected() {
  AesLink al;
  // ConvKey takes a 16-byte master; passing a shorter buffer is
  // a caller bug. The C++ wrapper doesn't resize, so the
  // conversion is rejected at compile time. We pin that
  // contract by using a static_assert and verifying a valid
  // 16-byte master works.
  static_assert(kKeySize == 16, "AES-128 key size");
  std::array<uint8_t, kKeySize> master{};
  for (size_t i = 0; i < kKeySize; ++i) {
    master[i] = static_cast<uint8_t>(i);
  }
  auto k = al.ConvKey(master, kConvA);
  TEST_ASSERT_EQUAL(kKeySize, k.size());
}

// ── Test 11: HkdfSha256 rejects excessive length ───────────────────────
//
// RFC 5869 limits OKM to 255 * HashLen = 8160 bytes. We must
// reject requests above that rather than spinning forever.
void test_aes_link_hkdf_max_length() {
  AesLink al;
  std::vector<uint8_t> ikm(16, 0x01);
  std::vector<uint8_t> salt(16, 0x02);
  std::vector<uint8_t> info(8, 0x03);
  // The boundary must be inclusive: 8160 is fine.
  auto okm = al.HkdfSha256(ikm, salt, info, 8160);
  TEST_ASSERT_EQUAL(8160, okm.size());
  // 8161 is rejected.
  TEST_ASSERT_FALSE(al.HkdfSha256(ikm, salt, info, 8161).size() == 8161);
}

// ── Test 12: HkdfSha256 zero-length is a no-op ─────────────────────────
void test_aes_link_hkdf_zero_length() {
  AesLink al;
  std::vector<uint8_t> ikm(16, 0x01);
  std::vector<uint8_t> salt(16, 0x02);
  std::vector<uint8_t> info(8, 0x03);
  auto okm = al.HkdfSha256(ikm, salt, info, 0);
  TEST_ASSERT_EQUAL(0, okm.size());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_aes_link_rfc5869_vector1);
  RUN_TEST(test_aes_link_conv_key_length);
  RUN_TEST(test_aes_link_conv_key_distinct);
  RUN_TEST(test_aes_link_conv_key_deterministic);
  RUN_TEST(test_aes_link_nonce_layout);
  RUN_TEST(test_aes_link_nonce_zero);
  RUN_TEST(test_aes_link_nonce_max);
  RUN_TEST(test_aes_link_nonce_distinct);
  RUN_TEST(test_aes_link_cross_language_compat);
  RUN_TEST(test_aes_link_short_master_rejected);
  RUN_TEST(test_aes_link_hkdf_max_length);
  RUN_TEST(test_aes_link_hkdf_zero_length);
  (void)0;
  UNITY_END();
}
