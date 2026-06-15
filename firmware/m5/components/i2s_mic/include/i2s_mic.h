// i2s_mic.h — Tether M5 I2S microphone (INMP441) interface (plan.md §4.6).
//
// On real hardware the mic is an I2S RX master, 8 kHz, 16-bit, mono,
// with 4 DMA buffers of 256 samples each. The audio_capture task
// (plan §4.8.2) consumes samples from the DMA and pushes them into
// the Opus encoder. On host this class is a thin stub; the
// audio_capture task uses a mock mic for tests.
#pragma once

#include <cstddef>
#include <cstdint>

namespace tether::m5 {

class I2SMic {
 public:
  I2SMic() = default;
  ~I2SMic() = default;

  // Initialize the I2S RX master. Pin assignments match hardware.md
  // (INMP441: SCK, WS, SD on dedicated I2S pins).
  bool Init();

  // Read up to `max_samples` int16 samples into `out`. Returns the
  // number of samples actually read. On host this returns 0 (no real
  // mic); on hardware it returns whatever is in the DMA buffer.
  size_t ReadSamples(int16_t *out, size_t max_samples);

  // Start/stop the I2S peripheral. power_mgmt uses Stop() to save
  // current when the mic is idle (research.md §7.6).
  void Start();
  void Stop();

  // Test seam: stuff synthetic samples for host tests.
  void InjectForTest(const int16_t *samples, size_t n);
};

}  // namespace tether::m5
