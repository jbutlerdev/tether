// ota.h — Tether M5 USB OTA update path (plan §9.4).
//
// In v1 the only OTA channel is USB: the developer runs
// `idf.py -p /dev/ttyUSB0 flash` from the dev machine, and
// the M5's ROM bootloader accepts the image. The component
// we add here is a thin wrapper that:
//
//   1. Tracks the state machine (kIdle → kWriting → kReady).
//   2. Computes the SHA-256 of the image as it streams in
//      (so we can verify against a developer-supplied digest
//      without re-reading the partition).
//   3. Marks the partition bootable on success, invalid on
//      failure.
//   4. Exposes a Rollback() hook the conv manager can call on
//      a boot-loop (the standard esp_ota_mark_app_invalid_rollback
//      path).
//
// The host build uses an in-memory FakePartition to back the
// state machine. On real hardware the implementation routes
// through esp_partition_* / esp_ota_* APIs.

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <string>

namespace tether::m5 {

// OtaState is the small state machine. The transitions are:
//
//   kIdle --Begin()--> kWriting
//   kWriting --VerifyAndCommit(ok)--> kReady
//   kWriting --VerifyAndCommit(fail)--> kVerifyFailed
//   kVerifyFailed --Rollback()--> kRolledBack
//   kReady --Rollback()--> kRolledBack
//
// kReady means the new image is bootable on the next reset.
enum class OtaState : uint8_t {
  kIdle = 0,
  kWriting = 1,
  kReady = 2,
  kVerifyFailed = 3,
  kRolledBack = 4,
};

// kSha256Size is the SHA-256 digest length in bytes.
inline constexpr std::size_t kSha256Size = 32;

// Sha256 is a streaming SHA-256 hasher. The interface is the
// minimum we need for OTA verification; it is intentionally
// not a full hash.Hash implementation.
class Sha256 {
public:
  Sha256();

  // Update adds `len` bytes from `data` to the running hash.
  // Calling Update after Finalize is undefined; the test
  // suite always pairs N Updates with one Finalize.
  void Update(const uint8_t *data, std::size_t len);

  // Finalize returns the 32-byte digest. The hasher is
  // single-shot: a second call to Finalize returns the same
  // digest (matching the Go stdlib's sha256.Sum256 behavior).
  std::array<uint8_t, kSha256Size> Finalize();

private:
  // Internal state. The on-host implementation is a simple
  // struct; the production implementation wraps mbedTLS.
  void *ctx_; // opaque to keep the header dependency-light
};

class OtaUpdater {
public:
  OtaUpdater() = default;

  // Begin selects the next free OTA partition. Returns false
  // if no partition is available (the dev flashed two
  // images in a row without committing the first).
  bool Begin();

  // WriteChunk appends `len` bytes from `data` to the
  // selected partition. Returns false if the state machine
  // is not in kWriting.
  bool WriteChunk(const uint8_t *data, std::size_t len);

  // VerifyAndCommit checks the running SHA-256 of the
  // streamed image against `expected_digest`. On a match the
  // partition is marked bootable and the state moves to
  // kReady. On a mismatch the partition is marked invalid
  // (so the bootloader will not run it on next reset) and
  // the state moves to kVerifyFailed.
  bool VerifyAndCommit(const std::array<uint8_t, kSha256Size> &expected_digest);

  // Rollback reverts to the previous boot partition. Called
  // by the conv manager on a boot-loop detection.
  void Rollback();

  // State returns the current state machine value.
  OtaState State() const { return state_; }

  // BytesStreamed is the number of bytes the OTA has
  // accumulated so far. Used by the conv manager to display
  // a progress bar to the operator TUI.
  std::size_t BytesStreamed() const { return bytes_streamed_; }

  // ImageSha256 returns the SHA-256 of the bytes streamed so
  // far. (Same value as VerifyAndCommit would compute.) Used
  // by the operator TUI to print the final digest for manual
  // verification.
  std::array<uint8_t, kSha256Size> ImageSha256();

private:
  OtaState state_ = OtaState::kIdle;
  std::size_t bytes_streamed_ = 0;
  Sha256 hasher_;
};

} // namespace tether::m5
