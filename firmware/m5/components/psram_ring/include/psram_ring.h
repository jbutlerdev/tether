// psram_ring.h — single-producer single-consumer (SPSC) ring buffer
// (plan.md §4.4).
//
// The audio capture task writes Opus frames into the ring; the storage
// flush and radio tasks read from it. Per research.md §7.3 the ring is
// lock-free — head and tail are std::atomic<size_t> and a memory
// barrier on the producer side ensures the reader sees fresh bytes.
//
// The capacity must be a power of two. Wrapping the head/tail pointers
// with `(idx & (capacity - 1))` lets the modulo compile to a single
// `and` instruction. The class rejects non-power-of-two capacities in
// its constructor.

#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>

namespace tether::m5 {

class PsramRing {
 public:
  // Build a ring with `capacity` slots. Throws std::invalid_argument if
  // capacity is not a power of two.
  explicit PsramRing(size_t capacity);

  PsramRing(const PsramRing &) = delete;
  PsramRing &operator=(const PsramRing &) = delete;
  PsramRing(PsramRing &&) = delete;
  PsramRing &operator=(PsramRing &&) = delete;

  ~PsramRing();

  // Append up to `len` bytes from `data`. Returns the number of bytes
  // actually written (0 if the ring is full and no bytes fit).
  size_t Write(const uint8_t *data, size_t len);

  // Pop up to `len` bytes into `out`. Returns the number of bytes
  // actually read (0 if the ring is empty).
  size_t Read(uint8_t *out, size_t len);

  // Number of bytes available to read.
  size_t Available() const;

  // Total capacity in bytes.
  size_t Capacity() const { return capacity_; }

  // Test seam: reset the ring to empty (does not free memory).
  void ResetForTest();

 private:
  uint8_t *buf_ = nullptr;
  size_t capacity_ = 0;
  // Head: writer's next position (number of bytes written cumulatively).
  // Tail: reader's next position.
  // Available = head - tail (mod capacity).
  std::atomic<size_t> head_{0};
  std::atomic<size_t> tail_{0};
};

}  // namespace tether::m5
