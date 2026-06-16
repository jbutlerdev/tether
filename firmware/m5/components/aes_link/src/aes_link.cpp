// aes_link.cpp — implementation of tether::m5::AesLink.
//
// The implementation is a from-scratch port of the Go reference
// in go/internal/crypto. The Go version uses crypto/sha256 from
// the standard library; the C++ version uses the ESP-IDF bundled
// mbedTLS (or the freertos_shim's hash on the host build) so we
// don't have to ship a parallel SHA-256.
//
// On the host build (TETHER_M5_HOST_TEST defined) we use a
// minimal SHA-256 implementation we ship inline below; it is
// exercised by the unit tests in test/test_aes_link.cpp. On real
// hardware, mbedTLS's mbedtls_sha256() is the underlying engine.

#include "aes_link.h"

#include <cstring>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h" // provides tether::m5::test::sha256 on host
#else
#include "esp_log.h"
#include "mbedtls/sha256.h"
#endif

namespace tether::m5 {

namespace {

// HMAC-SHA256. Mirrors the Go reference.
constexpr std::size_t kSha256BlockSize = 64;
constexpr std::size_t kSha256DigestSize = 32;

struct HmacSha256State {
  std::array<uint8_t, kSha256BlockSize> ipad{};
  std::array<uint8_t, kSha256BlockSize> opad{};
  std::array<uint8_t, kSha256DigestSize> inner_digest{};
  bool inner_finalized = false;
};

// SHA-256 wrapper. On host this routes through the freertos_shim
// helper; on real hardware it calls mbedTLS.
void Sha256(const uint8_t *data, std::size_t len, uint8_t out[32]) {
#ifdef TETHER_M5_HOST_TEST
  tether::m5::test::sha256(data, len, out);
#else
  mbedtls_sha256_context ctx;
  mbedtls_sha256_init(&ctx);
  mbedtls_sha256_starts(&ctx, /*is224=*/false);
  mbedtls_sha256_update(&ctx, data, len);
  mbedtls_sha256_finish(&ctx, out);
  mbedtls_sha256_free(&ctx);
#endif
}

void HmacSha256Init(HmacSha256State &ctx, const uint8_t *key,
                    std::size_t key_len) {
  std::array<uint8_t, kSha256BlockSize> k{};
  if (key_len > kSha256BlockSize) {
    Sha256(key, key_len, k.data());
  } else {
    std::memcpy(k.data(), key, key_len);
  }
  for (std::size_t i = 0; i < kSha256BlockSize; ++i) {
    ctx.ipad[i] = k[i] ^ 0x36;
    ctx.opad[i] = k[i] ^ 0x5c;
  }
  // Pre-compute the inner digest of (ipad) so the first Sum call
  // can produce HMAC of an empty message (matches Go's behavior).
  std::array<uint8_t, kSha256BlockSize> buf{};
  std::memcpy(buf.data(), ctx.ipad.data(), kSha256BlockSize);
  Sha256(buf.data(), kSha256BlockSize, ctx.inner_digest.data());
  ctx.inner_finalized = true;
}

void HmacSha256Update(HmacSha256State &ctx, const uint8_t *data,
                      std::size_t len) {
  if (!ctx.inner_finalized || len == 0) {
    return;
  }
  // Tether's HKDF call sites always write all of the message in
  // a single call before Sum, so we re-compute the inner digest
  // from (ipad || data) in one shot.
  std::vector<uint8_t> buf(kSha256BlockSize + len);
  std::memcpy(buf.data(), ctx.ipad.data(), kSha256BlockSize);
  std::memcpy(buf.data() + kSha256BlockSize, data, len);
  Sha256(buf.data(), buf.size(), ctx.inner_digest.data());
}

void HmacSha256Sum(const HmacSha256State &ctx, uint8_t out[32]) {
  std::array<uint8_t, kSha256BlockSize + kSha256DigestSize> buf{};
  std::memcpy(buf.data(), ctx.opad.data(), kSha256BlockSize);
  std::memcpy(buf.data() + kSha256BlockSize, ctx.inner_digest.data(),
              kSha256DigestSize);
  Sha256(buf.data(), buf.size(), out);
}

void HmacSha256(const uint8_t *key, std::size_t key_len, const uint8_t *msg,
                std::size_t msg_len, uint8_t out[32]) {
  HmacSha256State ctx;
  HmacSha256Init(ctx, key, key_len);
  HmacSha256Update(ctx, msg, msg_len);
  HmacSha256Sum(ctx, out);
}

} // namespace

// HkdfSha256 — see header.
std::vector<uint8_t> AesLink::HkdfSha256(const std::vector<uint8_t> &ikm,
                                         const std::vector<uint8_t> &salt,
                                         const std::vector<uint8_t> &info,
                                         std::size_t length) const {
  std::vector<uint8_t> out;
  if (length == 0) {
    return out;
  }
  if (length > kHkdfMaxOKMLen) {
    return out; // empty vector signals failure; caller checks size
  }

  // RFC 5869 §2.2: empty salt → HashLen zeros.
  std::vector<uint8_t> effective_salt = salt;
  if (effective_salt.empty()) {
    effective_salt.assign(kSha256DigestSize, 0);
  }

  // Extract: PRK = HMAC-SHA256(salt, IKM)
  uint8_t prk[kSha256DigestSize];
  HmacSha256(effective_salt.data(), effective_salt.size(), ikm.data(),
             ikm.size(), prk);

  // Expand: OKM = T(1) || T(2) || ... || T(N)
  //   T(0) = empty
  //   T(i) = HMAC-SHA256(PRK, T(i-1) || info || i)
  out.reserve(length);
  std::vector<uint8_t> t;
  uint8_t counter = 1;
  while (out.size() < length) {
    std::vector<uint8_t> msg;
    msg.reserve(t.size() + info.size() + 1);
    msg.insert(msg.end(), t.begin(), t.end());
    msg.insert(msg.end(), info.begin(), info.end());
    msg.push_back(counter);
    t.assign(kSha256DigestSize, 0);
    HmacSha256(prk, kSha256DigestSize, msg.data(), msg.size(), t.data());
    out.insert(out.end(), t.begin(), t.end());
    counter++;
  }
  out.resize(length);
  return out;
}

// ConvKey — see header.
std::array<uint8_t, kKeySize>
AesLink::ConvKey(const std::array<uint8_t, kKeySize> &master,
                 const std::array<uint8_t, 16> &conv_id) const {
  // HKDF(master, salt=conv_id, info="tether-link-v1", L=16)
  std::vector<uint8_t> master_v(master.begin(), master.end());
  std::vector<uint8_t> salt_v(conv_id.begin(), conv_id.end());
  std::vector<uint8_t> info(kConvKeyInfo,
                            kConvKeyInfo + std::strlen(kConvKeyInfo));
  std::vector<uint8_t> okm = HkdfSha256(master_v, salt_v, info, kKeySize);
  std::array<uint8_t, kKeySize> out{};
  if (okm.size() == kKeySize) {
    std::memcpy(out.data(), okm.data(), kKeySize);
  }
  return out;
}

// NonceFromMsgID — see header.
std::array<uint8_t, kNonceSize> AesLink::NonceFromMsgID(uint32_t msg_id) const {
  std::array<uint8_t, kNonceSize> n{};
  n[0] = static_cast<uint8_t>(msg_id & 0xFF);
  n[1] = static_cast<uint8_t>((msg_id >> 8) & 0xFF);
  n[2] = static_cast<uint8_t>((msg_id >> 16) & 0xFF);
  n[3] = static_cast<uint8_t>((msg_id >> 24) & 0xFF);
  // bytes 4..15 are already zero.
  return n;
}

} // namespace tether::m5
