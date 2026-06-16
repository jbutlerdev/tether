// littlefs_vfs_host.cpp — host-side implementation of LfsVfs (plan.md §5.1).
//
// This file is built when TETHER_M5_HOST_TEST is defined. It provides
// the LfsVfs API against the local filesystem (a per-test tmpfs
// directory). The real firmware uses src/littlefs_vfs.cpp.

#ifdef TETHER_M5_HOST_TEST

#include "littlefs_vfs.h"

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <string>
#include <sys/stat.h>
#include <sys/statvfs.h>

namespace fs = std::filesystem;

namespace tether::m5 {

LfsVfs::~LfsVfs() {
  if (mounted_) {
    Unmount();
  }
}

esp_err_t LfsVfs::Mount(const char *root) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (mounted_) {
    if (root_ == root) {
      return ESP_OK; // idempotent
    }
    return ESP_ERR_INVALID_STATE;
  }
  if (!root) {
    return ESP_ERR_INVALID_ARG;
  }
  std::error_code ec;
  fs::create_directories(root, ec);
  if (ec) {
    return ESP_FAIL;
  }
  root_ = root;
  mounted_ = true;
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
  // Make sure the parent directory exists when writing.
  if (mode[0] == 'w' || mode[0] == 'a') {
    fs::path p(full);
    if (p.has_parent_path()) {
      std::error_code ec;
      fs::create_directories(p.parent_path(), ec);
    }
  }
  return std::fopen(full.c_str(), mode);
}

bool LfsVfs::Exists(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return false;
  }
  std::string full = FullPath(path);
  std::error_code ec;
  return fs::exists(full, ec);
}

int LfsVfs::Remove(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  std::error_code ec;
  // Treat "does not exist" as a no-op (POSIX unlink semantics with
  // the missing flag set) — returning success.
  if (!fs::exists(full, ec)) {
    return 0;
  }
  return fs::remove(full, ec) ? 0 : -1;
}

int LfsVfs::Rename(const char *from, const char *to) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !from || !to) {
    return -1;
  }
  std::string full_from = FullPath(from);
  std::string full_to = FullPath(to);
  std::error_code ec;
  if (!fs::exists(full_from, ec)) {
    return -1;
  }
  // Ensure parent of `to` exists so atomic rename works for nested
  // targets.
  {
    fs::path p(full_to);
    if (p.has_parent_path()) {
      fs::create_directories(p.parent_path(), ec);
    }
  }
  fs::rename(full_from, full_to, ec);
  return ec ? -1 : 0;
}

int LfsVfs::Mkdir(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  std::error_code ec;
  if (fs::exists(full, ec) && fs::is_directory(full, ec)) {
    return 0; // already a directory
  }
  return fs::create_directories(full, ec) ? 0 : -1;
}

int LfsVfs::Rmdir(const char *path) {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return -1;
  }
  std::string full = FullPath(path);
  std::error_code ec;
  if (!fs::exists(full, ec)) {
    return 0; // missing: no-op
  }
  if (!fs::is_directory(full, ec)) {
    return -1;
  }
  return fs::remove(full, ec) ? 0 : -1;
}

std::vector<std::string> LfsVfs::ListDir(const char *path) {
  std::vector<std::string> out;
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_ || !path) {
    return out;
  }
  std::string full = FullPath(path);
  std::error_code ec;
  if (!fs::is_directory(full, ec)) {
    return out;
  }
  for (const auto &entry : fs::directory_iterator(full, ec)) {
    if (ec)
      break;
    out.emplace_back(entry.path().filename().string());
  }
  std::sort(out.begin(), out.end());
  return out;
}

size_t LfsVfs::TotalBytes() {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_) {
    return 0;
  }
  struct statvfs st;
  if (statvfs(root_.c_str(), &st) != 0) {
    return 0;
  }
  return static_cast<size_t>(st.f_blocks) * st.f_frsize;
}

size_t LfsVfs::FreeBytes() {
  std::lock_guard<std::mutex> lk(mutex_);
  if (!mounted_) {
    return 0;
  }
  struct statvfs st;
  if (statvfs(root_.c_str(), &st) != 0) {
    return 0;
  }
  return static_cast<size_t>(st.f_bavail) * st.f_frsize;
}

} // namespace tether::m5

#endif // TETHER_M5_HOST_TEST
