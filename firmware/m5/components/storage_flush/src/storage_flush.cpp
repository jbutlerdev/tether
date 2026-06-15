// storage_flush.cpp — implementation of tether::m5::StorageFlush.

#include "storage_flush.h"

#include <chrono>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <string>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.flush";
constexpr size_t kFlushChunk = 256;
} // namespace

StorageFlush::StorageFlush(PsramRing &ring, SdCard &card)
    : ring_(ring), card_(card) {}

bool StorageFlush::Init() {
  if (!card_.IsMounted()) {
    ESP_LOGE(kTag, "SD card not mounted");
    return false;
  }
  return true;
}

size_t StorageFlush::RunOnce() {
  if (!card_.IsMounted())
    return 0;
  uint8_t buf[kFlushChunk];
  size_t got = ring_.Read(buf, sizeof(buf));
  if (got == 0)
    return 0;
  // Pick a file name lazily. Real firmware uses a timestamped
  // directory like /sdcard/2026-01-01/000123.opus; the host build
  // uses a simpler test-friendly path.
  if (last_file_.empty()) {
    last_file_ = "/tether_capture_001.bin";
  }
  FILE *fp = card_.Open(last_file_.c_str(), "ab");
  if (!fp) {
    ESP_LOGE(kTag, "open %s failed", last_file_.c_str());
    return 0;
  }
  size_t wrote = std::fwrite(buf, 1, got, fp);
  std::fclose(fp);
  if (wrote != got) {
    ESP_LOGE(kTag, "short write: %zu / %zu", wrote, got);
    return wrote;
  }
  total_bytes_ += wrote;
  chunks_written_++;
  return wrote;
}

void StorageFlush::RotateFileForTest() {
  // Bump the file name to a new suffix so subsequent writes go to a
  // fresh file. Real firmware rotates at fixed time boundaries.
  if (last_file_.empty()) {
    last_file_ = "/tether_capture_001.bin";
    return;
  }
  // Find the trailing digits and increment.
  size_t pos = last_file_.find_last_of("0123456789");
  if (pos == std::string::npos) {
    last_file_ += ".1";
    return;
  }
  size_t start = pos;
  while (start > 0 &&
         std::isdigit(static_cast<unsigned char>(last_file_[start - 1]))) {
    --start;
  }
  int n = std::atoi(last_file_.c_str() + start);
  char tail[32];
  std::snprintf(tail, sizeof tail, "%0*d",
                static_cast<int>(last_file_.size() - start), n + 1);
  last_file_ = last_file_.substr(0, start) + tail;
}

} // namespace tether::m5
