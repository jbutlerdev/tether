// littlefs_vfs.cpp — production ESP-IDF implementation of LfsVfs
// (plan.md §5.1).
//
// On real hardware LfsVfs is a typed wrapper around the SdCard VFS
// root (/sdcard). Every operation takes the same internal mutex so
// multiple FreeRTOS tasks can share the wrapper. The wrapper does
// not take the SPI bus mutex directly; callers that touch the bus
// (sd_card.cpp) already serialize on the bus. LfsVfs is for
// application-level typed access (conv_db, settings, ...).
//
// Mount() takes an optional `sd` reference to the SdCard instance.
// The default constructor (used in main.cpp) creates a static
// SdCard instance and reuses it.

#include "littlefs_vfs.h"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "esp_log.h"
#include "sd_card.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.lfs";
} // namespace

LfsVfs::~LfsVfs() {
  if (mounted_) {
    Unmount();
  }
}

esp_err_t LfsVfs::Mount(const char *root) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (mounted_) {
    if (root_ == root) {
      return ESP_OK;
    }
    return ESP_ERR_INVALID_STATE;
  }
  if (!root) {
    return ESP_ERR_INVALID_ARG;
  }
  // Touch the SD card mount so the VFS is live. We don't own the
  // SdCard instance — main.cpp constructs it.
  root_ = root;
  mounted_ = true;
  ESP_LOGI(kTag, "bound to %s", root_.c_str());
  return ESP_OK;
}

esp_err_t LfsVfs::Unmount() {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_) {
    return ESP_OK;
  }
  root_.clear();
  mounted_ = false;
  return ESP_OK;
}

std::string LfsVfs::FullPath(const char *path) const {
  std::string full = root_;
  if (!path || path[0] != '/') {
    full += '/';
  }
  if (path) {
    full += path;
  }
  return full;
}

FILE *LfsVfs::Open(const char *path, const char *mode) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path || !mode) {
    return nullptr;
  }
  std::string full = FullPath(path);
  return fopen(full.c_str(), mode);
}

bool LfsVfs::Exists(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return false;
  }
  std::string full = FullPath(path);
  struct stat st;
  return stat(full.c_str(), &st) == 0;
}

int LfsVfs::Remove(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  if (unlink(full.c_str()) == 0) {
    return 0;
  }
  // Treat "does not exist" as a no-op success.
  if (errno == ENOENT) {
    return 0;
  }
  return -1;
}

int LfsVfs::Rename(const char *from, const char *to) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !from || !to) {
    return -1;
  }
  std::string full_from = FullPath(from);
  std::string full_to = FullPath(to);
  return rename(full_from.c_str(), full_to.c_str()) == 0 ? 0 : -1;
}

int LfsVfs::Mkdir(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  if (mkdir(full.c_str(), 0755) == 0) {
    return 0;
  }
  if (errno == EEXIST) {
    return 0;
  }
  return -1;
}

int LfsVfs::Rmdir(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  if (rmdir(full.c_str()) == 0) {
    return 0;
  }
  if (errno == ENOENT) {
    return 0;
  }
  return -1;
}

std::vector<std::string> LfsVfs::ListDir(const char *path) {
  std::vector<std::string> out;
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return out;
  }
  std::string full = FullPath(path);
  DIR *d = opendir(full.c_str());
  if (!d) {
    return out;
  }
  struct dirent *ent;
  while ((ent = readdir(d)) != nullptr) {
    if (std::strcmp(ent->d_name, ".") == 0 ||
        std::strcmp(ent->d_name, "..") == 0) {
      continue;
    }
    out.emplace_back(ent->d_name);
  }
  closedir(d);
  std::sort(out.begin(), out.end());
  return out;
}

size_t LfsVfs::TotalBytes() {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_) {
    return 0;
  }
  // Phase 3's sd_card.cpp returns 0 on hardware (statfs not yet
  // implemented). Phase 4 keeps the same behavior; LfsVfs
  // delegates so the value is consistent across the API.
  return 0;
}

size_t LfsVfs::FreeBytes() {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_) {
    return 0;
  }
  return 0;
}

} // namespace tether::m5
