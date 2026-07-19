// i2s_amp.cpp — Tether I2S amplifier with tone generator.
//
// On real hardware this drives a MAX98357A class-D amp over I2S TX
// at 8 kHz, 16-bit, mono. Two topologies are supported, selected by
// board.h::kI2sAmpPort:
//
//   - SHARED bus (M5, kI2sAmpPort == kI2sMicPort == I2S_NUM_0): the
//     amp shares I2S0 with the mic in full-duplex mode. Whichever
//     Init() runs first creates the tx+rx channel pair (stored in
//     g_i2s_tx_handle / g_i2s_rx_handle); the second is a no-op.
//
//   - SEPARATE bus (MVSR, kI2sAmpPort == I2S_NUM_1): the amp owns its
//     own TX-only channel on I2S1 using the dedicated amp pins
//     (kPinAmpBclk/Ws/Dout). The mic owns I2S0 independently.
//
// See board.h and docs/VARIANTS.md.

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
} // namespace

#ifndef TETHER_M5_HOST_TEST
// Shared I2S channel handles for the M5's full-duplex bus. Owned by
// whichever component's Init() runs first; declared extern in
// i2s_mic.cpp. On the MVSR (separate buses) g_i2s_tx_handle is unused
// — the amp owns s_amp_tx_handle on I2S1 instead.
i2s_chan_handle_t g_i2s_tx_handle = nullptr;
i2s_chan_handle_t g_i2s_rx_handle = nullptr;
// Standalone TX handle for the non-shared variants (MVSR).
static i2s_chan_handle_t s_amp_tx_handle = nullptr;
#endif

bool I2SAmp::Init() {
#ifdef TETHER_M5_HOST_TEST
  return true;
#else
  // ── Shared full-duplex bus (M5): reuse the shared handle pair. ──
  if constexpr (board::kI2sAmpPort == board::kI2sMicPort) {
    if (g_i2s_tx_handle) {
      return true; // Already initialized.
    }
    i2s_chan_config_t chan_cfg = {};
    chan_cfg.id = static_cast<i2s_port_t>(board::kI2sAmpPort);
    chan_cfg.role = I2S_ROLE_MASTER;
    chan_cfg.dma_desc_num = 4;
    chan_cfg.dma_frame_num = 256;
    chan_cfg.auto_clear = true;
    esp_err_t err =
        i2s_new_channel(&chan_cfg, &g_i2s_tx_handle, &g_i2s_rx_handle);
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
    std_cfg.gpio_cfg.bclk = board::kPinI2sBclk;
    std_cfg.gpio_cfg.ws = board::kPinI2sWs;
    std_cfg.gpio_cfg.dout = board::kPinI2sDout;
    std_cfg.gpio_cfg.din = board::kPinI2sDin;
    std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
    std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
    std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
    std_cfg.gpio_cfg.invert_flags.ws_inv = false;
    err = i2s_channel_init_std_mode(g_i2s_tx_handle, &std_cfg);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_init_std_mode(tx): %d", err);
      return false;
    }
    err = i2s_channel_init_std_mode(g_i2s_rx_handle, &std_cfg);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_init_std_mode(rx): %d", err);
      return false;
    }
    err = i2s_channel_enable(g_i2s_tx_handle);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_enable(tx): %d", err);
      return false;
    }
    err = i2s_channel_enable(g_i2s_rx_handle);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_enable(rx): %d", err);
      return false;
    }
    return true;
  }

  // ── Separate bus (MVSR): own the TX handle on kI2sAmpPort. ──────
  if (s_amp_tx_handle) {
    return true;
  }
  i2s_chan_config_t chan_cfg = {};
  chan_cfg.id = static_cast<i2s_port_t>(board::kI2sAmpPort);
  chan_cfg.role = I2S_ROLE_MASTER;
  chan_cfg.dma_desc_num = 4;
  chan_cfg.dma_frame_num = 256;
  chan_cfg.auto_clear = true;
  // TX-only channel (the mic owns its own RX channel on kI2sMicPort).
  esp_err_t err = i2s_new_channel(&chan_cfg, &s_amp_tx_handle, nullptr);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_new_channel(tx-only): %d", err);
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
  // MVSR amp pins (MAX98357A on I2S1).
  std_cfg.gpio_cfg.bclk = board::kPinAmpBclk;
  std_cfg.gpio_cfg.ws = board::kPinAmpWs;
  std_cfg.gpio_cfg.dout = board::kPinAmpDout;
  std_cfg.gpio_cfg.din = I2S_GPIO_UNUSED; // TX-only
  std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.ws_inv = false;
  err = i2s_channel_init_std_mode(s_amp_tx_handle, &std_cfg);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_init_std_mode(tx): %d", err);
    return false;
  }
  err = i2s_channel_enable(s_amp_tx_handle);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_enable(tx): %d", err);
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

size_t I2SAmp::WritePCM(const int16_t *pcm, size_t num_samples) {
  if (!pcm || num_samples == 0)
    return 0;
#ifdef TETHER_M5_HOST_TEST
  // Host: no-op, just count the samples for test verification.
  total_samples_ += num_samples;
  return num_samples;
#else
  i2s_chan_handle_t tx = (board::kI2sAmpPort == board::kI2sMicPort)
                             ? g_i2s_tx_handle
                             : s_amp_tx_handle;
  if (!tx)
    return 0;
  size_t bytes_written = 0;
  size_t bytes_to_write = num_samples * sizeof(int16_t);
  esp_err_t err =
      i2s_channel_write(tx, pcm, bytes_to_write, &bytes_written, portMAX_DELAY);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_write: %d", err);
    return 0;
  }
  size_t samples_written = bytes_written / sizeof(int16_t);
  total_samples_ += samples_written;
  return samples_written;
#endif
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
  // buffer. Non-blocking write; if the DMA is full (shouldn't
  // happen at 8 kHz with 4×256-frame buffers) we drop.
  i2s_chan_handle_t tx = (board::kI2sAmpPort == board::kI2sMicPort)
                             ? g_i2s_tx_handle
                             : s_amp_tx_handle;
  if (tx && i > 0) {
    size_t bytes_written = 0;
    i2s_channel_write(tx, out, i * sizeof(int16_t), &bytes_written, 0);
  }
#endif
  return i;
}

void I2SAmp::ResetForTest() {
  Stop();
  total_samples_ = 0;
}

} // namespace tether::m5
