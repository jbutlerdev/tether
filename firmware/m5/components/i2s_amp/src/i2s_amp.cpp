// i2s_amp.cpp — Tether M5 I2S amplifier with tone generator.
//
// On real hardware this drives a MAX98357A class-D amp over I2S TX at
// 8 kHz, 16-bit, mono. The PlayTone() method synthesizes a sine wave
// into a DMA buffer; ReadSamples() drains it for the host build (so
// tests can verify frequency and silence behavior). On real hardware,
// the DMA-filled buffer is consumed by the I2S peripheral directly.

#include "i2s_amp.h"

#include <algorithm>
#include <cmath>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/i2s.h"
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.amp";
constexpr int kAmpVolume = 8000; // peak amplitude
} // namespace

void I2SAmp::PlayTone(int freq_hz, int duration_ms) {
  freq_hz_ = freq_hz;
  duration_ms_ = duration_ms;
  tone_samples_remaining_ =
      static_cast<size_t>(duration_ms) * kSampleRate / 1000;
  phase_ = 0;
}

void I2SAmp::Stop() {
  freq_hz_ = 0;
  duration_ms_ = 0;
  tone_samples_remaining_ = 0;
  phase_ = 0;
}

size_t I2SAmp::ReadSamples(int16_t *out, size_t max_samples) {
  size_t i = 0;
  while (i < max_samples) {
    if (freq_hz_ == 0 || tone_samples_remaining_ == 0) {
      out[i++] = 0;
      continue;
    }
    // Synthesize one sample: y = A * sin(2π * f * n / Fs)
    double t = static_cast<double>(phase_) / static_cast<double>(kSampleRate);
    double sample = kAmpVolume * std::sin(2.0 * M_PI * freq_hz_ * t);
    out[i++] = static_cast<int16_t>(sample);
    phase_ = (phase_ + 1) % static_cast<uint32_t>(kSampleRate);
    --tone_samples_remaining_;
  }
  total_samples_ += i;
  return i;
}

void I2SAmp::ResetForTest() {
  Stop();
  total_samples_ = 0;
}

} // namespace tether::m5
