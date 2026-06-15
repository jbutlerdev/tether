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

// Production stubs. The real LittleFS VFS wrapper
// (firmware/m5/components/littlefs_vfs) provides the actual
// implementation; these stubs satisfy the linker so the
// component compiles in isolation. The production wiring
// (replacing these with the LfsVfs calls) is deferred to
// v1.1 — the on-disk format is what matters for the wire
// contract and the host tests pin that.
bool MkdirsHost(const std::string &path) {
  (void)path;
  return false;
}
bool ExistsHost(const std::string &path) {
  (void)path;
  return false;
}
bool RemoveHost(const std::string &path) {
  (void)path;
  return false;
}
std::vector<std::string> ListDirHost(const std::string &dir) {
  (void)dir;
  return {};
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
  if (!MkdirsHost(sub)) {
    return false;
  }
  root_ = r;
  inited_ = true;
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
  // Production path: write through the LfsVfs wrapper. The
  // on-disk format is the same; only the storage backend
  // differs. The host tests pin the format; this is a
  // straight substitution in v1.1.
  extern LfsVfs &GetLfsVfs();
  return GetLfsVfs().Write(full.c_str(), &rec, CrashRecord::kSizeOnDisk) ==
         CrashRecord::kSizeOnDisk;
#endif
}

bool CrashLog::Delete(const char *name) {
  if (!inited_ || name == nullptr || name[0] == '\0') {
    return false;
  }
  std::string full = JoinPath(root_, name);
  if (!ExistsHost(full)) {
    return false;
  }
  return RemoveHost(full);
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
  if (!ExistsHost(sub)) {
    return out;
  }
  std::vector<std::string> files = ListDirHost(sub);
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
    extern LfsVfs &GetLfsVfs();
    int n = GetLfsVfs().Read(full.c_str(), &rec, CrashRecord::kSizeOnDisk);
    if (n != static_cast<int>(CrashRecord::kSizeOnDisk)) {
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
