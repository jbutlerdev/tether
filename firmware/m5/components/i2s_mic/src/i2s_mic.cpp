// i2s_mic.cpp — I2S microphone implementation.
//
// On real hardware this drives the INMP441 over I2S RX. The mic
// shares the I2S0 bus with the amp (i2s_amp) in full-duplex mode:
// the same BCLK and WS signals drive both, with the mic on the DIN
// line and the amp on the DOUT line. See board.h::kPinI2s* for
// the pin map and the three hardware mods that are required to
// free GPIO 9 / 10 / 12.
//
// The I2S0 peripheral is initialized in shared full-duplex mode by
// I2SAmp::Init() or I2SMic::Init() — whichever runs first. Both
// Init() methods are idempotent: a second call is a no-op once the
// channel handle is set.
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

#include "board.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.mic";
} // namespace

#ifndef TETHER_M5_HOST_TEST
// I2S0 channel handles, shared with i2s_amp. Defined in
// i2s_amp.cpp; declared extern here so this component can use
// the RX handle. C++ linkage (no extern "C" wrapper).
extern i2s_chan_handle_t g_i2s_tx_handle;
extern i2s_chan_handle_t g_i2s_rx_handle;
#endif

bool I2SMic::Init() {
#ifdef TETHER_M5_HOST_TEST
  return true;
#else
  if (g_i2s_rx_handle) {
    return true; // Already initialized (by the amp or earlier).
  }
  // Full-duplex I2S0 init. Same pin map for both TX (amp) and RX
  // (mic); the peripheral drives both directions on the shared
  // SCK/WS lines.
  i2s_chan_config_t chan_cfg = {};
  chan_cfg.id = I2S_NUM_0;
  chan_cfg.role = I2S_ROLE_MASTER;
  chan_cfg.dma_desc_num = 4;
  chan_cfg.dma_frame_num = 256;
  chan_cfg.auto_clear = false;
  i2s_chan_handle_t tx_handle = nullptr;
  esp_err_t err = i2s_new_channel(&chan_cfg, &tx_handle, &g_i2s_rx_handle);
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
  // bit_shift=true gives left-align / MSB-first, which is what
  // the INMP441 wants. (ESP-IDF v5.2 removed the explicit
  // left_align/big_endian members; bit_shift is the new API.)
  std_cfg.gpio_cfg.bclk = board::kPinI2sBclk;
  std_cfg.gpio_cfg.ws = board::kPinI2sWs;
  std_cfg.gpio_cfg.din = board::kPinI2sDin;   // mic SD
  std_cfg.gpio_cfg.dout = board::kPinI2sDout; // amp DIN (shared bus)
  std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
  std_cfg.gpio_cfg.invert_flags.ws_inv = false;

  err = i2s_channel_init_std_mode(tx_handle, &std_cfg);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_init_std_mode(tx): %d", err);
    return false;
  }
  err = i2s_channel_init_std_mode(g_i2s_rx_handle, &std_cfg);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_channel_init_std_mode(rx): %d", err);
    return false;
  }
  err = i2s_channel_enable(tx_handle);
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
#endif
}

size_t I2SMic::ReadSamples(int16_t *out, size_t max_samples) {
#ifdef TETHER_M5_HOST_TEST
  extern std::vector<int16_t> g_mic_injected;
  if (!g_mic_injected.empty() && out) {
    size_t n = std::min(max_samples, g_mic_injected.size());
    std::copy(g_mic_injected.begin(), g_mic_injected.begin() + n, out);
    g_mic_injected.erase(g_mic_injected.begin(), g_mic_injected.begin() + n);
    return n;
  }
  return 0;
#else
  size_t bytes_read = 0;
  if (g_i2s_rx_handle) {
    i2s_channel_read(g_i2s_rx_handle, out, max_samples * sizeof(int16_t),
                     &bytes_read, portMAX_DELAY);
  }
  return bytes_read / sizeof(int16_t);
#endif
}

void I2SMic::Start() {
#ifdef TETHER_M5_HOST_TEST
#else
  if (g_i2s_rx_handle)
    i2s_channel_enable(g_i2s_rx_handle);
#endif
}

void I2SMic::Stop() {
#ifdef TETHER_M5_HOST_TEST
#else
  if (g_i2s_rx_handle)
    i2s_channel_disable(g_i2s_rx_handle);
#endif
}

void I2SMic::InjectForTest(const int16_t *samples, size_t n) {
#ifdef TETHER_M5_HOST_TEST
  extern std::vector<int16_t> g_mic_injected;
  g_mic_injected.assign(samples, samples + n);
#else
  (void)samples;
  (void)n;
#endif
}

#ifdef TETHER_M5_HOST_TEST
std::vector<int16_t> g_mic_injected;
#endif

} // namespace tether::m5
