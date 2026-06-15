// i2s_amp.h — Tether M5 I2S amplifier with tone generator (plan.md
// §4.6).
//
// On real hardware the amp drives a MAX98357A class-D amplifier over
// I2S TX with 16-bit samples at 8 kHz. The same public API works on
// host: PlayTone() writes a synthetic sine wave into an internal
// buffer, and ReadSamples() lets tests verify the waveform.
//
// PlayTone(freq_hz, duration_ms) queues a tone. Stop() zeroes the
// output buffer. ReadSamples() drains samples in 16-bit PCM.

#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace tether::m5 {

class I2SAmp {
 public:
  I2SAmp() = default;
  ~I2SAmp() = default;

  I2SAmp(const I2SAmp &) = delete;
  I2SAmp &operator=(const I2SAmp &) = delete;

  // Queue a sine tone at `freq_hz` for `duration_ms` milliseconds.
  // Calling PlayTone() while a tone is already playing replaces the
  // queued tone. The tone is written into the internal buffer on
  // ReadSamples().
  void PlayTone(int freq_hz, int duration_ms);

  // Stop any queued tone and zero the output buffer.
  void Stop();

  // Drain up to `max_samples` int16 samples into `out`. Returns the
  // number of samples written. After the queued tone is exhausted,
  // the buffer is filled with zeros.
  size_t ReadSamples(int16_t *out, size_t max_samples);

  // Total samples written across all ReadSamples() calls since the
  // last reset. Useful for FFT-windowed tests.
  size_t TotalSamplesPlayed() const { return total_samples_; }

  // Test seam: reset all internal state.
  void ResetForTest();

 private:
  // Current queued tone. freq_hz == 0 means "no tone".
  int freq_hz_ = 0;
  int duration_ms_ = 0;
  size_t tone_samples_remaining_ = 0; // samples left to emit
  // Sample rate the host build synthesizes at.
  static constexpr int kSampleRate = 8000;
  // Phase accumulator: 0..kSampleRate-1 (0 = start of cycle).
  uint32_t phase_ = 0;
  size_t total_samples_ = 0;
};

}  // namespace tether::m5
