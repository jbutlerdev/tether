// littlefs_vfs.h — Tether M5 typed LittleFS wrapper (plan.md §5.1).
//
// Phase 3 added sd_card (the raw POSIX VFS mount). Phase 4 adds a
// thin typed wrapper on top — the `LfsVfs` class — that the higher
// layers (conv_db, epd settings, ...) build on. The wrapper owns the
// mount point, exposes a small typed file API (Open / Exists /
// Remove / Rename / Mkdir / Rmdir / ListDir / TotalBytes / FreeBytes),
// and stays platform-portable: on real hardware it routes through
// SdCard (the VFS root is /sdcard) and on host it uses a per-test
// tmpfs root.
//
// All methods are thread-safe with an internal mutex so multiple
// tasks (audio_capture, storage_flush, conv_manager, ...) can use the
// same LfsVfs instance.

#pragma once

#include <cstddef>
#include <cstdint>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include <mutex>
#include <string>
#include <vector>

namespace tether::m5 {

inline constexpr size_t kLfsMaxPath = 128;
inline constexpr char kLfsDefaultRoot[] = "/lfs";

class LfsVfs {
public:
  LfsVfs() = default;
  ~LfsVfs();

  LfsVfs(const LfsVfs &) = delete;
  LfsVfs &operator=(const LfsVfs &) = delete;

  // Mount the filesystem at `root`. On host `root` is a tmpfs path;
  // on real hardware it is the VFS path the SD card is registered
  // at (default /sdcard). Idempotent: a second Mount() with the
  // same root returns ESP_OK. Mounting with a different root
  // returns ESP_ERR_INVALID_STATE.
  esp_err_t Mount(const char *root = kLfsDefaultRoot);

  // Unmount. Idempotent.
  esp_err_t Unmount();

  // True if currently mounted.
  bool IsMounted() const { return mounted_; }

  // The mount root. nullptr if not mounted.
  const char *Root() const { return root_.c_str(); }

  // POSIX-style file open. Returns nullptr on error.
  FILE *Open(const char *path, const char *mode);

  // True if `path` exists. Returns false for empty paths or when
  // the FS is unmounted.
  bool Exists(const char *path);

  // Remove a file. Returns 0 on success, -1 on error (including
  // "does not exist" — we treat remove-of-missing as a no-op so
  // callers can be idempotent, matching POSIX unlink semantics).
  int Remove(const char *path);

  // Rename a file. Returns 0 on success, -1 on error.
  int Rename(const char *from, const char *to);

  // Create a directory. Returns 0 on success or if it already
  // exists; -1 on error.
  int Mkdir(const char *path);

  // Remove a directory. Returns 0 on success or if it does not
  // exist; -1 on error (e.g. directory not empty).
  int Rmdir(const char *path);

  // List the entries in `path` (a directory). Names are returned
  // sorted alphabetically. The returned vector is empty on
  // error or if the directory is empty.
  std::vector<std::string> ListDir(const char *path);

  // Total bytes in the mounted FS. 0 if not mounted or statfs
  // is unavailable.
  size_t TotalBytes();

  // Free bytes in the mounted FS. 0 if not mounted or statfs
  // is unavailable.
  size_t FreeBytes();

  // Test seam: take/release the internal mutex from outside.
  void Lock() { mutex_.lock(); }
  void Unlock() { mutex_.unlock(); }

private:
  // Build the full path on disk by concatenating root_ + path.
  // `path` should be relative to the mount root.
  std::string FullPath(const char *path) const;

  bool mounted_ = false;
  std::string root_;
  std::mutex mutex_;
};

} // namespace tether::m5
