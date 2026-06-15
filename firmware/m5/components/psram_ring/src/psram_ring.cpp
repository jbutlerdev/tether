// psram_ring.cpp — implementation of tether::m5::PsramRing.
//
// Single-producer / single-consumer ring with std::atomic<size_t> head
// and tail. The capacity is a power of two so wrapping is a bitwise
// AND. Memory ordering uses sequentially-consistent for the head (the
// producer publishes bytes to the reader) and acquire on tail reads;
// the exact ordering is overkill for correctness but matches the
// spec's no-mutex requirement (research.md §7.3).

#include "psram_ring.h"

#include <cstring>
#include <stdexcept>

#ifdef TETHER_M5_HOST_TEST
// No ESP-IDF headers on host.
#else
#include "esp_heap_caps.h"
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.ring";

bool IsPowerOfTwo(size_t n) { return n != 0 && (n & (n - 1)) == 0; }
} // namespace

PsramRing::PsramRing(size_t capacity) : capacity_(capacity) {
  if (!IsPowerOfTwo(capacity_)) {
    throw std::invalid_argument("PsramRing: capacity must be a power of 2");
  }
  // On ESP32-S3 with PSRAM, prefer MALLOC_CAP_SPIRAM. On host we just
  // use plain new[].
#ifdef TETHER_M5_HOST_TEST
  buf_ = new uint8_t[capacity_];
#else
  buf_ = static_cast<uint8_t *>(
      heap_caps_malloc(capacity_, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT));
  if (!buf_) {
    // Fall back to internal RAM if PSRAM isn't configured (e.g. testing
    // on an ESP32-S3 devkit without a PSRAM chip).
    buf_ = static_cast<uint8_t *>(heap_caps_malloc(capacity_, MALLOC_CAP_8BIT));
    ESP_LOGW(kTag, "PSRAM unavailable; using internal RAM");
  }
#endif
  if (!buf_) {
    throw std::bad_alloc();
  }
  head_.store(0, std::memory_order_relaxed);
  tail_.store(0, std::memory_order_relaxed);
#ifdef TETHER_M5_HOST_TEST
  (void)kTag;
#endif
}

PsramRing::~PsramRing() {
  if (buf_) {
#ifdef TETHER_M5_HOST_TEST
    delete[] buf_;
#else
    heap_caps_free(buf_);
#endif
    buf_ = nullptr;
  }
}

size_t PsramRing::Write(const uint8_t *data, size_t len) {
  if (!buf_ || !data || len == 0)
    return 0;
  size_t head = head_.load(std::memory_order_relaxed);
  size_t tail = tail_.load(std::memory_order_acquire);
  size_t available = capacity_ - (head - tail);
  if (available == 0)
    return 0;
  size_t to_write = (len < available) ? len : available;
  // Wrap-around copy: write in up to two segments.
  size_t head_idx = head & (capacity_ - 1);
  size_t first = capacity_ - head_idx;
  if (first > to_write)
    first = to_write;
  std::memcpy(buf_ + head_idx, data, first);
  size_t second = to_write - first;
  if (second > 0) {
    std::memcpy(buf_ + 0, data + first, second);
  }
  // Publish bytes with release ordering so the reader sees the writes.
  head_.store(head + to_write, std::memory_order_release);
  return to_write;
}

size_t PsramRing::Read(uint8_t *out, size_t len) {
  if (!buf_ || !out || len == 0)
    return 0;
  size_t tail = tail_.load(std::memory_order_relaxed);
  size_t head = head_.load(std::memory_order_acquire);
  size_t available = head - tail;
  if (available == 0)
    return 0;
  size_t to_read = (len < available) ? len : available;
  size_t tail_idx = tail & (capacity_ - 1);
  size_t first = capacity_ - tail_idx;
  if (first > to_read)
    first = to_read;
  std::memcpy(out, buf_ + tail_idx, first);
  size_t second = to_read - first;
  if (second > 0) {
    std::memcpy(out + first, buf_ + 0, second);
  }
  tail_.store(tail + to_read, std::memory_order_release);
  return to_read;
}

size_t PsramRing::Available() const {
  size_t head = head_.load(std::memory_order_acquire);
  size_t tail = tail_.load(std::memory_order_acquire);
  return head - tail;
}

void PsramRing::ResetForTest() {
  head_.store(0, std::memory_order_relaxed);
  tail_.store(0, std::memory_order_relaxed);
}

} // namespace tether::m5
