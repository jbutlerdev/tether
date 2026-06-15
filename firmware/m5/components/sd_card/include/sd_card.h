// sd_card.h — Tether M5 SD card LittleFS mount (plan.md §4.3).
//
// On real hardware the SdCard class wraps esp_littlefs and presents a
// POSIX file API (fopen / fread / fwrite / fclose). On host, the same
// API is backed by a tmpfs rooted at a directory passed to Mount().
//
// The class is intentionally minimal: it does not try to be a VFS shim.
// Higher-level code that wants typed file operations should go through
// littlefs_vfs (plan.md §5.1) once we get to Phase 4.

#pragma once

#include <cstddef>
#include <cstdint>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

namespace tether::m5 {

// Per-component log tag, also reused by every test.
inline constexpr char kSdCardTag[] = "tether.sd";

class SdCard {
 public:
  SdCard() = default;
  ~SdCard();

  SdCard(const SdCard &) = delete;
  SdCard &operator=(const SdCard &) = delete;

  // Mount the filesystem. Idempotent: a second Mount() with the same
  // root is a no-op that returns ESP_OK.
  //
  // On host: `root` is a path on the local filesystem. The class
  // creates the directory if it does not exist and binds it as the
  // "LittleFS" root.
  // On real hardware: `root` is ignored; the class uses the SD card
  // SPI device registered with Bus() at /sdcard.
  esp_err_t Mount(const char *root = "/tmp/tether_sd");

  // Unmount the filesystem. Idempotent. Returns ESP_OK if not mounted.
  esp_err_t Unmount();

  // Open a file in POSIX mode ("r", "w", "a", "rb", etc.). Returns
  // nullptr on error (file not found, out of memory, etc.).
  FILE *Open(const char *path, const char *mode);

  // Remove a file. Returns 0 on success, -1 on error.
  int Remove(const char *path);

  // Rename a file. Returns 0 on success, -1 on error.
  int Rename(const char *from, const char *to);

  // Total bytes in the mounted filesystem. Returns 0 if not mounted or
  // the platform does not support statfs.
  size_t TotalBytes() const;

  // Free bytes in the mounted filesystem.
  size_t FreeBytes() const;

  // True if Mount() has been called and not Unmount()'d.
  bool IsMounted() const { return mounted_; }

  // The path used for Mount(). nullptr if not mounted.
  const char *MountRoot() const { return mount_root_; }

 private:
  bool mounted_ = false;
  char mount_root_[256] = {};
};

}  // namespace tether::m5
