// conv_db.h — Tether M5 conversation DB (plan.md §5.2 / research.md §9).
//
// Each conversation is a UUID-identified record with metadata and a
// rolling 50-entry history. The DB lives on the typed LittleFS VFS
// from plan §5.1 at `/conv/<uuid>/{meta.bin,history.bin}`. Up to
// 16 conversations are stored; Upsert beyond the 16th is rejected
// with ESP_ERR_NO_MEM (we never silently drop entries).
//
// The class is thread-safe — every public method takes an internal
// std::mutex. On host the tests run this code path under multiple
// threads to verify there are no torn writes.
//
// Layout on disk (binary, little-endian, no padding):
//   /conv/<uuid>/meta.bin     — ConvInfo struct (see below)
//   /conv/<uuid>/history.bin  — ring of HistoryEntry (see below)
//   /conv/<uuid>/ratchet.bin  — AES counter (Phase 7 will use it)
//
// The `name` field is exactly 24 bytes wide; Upsert enforces a NUL
// terminator within the 24-byte buffer and truncates with a NUL if
// the input string is longer. Upsert of a `name` whose 24-byte
// buffer has no NUL returns ESP_ERR_INVALID_ARG.

#pragma once

#include <cstddef>
#include <cstdint>
#include <mutex>
#include <string>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include "littlefs_vfs.h"

namespace tether::m5 {

inline constexpr size_t kConvNameMax = 24;     // NUL-terminated within 24 B
inline constexpr size_t kConvTargetMax = 128;  // matrix room id / forge uuid
inline constexpr size_t kConvIdSize = 16;      // UUID bytes
inline constexpr size_t kConvEncKeySize = 16;  // AES-128
inline constexpr size_t kHistoryEntryTextMax = 64;
inline constexpr size_t kHistoryRingMax = 50;
inline constexpr size_t kConvDbMax = 16;       // hard cap from research.md §9

// Fixed-size ConvInfo; binary-stable for the on-disk format.
// The struct uses natural alignment. We store the entire
// `sizeof(ConvInfo)` bytes verbatim on disk; the on-disk size
// is exactly the in-memory size. Reads and writes both go
// through this struct, so the layout only needs to be
// self-consistent — there is no cross-version compatibility
// concern inside the M5 firmware.
struct ConvInfo {
  uint8_t id[kConvIdSize] = {};
  char name[kConvNameMax] = {};
  uint8_t kind = 0;          // 0=Matrix, 1=Forge, 2=Broadcast
  char target[kConvTargetMax] = {};
  uint8_t enc_key[kConvEncKeySize] = {};
  int64_t last_activity_ms = 0;
  uint16_t unread = 0;
  bool exists = false;       // tombstone flag (kept in case the wire
                             // format needs to distinguish "removed"
                             // from "never existed")
};
static_assert(sizeof(ConvInfo) > 0, "ConvInfo must be non-empty");

// Fixed-size HistoryEntry; one per ring slot.
struct HistoryEntry {
  uint32_t msg_id = 0;
  int64_t timestamp_ms = 0;
  uint8_t direction = 0;     // 0=out, 1=in, 2=system
  char text[kHistoryEntryTextMax] = {};
  uint8_t status = 0;        // 0=pending, 1=acked, 2=failed
};
static_assert(sizeof(HistoryEntry) > 0, "HistoryEntry must be non-empty");

// In-memory summary of a single conv directory; used by List().
struct ConvSummary {
  uint8_t id[kConvIdSize];
  char name[kConvNameMax];
  uint8_t kind;
  int64_t last_activity_ms;
  uint16_t unread;
};

// The conversation DB. Owns an LfsVfs instance so tests can
// mount against a tmpfs root without instantiating a real
// SdCard.
class ConvDb {
public:
  ConvDb() = default;
  ~ConvDb() = default;

  ConvDb(const ConvDb &) = delete;
  ConvDb &operator=(const ConvDb &) = delete;

  // Mount the underlying VFS at `root` and create /conv if missing.
  // Idempotent. The root defaults to the per-test tmpfs directory
  // in host builds and to `/sdcard` on real hardware.
  esp_err_t Init(const char *root = "/sdcard");

  // The VFS instance (for tests and for callers that need
  // additional filesystem access at the conv root).
  LfsVfs &Vfs() { return vfs_; }
  const LfsVfs &Vfs() const { return vfs_; }

  // Insert or replace a conv. On success returns ESP_OK; the
  // caller may pass `unread = 0` to clear the badge.
  // Returns:
  //   ESP_ERR_INVALID_ARG — id is all zero, or name has no NUL
  //                         within its 24-byte field
  //   ESP_ERR_NO_MEM      — DB already holds 16 conversations
  //                         and the id is not in the set (a no-
  //                         dup is treated as an overwrite and
  //                         does NOT count against the cap)
  esp_err_t Upsert(const ConvInfo &c);

  // Remove a conv. Returns ESP_OK on success (idempotent: removing
  // a missing conv is a no-op and returns ESP_OK).
  esp_err_t Remove(const uint8_t id[kConvIdSize]);

  // Look up a conv by id. `out` may be nullptr to test existence.
  // Returns ESP_OK on success, ESP_ERR_NOT_FOUND if absent.
  esp_err_t Get(const uint8_t id[kConvIdSize], ConvInfo *out);

  // List the conversations, sorted by last_activity_ms desc.
  // Caps the result at kConvDbMax (16). When more than 16 are
  // stored (e.g. a future version that lifts the cap), the
  // highest-activity 16 are returned.
  std::vector<ConvInfo> List();

  // Append a history entry to the ring. The ring is bounded at
  // kHistoryRingMax (50). Newer entries overwrite the oldest.
  // Returns ESP_ERR_NOT_FOUND if the conv does not exist.
  esp_err_t AppendHistory(const uint8_t id[kConvIdSize],
                          const HistoryEntry &e);

  // Read up to `max` history entries, most recent first.
  std::vector<HistoryEntry> GetHistory(const uint8_t id[kConvIdSize],
                                       size_t max);

  // Wipe the history ring. Idempotent.
  esp_err_t ClearHistory(const uint8_t id[kConvIdSize]);

  // Count conversations. O(n) over /conv.
  size_t Size();

  // Internal: count conversations, requires the caller to hold
  // `mutex_`. Used by Upsert's cap check.
  size_t SizeLocked();

private:
  static std::string IdToPath(const uint8_t id[kConvIdSize]);
  static void IdToString(const uint8_t id[kConvIdSize], char *out, size_t n);

  LfsVfs vfs_;
  std::mutex mutex_;
};

} // namespace tether::m5
