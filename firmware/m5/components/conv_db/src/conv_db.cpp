// conv_db.cpp — implementation of tether::m5::ConvDb (plan.md §5.2).
//
// On-disk layout:
//
//   /conv/<hex32>/meta.bin     — single ConvInfo struct
//   /conv/<hex32>/history.bin  — ring of HistoryEntry records
//                                (length-prefixed header:
//                                 uint32 count, then count ×
//                                 HistoryEntry, FIFO-style)
//   /conv/<hex32>/ratchet.bin  — AES counter, 16 bytes (Phase 7)
//
// The implementation serializes on `mutex_`; multiple tasks can
// share the ConvDb instance. On real hardware this is the
// conv_manager task (Phase 4 §5.5) and the ui_state task
// (Phase 4 §5.4).
//
// Atomic writes: every Upsert is a write-to-tmp + rename, so a
// mid-write crash leaves the previous valid record on disk. The
// test suite verifies that no `*.tmp` files are left behind.

#include "conv_db.h"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <utility>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#endif

namespace tether::m5 {

namespace {

constexpr char kConvDir[] = "/conv";
constexpr char kMetaFile[] = "meta.bin";
constexpr char kHistoryFile[] = "history.bin";
constexpr char kRatchetFile[] = "ratchet.bin";
constexpr char kTmpSuffix[] = ".tmp";

// History ring on-disk header. We keep a count and a write index
// (next slot to write to) so that the ring can wrap.
struct HistoryHeader {
  uint32_t magic;     // 'TRH1' = 0x31524854
  uint32_t count;     // number of valid entries (≤ kHistoryRingMax)
  uint32_t write_idx; // next slot to write (mod kHistoryRingMax)
  uint32_t reserved;  // align to 16 bytes
};
static_assert(sizeof(HistoryHeader) == 16, "HistoryHeader must be 16 bytes");
constexpr uint32_t kHistoryMagic = 0x31524854U; // 'T','R','H','1' little-endian

bool IdIsAllZero(const uint8_t id[kConvIdSize]) {
  for (size_t i = 0; i < kConvIdSize; ++i) {
    if (id[i] != 0)
      return false;
  }
  return true;
}

bool NameFieldHasNul(const ConvInfo &c) {
  for (size_t i = 0; i < kConvNameMax; ++i) {
    if (c.name[i] == '\0')
      return true;
  }
  return false;
}

void TruncateNameToField(ConvInfo &) {
  // No-op. The Upsert path enforces the NUL-in-field invariant by
  // rejecting names whose 24-byte field is not NUL-terminated.
  // (This is a no-op retained for symmetry with NameFieldHasNul;
  // callers should not rely on it.)
}

size_t AtomicWriteFile(LfsVfs &vfs, const char *path, const void *data,
                       size_t len) {
  std::string tmp = std::string(path) + kTmpSuffix;
  FILE *fp = vfs.Open(tmp.c_str(), "wb");
  if (!fp) {
    return 0;
  }
  size_t n = fwrite(data, 1, len, fp);
  fclose(fp);
  if (n != len) {
    vfs.Remove(tmp.c_str());
    return 0;
  }
  if (vfs.Rename(tmp.c_str(), path) != 0) {
    vfs.Remove(tmp.c_str());
    return 0;
  }
  return len;
}

bool ReadFileAll(LfsVfs &vfs, const char *path, void *out, size_t len) {
  FILE *fp = vfs.Open(path, "rb");
  if (!fp) {
    return false;
  }
  size_t n = fread(out, 1, len, fp);
  fclose(fp);
  return n == len;
}

} // namespace

// ── ID <-> path conversion ────────────────────────────────────────────

void ConvDb::IdToString(const uint8_t id[kConvIdSize], char *out, size_t n) {
  // Hex-encode 16 bytes = 32 hex chars + 1 NUL. n must be ≥ 33.
  static const char hex[] = "0123456789abcdef";
  if (n < 33) {
    if (n > 0)
      out[0] = '\0';
    return;
  }
  for (size_t i = 0; i < kConvIdSize; ++i) {
    out[i * 2] = hex[(id[i] >> 4) & 0xF];
    out[i * 2 + 1] = hex[id[i] & 0xF];
  }
  out[32] = '\0';
}

std::string ConvDb::IdToPath(const uint8_t id[kConvIdSize]) {
  char hex[33];
  IdToString(id, hex, sizeof hex);
  return std::string(kConvDir) + "/" + hex;
}

// ── Public API ────────────────────────────────────────────────────────

esp_err_t ConvDb::Init(const char *root) {
  std::lock_guard<std::mutex> lk(mutex_);
  esp_err_t rc = vfs_.Mount(root);
  if (rc != ESP_OK) {
    return rc;
  }
  if (vfs_.Mkdir(kConvDir) != 0) {
    return ESP_FAIL;
  }
  return ESP_OK;
}

esp_err_t ConvDb::Upsert(const ConvInfo &c) {
  if (IdIsAllZero(c.id)) {
    return ESP_ERR_INVALID_ARG;
  }
  ConvInfo local = c;
  // The name field is exactly kConvNameMax bytes wide. The caller
  // is responsible for NUL-terminating the string within the
  // field; an overflowed buffer (no NUL) is rejected outright.
  if (!NameFieldHasNul(local)) {
    return ESP_ERR_INVALID_ARG;
  }

  std::lock_guard<std::mutex> lk(mutex_);
  std::string dir = IdToPath(local.id);
  // If the conv exists, treat as overwrite (no cap check).
  bool exists = vfs_.Exists(dir.c_str());
  if (!exists) {
    if (SizeLocked() >= kConvDbMax) {
      return ESP_ERR_NO_MEM;
    }
    if (vfs_.Mkdir(dir.c_str()) != 0) {
      return ESP_FAIL;
    }
  }
  std::string meta = dir + "/" + kMetaFile;
  if (AtomicWriteFile(vfs_, meta.c_str(), &local, sizeof local) !=
      sizeof local) {
    return ESP_FAIL;
  }
  // Touch the ratchet file with a zero counter if absent.
  std::string ratchet = dir + "/" + kRatchetFile;
  if (!vfs_.Exists(ratchet.c_str())) {
    uint8_t zero[16] = {};
    AtomicWriteFile(vfs_, ratchet.c_str(), zero, sizeof zero);
  }
  return ESP_OK;
}

esp_err_t ConvDb::Remove(const uint8_t id[kConvIdSize]) {
  if (IdIsAllZero(id)) {
    return ESP_ERR_INVALID_ARG;
  }
  std::lock_guard<std::mutex> lk(mutex_);
  std::string dir = IdToPath(id);
  if (!vfs_.Exists(dir.c_str())) {
    return ESP_OK; // idempotent
  }
  // Remove the per-conv files first, then the dir.
  vfs_.Remove((dir + "/" + kMetaFile).c_str());
  vfs_.Remove((dir + "/" + kHistoryFile).c_str());
  vfs_.Remove((dir + "/" + kRatchetFile).c_str());
  vfs_.Rmdir(dir.c_str());
  return ESP_OK;
}

esp_err_t ConvDb::Get(const uint8_t id[kConvIdSize], ConvInfo *out) {
  if (IdIsAllZero(id)) {
    return ESP_ERR_INVALID_ARG;
  }
  std::lock_guard<std::mutex> lk(mutex_);
  std::string dir = IdToPath(id);
  if (!vfs_.Exists(dir.c_str())) {
    return ESP_ERR_NOT_FOUND;
  }
  if (!out) {
    return ESP_OK; // existence check
  }
  std::string meta = dir + "/" + kMetaFile;
  if (!ReadFileAll(vfs_, meta.c_str(), out, sizeof *out)) {
    return ESP_FAIL;
  }
  out->exists = true;
  return ESP_OK;
}

size_t ConvDb::Size() {
  std::lock_guard<std::mutex> lk(mutex_);
  return SizeLocked();
}

size_t ConvDb::SizeLocked() {
  std::vector<std::string> entries = vfs_.ListDir(kConvDir);
  // Only count entries whose name is a 32-char hex string (the
  // conv dirs). Anything else is an unexpected file/dir that we
  // leave alone.
  size_t n = 0;
  for (const auto &e : entries) {
    if (e.size() == 32)
      ++n;
  }
  return n;
}

std::vector<ConvInfo> ConvDb::List() {
  std::lock_guard<std::mutex> lk(mutex_);
  std::vector<ConvInfo> out;
  std::vector<std::string> entries = vfs_.ListDir(kConvDir);
  out.reserve(entries.size());
  for (const auto &e : entries) {
    if (e.size() != 32)
      continue; // skip non-uuid entries
    std::string meta = std::string(kConvDir) + "/" + e + "/" + kMetaFile;
    ConvInfo c;
    if (ReadFileAll(vfs_, meta.c_str(), &c, sizeof c)) {
      c.exists = true;
      out.push_back(c);
    }
  }
  // Sort by last_activity_ms descending.
  std::sort(out.begin(), out.end(), [](const ConvInfo &a, const ConvInfo &b) {
    return a.last_activity_ms > b.last_activity_ms;
  });
  if (out.size() > kConvDbMax) {
    out.resize(kConvDbMax);
  }
  return out;
}

esp_err_t ConvDb::AppendHistory(const uint8_t id[kConvIdSize],
                                const HistoryEntry &e) {
  if (IdIsAllZero(id)) {
    return ESP_ERR_INVALID_ARG;
  }
  std::lock_guard<std::mutex> lk(mutex_);
  std::string dir = IdToPath(id);
  if (!vfs_.Exists(dir.c_str())) {
    return ESP_ERR_NOT_FOUND;
  }
  std::string path = dir + "/" + kHistoryFile;

  // Read the existing header (if any) and ring contents.
  HistoryHeader hdr{};
  std::vector<HistoryEntry> ring(kHistoryRingMax);
  FILE *fp = vfs_.Open(path.c_str(), "rb");
  if (fp) {
    size_t got = fread(&hdr, 1, sizeof hdr, fp);
    if (got == sizeof hdr && hdr.magic == kHistoryMagic) {
      size_t entries = std::min<uint32_t>(hdr.count, kHistoryRingMax);
      size_t want = entries * sizeof(HistoryEntry);
      size_t read = fread(ring.data(), 1, want, fp);
      (void)read;
    } else {
      hdr = HistoryHeader{};
    }
    fclose(fp);
  }

  // Insert at the write index, overwriting if full.
  ring[hdr.write_idx % kHistoryRingMax] = e;
  hdr.write_idx = (hdr.write_idx + 1) % kHistoryRingMax;
  if (hdr.count < kHistoryRingMax) {
    hdr.count++;
  }
  hdr.magic = kHistoryMagic;

  // Write the header + ring atomically.
  size_t ring_bytes = kHistoryRingMax * sizeof(HistoryEntry);
  std::vector<uint8_t> blob(sizeof hdr + ring_bytes);
  std::memcpy(blob.data(), &hdr, sizeof hdr);
  std::memcpy(blob.data() + sizeof hdr, ring.data(), ring_bytes);
  if (AtomicWriteFile(vfs_, path.c_str(), blob.data(), blob.size()) !=
      blob.size()) {
    return ESP_FAIL;
  }
  return ESP_OK;
}

std::vector<HistoryEntry> ConvDb::GetHistory(const uint8_t id[kConvIdSize],
                                             size_t max) {
  if (IdIsAllZero(id)) {
    return {};
  }
  std::lock_guard<std::mutex> lk(mutex_);
  std::vector<HistoryEntry> out;
  std::string dir = IdToPath(id);
  if (!vfs_.Exists(dir.c_str())) {
    return out;
  }
  std::string path = dir + "/" + kHistoryFile;
  FILE *fp = vfs_.Open(path.c_str(), "rb");
  if (!fp) {
    return out;
  }
  HistoryHeader hdr{};
  size_t got = fread(&hdr, 1, sizeof hdr, fp);
  if (got != sizeof hdr || hdr.magic != kHistoryMagic) {
    fclose(fp);
    return out;
  }
  std::vector<HistoryEntry> ring(kHistoryRingMax);
  size_t want = kHistoryRingMax * sizeof(HistoryEntry);
  size_t read = fread(ring.data(), 1, want, fp);
  (void)read;
  fclose(fp);

  // Most-recent first. With count=N and write_idx=W, the newest
  // entry is at (W-1) mod MAX; the oldest valid entry is at
  // (W - N) mod MAX.
  size_t n = std::min<uint32_t>(hdr.count, kHistoryRingMax);
  size_t limit = std::min(n, max);
  out.reserve(limit);
  for (size_t k = 0; k < limit; ++k) {
    size_t idx = (hdr.write_idx + kHistoryRingMax - 1 - k) % kHistoryRingMax;
    out.push_back(ring[idx]);
  }
  return out;
}

esp_err_t ConvDb::ClearHistory(const uint8_t id[kConvIdSize]) {
  if (IdIsAllZero(id)) {
    return ESP_ERR_INVALID_ARG;
  }
  std::lock_guard<std::mutex> lk(mutex_);
  std::string dir = IdToPath(id);
  if (!vfs_.Exists(dir.c_str())) {
    return ESP_ERR_NOT_FOUND;
  }
  std::string path = dir + "/" + kHistoryFile;
  vfs_.Remove(path.c_str());
  return ESP_OK;
}

} // namespace tether::m5
