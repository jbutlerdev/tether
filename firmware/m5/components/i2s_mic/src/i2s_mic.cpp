// i2s_mic.cpp — I2S / PDM microphone implementation.
//
// Two mic topologies are supported, selected by board.h:
//
//   - kMicInterface == kI2sStd on a SHARED bus (M5): the mic and amp
//     share I2S0 in full-duplex mode (same BCLK/WS, DIN=mic, DOUT=amp).
//     Whichever Init() runs first creates the tx+rx channel pair; the
//     other is a no-op. The shared handles live as globals in
//     i2s_amp.cpp.
//
//   - kMicInterface == kI2sStd on a SEPARATE bus (MVSR V1.0, MSM261):
//     the mic owns I2S0 RX-only; the amp owns I2S1 TX-only. No shared
//     handles.
//
//   - kMicInterface == kPdm (MVSR V1.1, MP34DT05-A): the mic is a PDM
//     RX device on I2S0. The PDM peripheral uses a single CLK + DATA
//     pair (no WS); kPinI2sWs is the PDM clock, kPinI2sDin is the data.
//
// The port (I2S_NUM_0 / I2S_NUM_1) comes from board::kI2sMicPort so
// the driver code is shared across variants. See board.h and
// docs/VARIANTS.md.

#include "i2s_mic.h"

#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#if __has_include("driver/i2s_pdm.h")
#include "driver/i2s_pdm.h"
#endif
#include "driver/i2s_std.h"
#endif

#include "board.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.mic";
} // namespace

#ifndef TETHER_M5_HOST_TEST
// Shared I2S channel handles for the M5's full-duplex bus (kI2sMicPort
// == kI2sAmpPort). i2s_amp.cpp defines the storage; this component
// reads/writes them. On the MVSR (separate buses) these are unused —
// each component owns a local handle.
extern i2s_chan_handle_t g_i2s_tx_handle;
extern i2s_chan_handle_t g_i2s_rx_handle;

// Standalone RX handle for the non-shared variants (MVSR). nullptr on
// the M5, which uses the shared g_i2s_rx_handle instead.
static i2s_chan_handle_t s_mic_rx_handle = nullptr;
#endif

bool I2SMic::Init() {
#ifdef TETHER_M5_HOST_TEST
  return true;
#else
  // ── Shared full-duplex bus (M5): reuse the shared handle pair. ──
  if constexpr (board::kI2sMicPort == board::kI2sAmpPort) {
    if (g_i2s_rx_handle) {
      return true; // Already initialized by the amp or an earlier call.
    }
    i2s_chan_config_t chan_cfg = {};
    chan_cfg.id = static_cast<i2s_port_t>(board::kI2sMicPort);
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
    std_cfg.gpio_cfg.bclk = board::kPinI2sBclk;
    std_cfg.gpio_cfg.ws = board::kPinI2sWs;
    std_cfg.gpio_cfg.din = board::kPinI2sDin;
    std_cfg.gpio_cfg.dout = board::kPinI2sDout;
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
  }

  // ── Separate bus (MVSR): own the RX handle on kI2sMicPort. ──────
  if (s_mic_rx_handle) {
    return true;
  }
  i2s_chan_config_t chan_cfg = {};
  chan_cfg.id = static_cast<i2s_port_t>(board::kI2sMicPort);
  chan_cfg.role = I2S_ROLE_MASTER;
  chan_cfg.dma_desc_num = 4;
  chan_cfg.dma_frame_num = 256;
  chan_cfg.auto_clear = false;
  // RX-only channel (the amp owns its own TX channel on kI2sAmpPort).
  esp_err_t err = i2s_new_channel(&chan_cfg, nullptr, &s_mic_rx_handle);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_new_channel(rx-only): %d", err);
    return false;
  }

  if constexpr (board::kMicInterface == board::MicInterface::kPdm) {
    // PDM RX (MVSR V1.1, MP34DT05-A). Single CLK + DATA pair (no WS).
    // Use the ESP-IDF v5.2 default-config macros for the clock and slot
    // configs (the struct fields differ across IDF versions; the macros
    // are the stable API), then set only the two GPIO pins the v5.2 PDM
    // RX gpio config exposes: .clk (PDM clock) and .din (PDM data).
    i2s_pdm_rx_config_t pdm_cfg = {};
    pdm_cfg.clk_cfg = I2S_PDM_RX_CLK_DEFAULT_CONFIG(8000);
    pdm_cfg.slot_cfg = I2S_PDM_RX_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT,
                                                      I2S_SLOT_MODE_MONO);
    pdm_cfg.gpio_cfg.clk = board::kPinI2sWs;  // PDM clock
    pdm_cfg.gpio_cfg.din = board::kPinI2sDin; // PDM data
    err = i2s_channel_init_pdm_rx_mode(s_mic_rx_handle, &pdm_cfg);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_init_pdm_rx_mode: %d", err);
      return false;
    }
  } else {
    // Standard I2S RX (MVSR V1.0, MSM261). BCLK + WS + DIN.
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
    std_cfg.gpio_cfg.din = board::kPinI2sDin;
    std_cfg.gpio_cfg.dout = I2S_GPIO_UNUSED; // RX-only
    std_cfg.gpio_cfg.mclk = I2S_GPIO_UNUSED;
    std_cfg.gpio_cfg.invert_flags.mclk_inv = false;
    std_cfg.gpio_cfg.invert_flags.bclk_inv = false;
    std_cfg.gpio_cfg.invert_flags.ws_inv = false;
    err = i2s_channel_init_std_mode(s_mic_rx_handle, &std_cfg);
    if (err != ESP_OK) {
      ESP_LOGE(kTag, "i2s_channel_init_std_mode(rx): %d", err);
      return false;
    }
  }
  err = i2s_channel_enable(s_mic_rx_handle);
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
  i2s_chan_handle_t rx = nullptr;
  if constexpr (board::kI2sMicPort == board::kI2sAmpPort) {
    rx = g_i2s_rx_handle; // shared (M5)
  } else {
    rx = s_mic_rx_handle; // standalone (MVSR)
  }
  size_t bytes_read = 0;
  if (rx) {
    i2s_channel_read(rx, out, max_samples * sizeof(int16_t), &bytes_read,
                     portMAX_DELAY);
  }
  return bytes_read / sizeof(int16_t);
#endif
}

void I2SMic::Start() {
#ifdef TETHER_M5_HOST_TEST
#else
  i2s_chan_handle_t rx = (board::kI2sMicPort == board::kI2sAmpPort)
                             ? g_i2s_rx_handle
                             : s_mic_rx_handle;
  if (rx)
    i2s_channel_enable(rx);
#endif
}

void I2SMic::Stop() {
#ifdef TETHER_M5_HOST_TEST
#else
  i2s_chan_handle_t rx = (board::kI2sMicPort == board::kI2sAmpPort)
                             ? g_i2s_rx_handle
                             : s_mic_rx_handle;
  if (rx)
    i2s_channel_disable(rx);
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
