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
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/i2s_std.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#endif

#include "board.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.amp";
constexpr int kAmpVolume = 8000; // peak amplitude

#ifdef TETHER_M5_HOST_TEST
// No I2S handle in host tests; the amp is driven through ReadSamples()
// which fills a buffer with synthetic sine data.
#else
// I2S channel handle. Real hardware stores this as a process-global
// (the M5 has exactly one I2S TX port for the amp).
i2s_chan_handle_t g_tx_handle = nullptr;
#endif
}  // namespace

bool I2SAmp::Init() {
#ifdef TETHER_M5_HOST_TEST
  return true;
#else
  // The amp is driven by I2S1 in TX master mode. Pin map is in
  // main/board.h; the system architect chose this split config
  // because the M5 has no three free pins in a row for the amp on
  // a single edge — LRC and BCLK are on the right (47/48) and DIN
  // is on the left (18). See the comment block in board.h for
  // why GPIO 47/48 are not the I2C1 SDA/SCL on Tether.
  i2s_chan_config_t chan_cfg = {};
  chan_cfg.id = I2S_NUM_1;
  chan_cfg.role = I2S_ROLE_MASTER;
  chan_cfg.dma_desc_num = 4;
  chan_cfg.dma_frame_num = 256;
  chan_cfg.auto_clear = true;
  esp_err_t err = i2s_new_channel(&chan_cfg, &g_tx_handle, nullptr);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_new_channel: %d", err);
    return false;
  }
  i2s_std_config_t std_cfg = {};
  std_cfg.clk_cfg.sample_rate_hz = 8000;
  std_cfg.clk_cfg.clk_src = I2S_CLK_SRC_DEFAULT;
  std_cfg.clk_cfg.mclk_multiple = I2S_MCLK_MULTIPLE_256;
  std_cfg.slot_cfg.data_bit_width = I2S_DATA_BIT_WIDTH_16BIT;
  std_cfg.slot_cfg.slot_bit_width = I2S_SLOT_BIT_WIDTH_AUTO;
  std_cfg.slot_cfg.slot_mode = I2S_SLOT_MODE_MONO;
  std_cfg.slot_cfg.slot_mask = I2S_STD_SLOT_LEFT;
  std_cfg.slot_cfg.ws_width = 16;
  std_cfg.slot_cfg.ws_pol = false;
  std_cfg.slot_cfg.bit_shift = true;
  // bit_shift=true gives left-align / MSB-first, which is what the
  // MAX98357A wants. (ESP-IDF v5.2 removed the explicit
  // left_align/big_endian members; bit_shift is the new API.)
  std_cfg.gpio_cfg.bclk = board::kPinI2s1Bclk;
  std_cfg.gpio_cfg.ws = board::kPinI2s1Ws;
  std_cfg.gpio_cfg.dout = board::kPinI2s1Dout;
  std_cfg.gpio_cfg.din = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.ws_inv = false;
  err = i2s_channel_init_std_mode(g_tx_handle, &std_cfg);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_init_std_mode: %d", err);
    return false;
  }
  err = i2s_channel_enable(g_tx_handle);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_enable: %d", err);
    return false;
  }
  return true;
#endif
}

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
#ifdef TETHER_M5_HOST_TEST
  // No-op in host tests; the buffer is drained by the test.
#else
  // On real hardware, push the synthesized samples into the I2S DMA
  // buffer. We use a small non-blocking write; if the DMA is full
  // (shouldn't happen at 8 kHz with 4×256-frame buffers) we drop.
  if (g_tx_handle && i > 0) {
    size_t bytes_written = 0;
    i2s_channel_write(g_tx_handle, out, i * sizeof(int16_t),
                      &bytes_written, 0);
  }
#endif
  return i;
}

void I2SAmp::ResetForTest() {
  Stop();
  total_samples_ = 0;
}

}  // namespace tether::m5
