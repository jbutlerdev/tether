// aes_link.h — Tether M5 link-layer crypto (plan §9.1).
//
// This header is the C++ side of the per-conversation AES-128-CTR
// scheme. The M5 firmware derives the same 16-byte key as the Go
// base station via HKDF-SHA256, then hands the (key, nonce) pair
// to the SX1262 hardware engine via radio.setEncryption(...). The
// radio task (firmware/m5/components/radio_task) is the only
// caller of SetEncryption().
//
// The implementation is in src/aes_link.cpp. The header is
// deliberately small: it exposes only the primitives the radio
// task and the conv manager need, and the HKDF building blocks
// for unit tests.

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <vector>

namespace tether::m5 {

// kKeySize is the AES-128 key length (16 bytes).
inline constexpr std::size_t kKeySize = 16;

// kNonceSize is the SX1262 nonce length (16 bytes). The first 4
// bytes are the message id (little-endian); the remaining 12 are
// reserved (zero in v1).
inline constexpr std::size_t kNonceSize = 16;

// kHkdfMaxOKMLen is the largest output length HKDF will produce
// (RFC 5869 §2.3: 255 * 32 = 8160 bytes for SHA-256).
inline constexpr std::size_t kHkdfMaxOKMLen = 8160;

// convKeyInfo is the canonical info string for the Tether
// link-layer key. It must match go/internal/crypto's value
// exactly. Changing it invalidates every previously derived
// key, so it must only change as part of a versioned protocol
// upgrade.
inline constexpr const char *kConvKeyInfo = "tether-link-v1";

// AesLink is a stateless helper class. All methods are pure
// functions of their inputs; the class exists only to group the
// crypto primitives under a single namespace and to give the
// test suite a place to hang its hooks.
class AesLink {
public:
  AesLink() = default;

  // HkdfSha256 is the RFC 5869 HKDF-Extract-and-Expand with
  // HMAC-SHA256. Inputs match the Go reference in
  // go/internal/crypto.HKDFSHA256 byte-for-byte.
  //
  // Parameters:
  //   ikm   — input keying material (the master PSK)
  //   salt  — optional non-secret salt; empty is legal
  //   info  — optional context/info string
  //   length — desired output length, 0..kHkdfMaxOKMLen
  //
  // Returns the OKM. An over-length request returns an empty
  // vector (the caller must check the size against `length`).
  std::vector<uint8_t> HkdfSha256(const std::vector<uint8_t> &ikm,
                                  const std::vector<uint8_t> &salt,
                                  const std::vector<uint8_t> &info,
                                  std::size_t length) const;

  // ConvKey derives the per-conversation AES-128 key from a
  // 16-byte master PSK and a 16-byte conversation id. It is
  // the C++ mirror of go/internal/crypto.ConvKey.
  std::array<uint8_t, kKeySize>
  ConvKey(const std::array<uint8_t, kKeySize> &master,
          const std::array<uint8_t, 16> &conv_id) const;

  // NonceFromMsgID builds the 16-byte link-layer nonce for a
  // given 32-bit message id. The first 4 bytes are the msg id
  // in little-endian; the remaining 12 are zero.
  std::array<uint8_t, kNonceSize> NonceFromMsgID(uint32_t msg_id) const;
};

} // namespace tether::m5
