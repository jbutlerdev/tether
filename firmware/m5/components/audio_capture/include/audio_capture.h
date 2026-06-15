// audio_capture.h — Tether M5 audio capture task (plan.md §4.8.2).
//
// Drains the I2S DMA, encodes 20 ms frames with Opus, and writes
// them into the PSRAM ring. On real hardware the task runs at
// priority 23 on core 0; on host tests we expose RunOnce() for direct
// invocation.

#pragma once

#include <cstddef>
#include <cstdint>

#include "opus_enc.h"
#include "psram_ring.h"

namespace tether::m5 {

class AudioCapture {
public:
  AudioCapture(PsramRing &ring, OpusEncoder &enc);

  // Initialize the I2S mic and Opus encoder. Returns true on success.
  bool Init();

  // Pump one frame through the encoder into the ring. Returns the
  // number of bytes written (0 if the ring was full and the frame
  // was dropped with counter increment).
  size_t RunOnce();

  // Total frames encoded.
  uint64_t FramesEncoded() const { return frames_encoded_; }
  // Total frames dropped (ring full).
  uint64_t FramesDropped() const { return frames_dropped_; }
  // Total malloc calls during RunOnce() (used by the no-alloc test).
  uint64_t AllocationsDuringRun() const { return allocs_during_run_; }

  // Test seam: set the number of malloc() calls each RunOnce() should
  // simulate. Real hardware is expected to be 0; host tests set >0 to
  // assert behavior under allocation pressure.
  void SetMockAllocationsPerRunForTest(size_t n) { mock_allocs_per_run_ = n; }

  // Test seam: feed synthetic PCM directly into the encoder.
  void SetInputPcmForTest(const int16_t *pcm, size_t n);

  // Test seam: signal that I2S is not running (idle low-power state).
  void SetI2SRunningForTest(bool running) { i2s_running_ = running; }

private:
  PsramRing &ring_;
  OpusEncoder &enc_;
  int16_t pcm_buf_[160] = {};
  size_t pcm_n_ = 0;
  bool i2s_running_ = false;
  uint64_t frames_encoded_ = 0;
  uint64_t frames_dropped_ = 0;
  uint64_t allocs_during_run_ = 0;
  size_t mock_allocs_per_run_ = 0;
};

} // namespace tether::m5
