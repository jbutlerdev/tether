// i2s_mic.cpp — I2S microphone implementation. On real hardware this
// drives the INMP441 over I2S RX. On host it is a thin shim that
// returns injected samples (used by the audio_capture tests).

#include "i2s_mic.h"

#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/i2s.h"
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.mic";
} // namespace

bool I2SMic::Init() {
#ifdef TETHER_M5_HOST_TEST
  // No real I2S peripheral on host; tests use InjectForTest.
  return true;
#else
  i2s_config_t cfg = {};
  cfg.mode = I2S_MODE_MASTER | I2S_MODE_RX;
  cfg.sample_rate = 8000;
  cfg.bits_per_sample = I2S_BITS_PER_SAMPLE_16BIT;
  cfg.channel_format = I2S_CHANNEL_FMT_ONLY_LEFT;
  cfg.communication_format = I2S_COMM_FORMAT_STAND_I2S;
  cfg.intr_alloc_flags = 0;
  cfg.dma_buf_count = 4;
  cfg.dma_buf_len = 256;
  cfg.use_apll = false;
  cfg.tx_desc_auto_clear = false;
  cfg.fixed_mclk = 0;
  i2s_pin_config_t pins = {};
  pins.bck_io_num = 4;     // I2S_BCK
  pins.ws_io_num = 5;      // I2S_WS (LRCLK)
  pins.data_in_num = 6;    // INMP441 SD
  pins.data_out_num = I2S_PIN_NO_CHANGE;
  esp_err_t err = i2s_driver_install(I2S_NUM_0, &cfg, 0, nullptr);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_driver_install: %d", err);
    return false;
  }
  err = i2s_set_pin(I2S_NUM_0, &pins);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2s_set_pin: %d", err);
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
  i2s_read(I2S_NUM_0, out, max_samples * sizeof(int16_t), &bytes_read,
           portMAX_DELAY);
  return bytes_read / sizeof(int16_t);
#endif
}

void I2SMic::Start() {
#ifdef TETHER_M5_HOST_TEST
  // No-op on host.
#else
  i2s_start(I2S_NUM_0);
#endif
}

void I2SMic::Stop() {
#ifdef TETHER_M5_HOST_TEST
  // No-op on host.
#else
  i2s_stop(I2S_NUM_0);
#endif
}

void I2SMic::InjectForTest(const int16_t *samples, size_t n) {
#ifdef TETHER_M5_HOST_TEST
  // We piggyback the injected samples on the ReadSamples() path. In
  // production this method is a no-op (the real mic is the source).
  // Tests call InjectForTest() before ReadSamples().
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
