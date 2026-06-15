// crash_log.h — Tether M5 crash log to LittleFS (plan §9.3).
//
// On a panic, watchdog, or brownout the M5 captures a small
// fixed-size record to LittleFS at /crash/<timestamp>.bin. On
// the next boot, the conv manager (plan §5.5) reads the
// directory, uploads each file as a kLog frame to the base
// station, and deletes the file. The base station parses the
// blob and emits a structured slog entry that the operator TUI
// (plan §10.1) surfaces.
//
// The record is a small POD struct (CrashRecord) plus a 4-byte
// little-endian magic prefix. The on-disk format is stable
// across firmware versions; a future change to add a field
// bumps the magic and the read path handles both old and new.

#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include "littlefs_vfs.h"

namespace tether::m5 {

// CrashRecord is the on-disk record. Keep the layout
// little-endian and aligned so we can memcmp / memcpy
// directly.
//
// NOTE: this is the wire-level format. The first 4 bytes are
// the magic; the rest of the struct is read verbatim. Do not
// reorder fields without bumping kMagic.
struct CrashRecord {
  // 0x4D4F4F42 ("BOOM" in ASCII, little-endian). The
  // conv_manager check that uploaded this file uses
  // CrashRecord::kMagic; if it doesn't match, the file is
  // skipped (or treated as a foreign record).
  static constexpr uint32_t kMagic = 0x4D4F4F42u;

  // kSizeOnDisk is the byte length of the serialised record.
  // 4 (magic) + 4 (reason) + 4 (boot_count) + 8 (timestamp)
  // + 32 (task_name) + 64 (note) = 116 bytes. The struct is
  // kept POD so we can write it with a single fwrite.
  static constexpr std::size_t kSizeOnDisk = 116;

  uint32_t magic = kMagic;
  uint32_t reason = 0; // ResetReason value
  uint32_t boot_count = 0;
  uint64_t timestamp_unix_ms = 0;
  char task_name[32] = {0};
  char note[64] = {0};
};

class CrashLog {
public:
  CrashLog() = default;

  // Init prepares the root directory + /crash/ subdirectory.
  // Idempotent: calling Init twice in a row is fine. Returns
  // false if the directory cannot be created.
  bool Init(const char *root);

  // Write serialises `rec` to /crash/<name>.bin. Returns false
  // if Init() was not called or the file cannot be written.
  bool Write(const char *name, const CrashRecord &rec);

  // Delete removes the file /crash/<name>.bin. Returns true
  // if the file existed and was removed; false otherwise
  // (including if the file didn't exist; this matches the
  // conv store's Remove contract).
  bool Delete(const char *name);

  // ListAll reads every file in /crash/ and returns the
  // valid CrashRecords. Files that don't start with the magic
  // are silently skipped (the operator can scrub the
  // directory by hand; we don't want to crash on a foreign
  // file).
  std::vector<CrashRecord> ListAll();

  // Static helper: human-readable label for a ResetReason
  // value. Used by the boot log + the operator TUI.
  static const char *ReasonString(uint32_t reason);

private:
  std::string root_;
  bool inited_ = false;
  // vfs_ is the typed LittleFS wrapper the production path
  // routes through. On host it is unused (the unit tests use
  // std::filesystem via the kTestRoot scratch dir).
  LfsVfs vfs_;
};

} // namespace tether::m5
