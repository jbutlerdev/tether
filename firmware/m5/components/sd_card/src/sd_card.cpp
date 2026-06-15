// sd_card.cpp — production ESP-IDF implementation of SdCard (plan.md §4.3).
//
// On real hardware this wraps esp_littlefs and registers a VFS that
// makes /sdcard point at the SD card SPI device. The SD card shares
// the SPI bus with the SX1262 and EPD, so every LittleFS operation
// takes the bus mutex via Bus().Lock() / Bus().Unlock() (research.md
// §7.4).
//
// The class is intentionally minimal in Phase 3 — it exposes the POSIX
// file API. Phase 4 will introduce a typed file API on top (plan §5.1).

#include "sd_card.h"

#include <cstring>
#include <fcntl.h>
#include <sys/stat.h>
#ifdef TETHER_M5_HOST_TEST
#include <sys/statvfs.h>
#endif

#include "esp_littlefs.h"
#include "esp_log.h"
#include "esp_vfs.h"
#include "esp_vfs_littlefs.h"

#include "spi_bus.h"

namespace tether::m5 {

namespace {
constexpr char kVfsRoot[] = "/sdcard";
constexpr char kPartitionLabel[] = "sd";
} // namespace

SdCard::~SdCard() {
  if (mounted_) {
    Unmount();
  }
}

esp_err_t SdCard::Mount(const char * /*root*/) {
  if (mounted_)
    return ESP_OK;

  esp_vfs_littlefs_conf_t conf = {};
  conf.base_path = kVfsRoot;
  conf.partition_label = kPartitionLabel;
  conf.format_if_mount_failed = true;
  conf.dont_mount = false;
  esp_err_t err = esp_vfs_littlefs_register(&conf);
  if (err != ESP_OK) {
    ESP_LOGE(kSdCardTag, "esp_vfs_littlefs_register: %s", esp_err_to_name(err));
    return err;
  }
  std::strncpy(mount_root_, kVfsRoot, sizeof(mount_root_) - 1);
  mount_root_[sizeof(mount_root_) - 1] = '\0';
  mounted_ = true;
  ESP_LOGI(kSdCardTag, "mounted at %s", kVfsRoot);
  return ESP_OK;
}

esp_err_t SdCard::Unmount() {
  if (!mounted_)
    return ESP_OK;
  Bus().Lock(portMAX_DELAY);
  esp_err_t err = esp_vfs_littlefs_unregister(kPartitionLabel);
  Bus().Unlock();
  mount_root_[0] = '\0';
  mounted_ = false;
  return err;
}

FILE *SdCard::Open(const char *path, const char *mode) {
  if (!mounted_ || !path || !mode)
    return nullptr;
  Bus().Lock(portMAX_DELAY);
  FILE *fp = fopen(path, mode);
  Bus().Unlock();
  return fp;
}

int SdCard::Remove(const char *path) {
  if (!mounted_ || !path)
    return -1;
  Bus().Lock(portMAX_DELAY);
  int rc = unlink(path);
  Bus().Unlock();
  return rc;
}

int SdCard::Rename(const char *from, const char *to) {
  if (!mounted_ || !from || !to)
    return -1;
  Bus().Lock(portMAX_DELAY);
  int rc = rename(from, to);
  Bus().Unlock();
  return rc;
}

size_t SdCard::TotalBytes() const {
  if (!mounted_)
    return 0;
#ifdef TETHER_M5_HOST_TEST
  struct statvfs st;
  if (statvfs(mount_root_, &st) != 0)
    return 0;
  return static_cast<size_t>(st.f_blocks) * st.f_frsize;
#else
  return 0; // TODO(phase-4): use esp_littlefs_info()
#endif
}

size_t SdCard::FreeBytes() const {
  if (!mounted_)
    return 0;
#ifdef TETHER_M5_HOST_TEST
  struct statvfs st;
  if (statvfs(mount_root_, &st) != 0)
    return 0;
  return static_cast<size_t>(st.f_bavail) * st.f_frsize;
#else
  return 0; // TODO(phase-4): use esp_littlefs_info()
#endif
}

} // namespace tether::m5
