// i2s_mic.cpp — I2S microphone implementation. On real hardware this
// drives the INMP441 over I2S RX. On host it is a thin shim that
// returns injected samples (used by the audio_capture tests).
//
// We use the new I2S API (driver/i2s_std.h) for IDF 5.2+.

#include "i2s_mic.h"

#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/i2s_std.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.mic";

#ifndef TETHER_M5_HOST_TEST
// I2S channel handle. Real hardware stores this as a member of the
// Init() body; host tests don't need it.
i2s_chan_handle_t g_rx_handle = nullptr;
#endif
} // namespace

bool I2SMic::Init() {
#ifdef TETHER_M5_HOST_TEST
  return true;
#else
  i2s_chan_config_t chan_cfg = {};
  chan_cfg.id = I2S_NUM_0;
  chan_cfg.role = I2S_ROLE_MASTER;
  chan_cfg.dma_desc_num = 4;
  chan_cfg.dma_frame_num = 256;
  chan_cfg.auto_clear = false;
  esp_err_t err = i2s_new_channel(&chan_cfg, nullptr, &g_rx_handle);
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
  std_cfg.slot_cfg.left_align = true;
  std_cfg.slot_cfg.big_endian = false;
  std_cfg.gpio_cfg.bclk = (gpio_num_t)4;
  std_cfg.gpio_cfg.ws = (gpio_num_t)5;
  std_cfg.gpio_cfg.din = (gpio_num_t)6;
  std_cfg.gpio_cfg.dout = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.ws_inv = false;
  err = i2s_channel_init_std_mode(g_rx_handle, &std_cfg);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_init_std_mode: %d", err);
    return false;
  }
  err = i2s_channel_enable(g_rx_handle);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_enable: %d", err);
    return false;
  }
  return true;
#endif
}

size_t I2SMic::ReadSamples(int16_t *out, size_t max_samples) {
#ifdef TETHER_M5_HOST_TEST
  extern std::vector<int16_t> g_mic_injected;
  if (!g_mic_injected.empty() && out) {
    size_t n = std::min(max_samples, g_mic_injected.size());
    std::copy(g_mic_injected.begin(), g_mic_injected.begin() + n, out);
    g_mic_injected.erase(g_mic_injected.begin(),
                         g_mic_injected.begin() + n);
    return n;
  }
  return 0;
#else
  size_t bytes_read = 0;
  if (g_rx_handle) {
    i2s_channel_read(g_rx_handle, out, max_samples * sizeof(int16_t),
                     &bytes_read, portMAX_DELAY);
  }
  return bytes_read / sizeof(int16_t);
#endif
}

void I2SMic::Start() {
#ifdef TETHER_M5_HOST_TEST
#else
  if (g_rx_handle) i2s_channel_enable(g_rx_handle);
#endif
}

void I2SMic::Stop() {
#ifdef TETHER_M5_HOST_TEST
#else
  if (g_rx_handle) i2s_channel_disable(g_rx_handle);
#endif
}

void I2SMic::InjectForTest(const int16_t *samples, size_t n) {
#ifdef TETHER_M5_HOST_TEST
  extern std::vector<int16_t> g_mic_injected;
  g_mic_injected.assign(samples, samples + n);
#else
  (void)samples; (void)n;
#endif
}

#ifdef TETHER_M5_HOST_TEST
std::vector<int16_t> g_mic_injected;
#endif

}  // namespace tether::m5
