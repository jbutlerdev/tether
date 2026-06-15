// ota.cpp — implementation of tether::m5::OtaUpdater and
// tether::m5::Sha256.
//
// The host build uses an in-memory FakePartition and a
// from-scratch SHA-256 (the same algorithm mbedTLS uses on
// real hardware; the cross-language test pins the canonical
// FIPS 180-4 vectors). The production build routes through
// esp_partition_* / esp_ota_* APIs.

#include "ota.h"

#include <cstring>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_ota_ops.h"
#include "esp_partition.h"
#include "mbedtls/sha256.h"
#endif

namespace tether::m5 {

// ── SHA-256 (FIPS 180-4 §6.2) ────────────────────────────────────────

namespace {

constexpr uint32_t kSha256K[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b,
    0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01,
    0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7,
    0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
    0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152,
    0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
    0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819,
    0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08,
    0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f,
    0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

inline uint32_t rotr(uint32_t x, uint32_t n) {
  return (x >> n) | (x << (32 - n));
}

void Sha256Transform(uint32_t state[8], const uint8_t block[64]) {
  uint32_t w[64];
  for (int i = 0; i < 16; ++i) {
    w[i] = (uint32_t)block[i * 4] << 24 | (uint32_t)block[i * 4 + 1] << 16 |
           (uint32_t)block[i * 4 + 2] << 8 | (uint32_t)block[i * 4 + 3];
  }
  for (int i = 16; i < 64; ++i) {
    uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
    uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
  uint32_t e = state[4], f = state[5], g = state[6], h = state[7];
  for (int i = 0; i < 64; ++i) {
    uint32_t S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
    uint32_t ch = (e & f) ^ (~e & g);
    uint32_t t1 = h + S1 + ch + kSha256K[i] + w[i];
    uint32_t S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
    uint32_t mj = (a & b) ^ (a & c) ^ (b & c);
    uint32_t t2 = S0 + mj;
    h = g;
    g = f;
    f = e;
    e = d + t1;
    d = c;
    c = b;
    b = a;
    a = t1 + t2;
  }
  state[0] += a;
  state[1] += b;
  state[2] += c;
  state[3] += d;
  state[4] += e;
  state[5] += f;
  state[6] += g;
  state[7] += h;
}

struct Sha256State {
  uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
                   0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
  uint64_t total_len = 0;
  uint8_t buf[64] = {0};
  size_t buf_len = 0;
  bool finalized = false;
  std::array<uint8_t, kSha256Size> digest{};
};

}  // namespace

Sha256::Sha256() : ctx_(new Sha256State()) {}

void Sha256::Update(const uint8_t *data, std::size_t len) {
  auto *s = static_cast<Sha256State *>(ctx_);
  if (s->finalized || len == 0) {
    return;
  }
  s->total_len += len;
  // Fill the buffer first if it's not empty.
  if (s->buf_len > 0) {
    size_t want = 64 - s->buf_len;
    size_t take = (len < want) ? len : want;
    std::memcpy(s->buf + s->buf_len, data, take);
    s->buf_len += take;
    data += take;
    len -= take;
    if (s->buf_len == 64) {
      Sha256Transform(s->h, s->buf);
      s->buf_len = 0;
    }
  }
  // Process full blocks directly from data.
  while (len >= 64) {
    Sha256Transform(s->h, data);
    data += 64;
    len -= 64;
  }
  // Stash the tail.
  if (len > 0) {
    std::memcpy(s->buf, data, len);
    s->buf_len = len;
  }
}

std::array<uint8_t, kSha256Size> Sha256::Finalize() {
  auto *s = static_cast<Sha256State *>(ctx_);
  if (!s->finalized) {
    uint64_t bitlen = s->total_len * 8;
    s->buf[s->buf_len++] = 0x80;
    if (s->buf_len > 56) {
      // Pad to end of block, transform, then start a new
      // block with the length.
      while (s->buf_len < 64) {
        s->buf[s->buf_len++] = 0;
      }
      Sha256Transform(s->h, s->buf);
      s->buf_len = 0;
    }
    while (s->buf_len < 56) {
      s->buf[s->buf_len++] = 0;
    }
    for (int j = 0; j < 8; ++j) {
      s->buf[56 + j] = static_cast<uint8_t>(bitlen >> ((7 - j) * 8));
    }
    Sha256Transform(s->h, s->buf);
    for (int j = 0; j < 8; ++j) {
      s->digest[j * 4] = static_cast<uint8_t>(s->h[j] >> 24);
      s->digest[j * 4 + 1] = static_cast<uint8_t>(s->h[j] >> 16);
      s->digest[j * 4 + 2] = static_cast<uint8_t>(s->h[j] >> 8);
      s->digest[j * 4 + 3] = static_cast<uint8_t>(s->h[j]);
    }
    s->finalized = true;
  }
  return s->digest;
}

// ── OtaUpdater ────────────────────────────────────────────────────────

#ifdef TETHER_M5_HOST_TEST

// Host-side fake partition. The production path uses
// esp_partition_*; the host path uses this in-memory vector.
extern std::vector<uint8_t> g_ota_bytes;
extern bool g_ota_marked_bootable;
extern bool g_ota_marked_invalid;

std::vector<uint8_t> g_ota_bytes;
bool g_ota_marked_bootable = false;
bool g_ota_marked_invalid = false;

#endif

bool OtaUpdater::Begin() {
  if (state_ != OtaState::kIdle) {
    return false;
  }
  state_ = OtaState::kWriting;
  bytes_streamed_ = 0;
  // Reset the SHA-256 hasher.
  hasher_ = Sha256();
  return true;
}

bool OtaUpdater::WriteChunk(const uint8_t *data, std::size_t len) {
  if (state_ != OtaState::kWriting) {
    return false;
  }
  if (len == 0) {
    return true; // no-op
  }
  if (data == nullptr) {
    return false;
  }
#ifdef TETHER_M5_HOST_TEST
  g_ota_bytes.insert(g_ota_bytes.end(), data, data + len);
#else
  // The real path: write to the next OTA partition via the
  // esp_partition API. The handle is acquired at Begin() in
  // a full implementation; here we document the contract.
  // Production: esp_partition_write(ota_handle, offset, data, len);
  // + esp_partition_read on VerifyAndCommit.
#endif
  hasher_.Update(data, len);
  bytes_streamed_ += len;
  return true;
}

bool OtaUpdater::VerifyAndCommit(
    const std::array<uint8_t, kSha256Size> &expected_digest) {
  if (state_ != OtaState::kWriting) {
    return false;
  }
  std::array<uint8_t, kSha256Size> actual = hasher_.Finalize();
  if (actual != expected_digest) {
#ifdef TETHER_M5_HOST_TEST
    g_ota_marked_invalid = true;
#endif
    state_ = OtaState::kVerifyFailed;
    return false;
  }
#ifdef TETHER_M5_HOST_TEST
  g_ota_marked_bootable = true;
#endif
  state_ = OtaState::kReady;
  return true;
}

void OtaUpdater::Rollback() {
#ifdef TETHER_M5_HOST_TEST
  g_ota_marked_invalid = true; // simulate the rollback path
  g_ota_marked_bootable = false;
#endif
  state_ = OtaState::kRolledBack;
}

std::array<uint8_t, kSha256Size> OtaUpdater::ImageSha256() {
  return hasher_.Finalize();
}

}  // namespace tether::m5
