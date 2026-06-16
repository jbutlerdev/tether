// sd_card_host.cpp — host-side implementation of SdCard (plan.md §4.3).
//
// On host we back the SdCard class with the local filesystem, treating
// the mount root as a directory in the user's tmp. The same public API
// (Open, Remove, Rename, FreeBytes, etc.) maps to libc / syscalls. This
// lets every test that exercises the SD card API run on Linux without
// the SD card hardware or LittleFS being present.

#ifdef TETHER_M5_HOST_TEST

#include "sd_card.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <string>
#include <sys/statvfs.h>

namespace fs = std::filesystem;

namespace tether::m5 {

SdCard::~SdCard() {
  if (mounted_) {
    Unmount();
  }
}

esp_err_t SdCard::Mount(const char *root) {
  if (mounted_) {
    if (std::strcmp(mount_root_, root) == 0) {
      return ESP_OK; // idempotent
    }
    return ESP_ERR_INVALID_STATE;
  }
  if (!root)
    return ESP_ERR_INVALID_ARG;
  std::error_code ec;
  fs::create_directories(root, ec);
  if (ec) {
    return ESP_FAIL;
  }
  std::strncpy(mount_root_, root, sizeof(mount_root_) - 1);
  mount_root_[sizeof(mount_root_) - 1] = '\0';
  mounted_ = true;
  return ESP_OK;
}

esp_err_t SdCard::Unmount() {
  if (!mounted_)
    return ESP_OK;
  mount_root_[0] = '\0';
  mounted_ = false;
  return ESP_OK;
}

FILE *SdCard::Open(const char *path, const char *mode) {
  if (!mounted_ || !path || !mode)
    return nullptr;
  // Concatenate mount_root + path. We accept paths starting with '/'.
  std::string full = mount_root_;
  if (path[0] != '/')
    full += '/';
  full += path;
  return std::fopen(full.c_str(), mode);
}

int SdCard::Remove(const char *path) {
  if (!mounted_ || !path)
    return -1;
  std::string full = mount_root_;
  if (path[0] != '/')
    full += '/';
  full += path;
  std::error_code ec;
  return fs::remove(full, ec) ? 0 : -1;
}

int SdCard::Rename(const char *from, const char *to) {
  if (!mounted_ || !from || !to)
    return -1;
  std::string full_from = mount_root_;
  if (from[0] != '/')
    full_from += '/';
  full_from += from;
  std::string full_to = mount_root_;
  if (to[0] != '/')
    full_to += '/';
  full_to += to;
  std::error_code ec;
  fs::rename(full_from, full_to, ec);
  return ec ? -1 : 0;
}

size_t SdCard::TotalBytes() const {
  if (!mounted_)
    return 0;
  struct statvfs st;
  if (statvfs(mount_root_, &st) != 0)
    return 0;
  return static_cast<size_t>(st.f_blocks) * st.f_frsize;
}

size_t SdCard::FreeBytes() const {
  if (!mounted_)
    return 0;
  struct statvfs st;
  if (statvfs(mount_root_, &st) != 0)
    return 0;
  return static_cast<size_t>(st.f_bavail) * st.f_frsize;
}

} // namespace tether::m5

#endif // TETHER_M5_HOST_TEST
