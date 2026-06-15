// crash_log.cpp — implementation of tether::m5::CrashLog.
//
// The host build uses std::filesystem and std::fopen for the
// sandboxed tests. On real hardware the implementation should
// route through the existing LittleFS VFS wrapper
// (firmware/m5/components/littlefs_vfs) which already exposes
// the /lfs/... mount. We keep the on-disk format identical
// across both build targets so the production firmware and
// the host test runner exercise the same binary layout.

#include "crash_log.h"

#include <cstdio>
#include <cstring>
#include <string>

#ifdef TETHER_M5_HOST_TEST
#include <filesystem>
#include <system_error>
#else
#include "littlefs_vfs.h"
#endif

namespace tether::m5 {

namespace {

// Subdirectory under the root where crash files live.
constexpr char kCrashSubdir[] = "/crash";

std::string JoinPath(const std::string &root, const char *name) {
  // root/crash/name
  std::string out = root;
  if (out.empty() || out.back() != '/') {
    out += '/';
  }
  out += "crash/";
  out += name;
  return out;
}

#ifdef TETHER_M5_HOST_TEST

bool MkdirsHost(const std::string &path) {
  namespace fs = std::filesystem;
  std::error_code ec;
  fs::create_directories(path, ec);
  return !ec;
}

bool ExistsHost(const std::string &path) {
  namespace fs = std::filesystem;
  return fs::exists(path);
}

bool RemoveHost(const std::string &path) {
  namespace fs = std::filesystem;
  std::error_code ec;
  return fs::remove(path, ec);
}

std::vector<std::string> ListDirHost(const std::string &dir) {
  namespace fs = std::filesystem;
  std::vector<std::string> out;
  std::error_code ec;
  for (const auto &entry : fs::directory_iterator(dir, ec)) {
    if (entry.is_regular_file()) {
      out.push_back(entry.path().filename().string());
    }
  }
  return out;
}

#else  // real hardware

// Production path: the helpers route through the LfsVfs
// member of the owning CrashLog instance. We pass the vfs
// reference in to keep the helpers in the anonymous
// namespace (and out of the public API).
bool MkdirsHost(LfsVfs &vfs, const std::string &path) {
  // vfs_.Mkdir is non-recursive; the conv manager's
  // crash subdir is one level deep, so a single Mkdir is
  // enough. If we ever need a deeper tree, switch to a
  // loop that walks the path components.
  return vfs.Mkdir(path.c_str()) == 0;
}

bool ExistsHost(LfsVfs &vfs, const std::string &path) {
  return vfs.Exists(path.c_str());
}

bool RemoveHost(LfsVfs &vfs, const std::string &path) {
  return vfs.Remove(path.c_str()) == 0;
}

std::vector<std::string> ListDirHost(LfsVfs &vfs, const std::string &dir) {
  return vfs.ListDir(dir.c_str());
}

#endif  // TETHER_M5_HOST_TEST

}  // namespace

bool CrashLog::Init(const char *root) {
  if (root == nullptr || root[0] == '\0') {
    return false;
  }
  std::string r(root);
  std::string sub = r;
  if (sub.empty() || sub.back() != '/') {
    sub += '/';
  }
  sub += "crash";
#ifdef TETHER_M5_HOST_TEST
  if (!MkdirsHost(sub)) {
    return false;
  }
  root_ = r;
  inited_ = true;
  (void)vfs_; // unused on host
#else
  // Mount the VFS at the chosen root, then create the
  // crash subdirectory through the same wrapper. We do the
  // mount first so the production MkdirsHost can call
  // vfs_.Mkdir without checking whether the VFS is up.
  if (vfs_.Mount(r.c_str()) != ESP_OK) {
    return false;
  }
  if (!MkdirsHost(vfs_, sub)) {
    return false;
  }
  root_ = r;
  inited_ = true;
#endif
  return true;
}

bool CrashLog::Write(const char *name, const CrashRecord &rec) {
  if (!inited_ || name == nullptr || name[0] == '\0') {
    return false;
  }
  if (rec.magic != CrashRecord::kMagic) {
    return false; // refuse to write a record with a bad magic
  }
  std::string full = JoinPath(root_, name);
#ifdef TETHER_M5_HOST_TEST
  FILE *fp = std::fopen(full.c_str(), "wb");
  if (fp == nullptr) {
    return false;
  }
  size_t wrote =
      std::fwrite(&rec, 1, CrashRecord::kSizeOnDisk, fp);
  std::fclose(fp);
  return wrote == CrashRecord::kSizeOnDisk;
#else
  // Production path: open the file through the LfsVfs
  // wrapper and write the record with std::fwrite. The
  // on-disk format is the same as the host path; the
  // LfsVfs::Open call hands us a FILE* that routes through
  // the SD card's VFS mount (see littlefs_vfs_host.cpp /
  // littlefs_vfs.cpp for the production wiring).
  FILE *fp = vfs_.Open(full.c_str(), "wb");
  if (fp == nullptr) {
    return false;
  }
  size_t wrote = std::fwrite(&rec, 1, CrashRecord::kSizeOnDisk, fp);
  std::fclose(fp);
  return wrote == CrashRecord::kSizeOnDisk;
#endif
}

bool CrashLog::Delete(const char *name) {
  if (!inited_ || name == nullptr || name[0] == '\0') {
    return false;
  }
  std::string full = JoinPath(root_, name);
#ifdef TETHER_M5_HOST_TEST
  if (!ExistsHost(full)) {
    return false;
  }
  return RemoveHost(full);
#else
  if (!ExistsHost(vfs_, full)) {
    return false;
  }
  return RemoveHost(vfs_, full);
#endif
}

std::vector<CrashRecord> CrashLog::ListAll() {
  std::vector<CrashRecord> out;
  if (!inited_) {
    return out;
  }
  std::string sub = root_;
  if (sub.empty() || sub.back() != '/') {
    sub += '/';
  }
  sub += "crash";
#ifdef TETHER_M5_HOST_TEST
  if (!ExistsHost(sub)) {
    return out;
  }
  std::vector<std::string> files = ListDirHost(sub);
#else
  if (!ExistsHost(vfs_, sub)) {
    return out;
  }
  std::vector<std::string> files = ListDirHost(vfs_, sub);
#endif
  for (const auto &name : files) {
    std::string full = sub + "/" + name;
    CrashRecord rec{};
#ifdef TETHER_M5_HOST_TEST
    FILE *fp = std::fopen(full.c_str(), "rb");
    if (fp == nullptr) {
      continue;
    }
    size_t got = std::fread(&rec, 1, CrashRecord::kSizeOnDisk, fp);
    std::fclose(fp);
    if (got != CrashRecord::kSizeOnDisk) {
      continue;
    }
#else
    // Production path: open the file through the LfsVfs
    // wrapper and read the record with std::fread. The
    // on-disk format is the same as the host path; the
    // LfsVfs::Open call hands us a FILE* that routes through
    // the SD card's VFS mount.
    FILE *fp = vfs_.Open(full.c_str(), "rb");
    if (fp == nullptr) {
      continue;
    }
    size_t got = std::fread(&rec, 1, CrashRecord::kSizeOnDisk, fp);
    std::fclose(fp);
    if (got != CrashRecord::kSizeOnDisk) {
      continue;
    }
#endif
    if (rec.magic != CrashRecord::kMagic) {
      continue; // foreign file; skip
    }
    out.push_back(rec);
  }
  return out;
}

const char *CrashLog::ReasonString(uint32_t reason) {
  switch (reason) {
  case 1:
    return "power-on";
  case 2:
    return "soft-restart";
  case 3:
    return "task-wdt";
  case 4:
    return "panic";
  case 5:
    return "brownout";
  default:
    return "unknown";
  }
}

}  // namespace tether::m5
