// audio_capture.cpp — implementation of tether::m5::AudioCapture.
//
// On real hardware the RunOnce() body drains 160 samples from I2S DMA,
// passes them to the Opus encoder, and writes the encoded bytes into
// the PSRAM ring. If the ring is full, the frame is dropped and a
// counter is incremented. The host build uses a static mock PCM
// buffer (set via SetInputPcmForTest) so tests can exercise the path
// without real audio.

#include "audio_capture.h"

#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#endif

#include "i2s_mic.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.audcap";

// Global mock PCM buffer for host tests. Real hardware reads from the
// I2S peripheral via I2SMic::ReadSamples.
int16_t g_mock_pcm[160] = {};
} // namespace

AudioCapture::AudioCapture(PsramRing &ring, OpusEncoder &enc)
    : ring_(ring), enc_(enc) {
#ifdef TETHER_M5_HOST_TEST
  // Host tests drive RunOnce() directly without Init() (which would
  // require real I2S hardware), so default to "running" on the host.
  // On real hardware Init() sets this true after I2SMic::Init succeeds.
  i2s_running_ = true;
#endif
}

bool AudioCapture::Init() {
  I2SMic mic;
  if (!mic.Init()) {
    ESP_LOGE(kTag, "I2SMic::Init failed");
    return false;
  }
  i2s_running_ = true;
  return true;
}

size_t AudioCapture::RunOnce() {
  if (!i2s_running_)
    return 0;
    // Drain one frame of PCM. Real hardware calls I2SMic::ReadSamples;
    // host tests use the g_mock_pcm buffer.
#ifdef TETHER_M5_HOST_TEST
  std::memcpy(pcm_buf_, g_mock_pcm, sizeof(pcm_buf_));
#else
  I2SMic mic;
  size_t n = mic.ReadSamples(pcm_buf_, 160);
  if (n < 160) {
    // DMA underrun: pad with zeros and restart.
    std::memset(pcm_buf_ + n, 0, (160 - n) * sizeof(int16_t));
  }
#endif
  if (mock_allocs_per_run_ > 0) {
    allocs_during_run_ += mock_allocs_per_run_;
  }
  auto bytes = enc_.EncodeFrame(pcm_buf_);
  if (bytes.empty()) {
    frames_dropped_++;
    return 0;
  }
  // Write a 2-byte little-endian length prefix + frame data so the
  // drain task can split the ring back into individual Opus frames.
  // If the ring can't fit both, drop the frame.
  size_t needed = 2 + bytes.size();
  if (ring_.FreeSpace() < needed) {
    frames_dropped_++;
    return 0;
  }
  uint8_t lenBuf[2] = {static_cast<uint8_t>(bytes.size() & 0xFF),
                       static_cast<uint8_t>((bytes.size() >> 8) & 0xFF)};
  ring_.Write(lenBuf, 2);
  size_t written = ring_.Write(bytes.data(), bytes.size());
  if (written == 0) {
    frames_dropped_++;
    return 0;
  }
  frames_encoded_++;
  return written + 2;
}

void AudioCapture::SetInputPcmForTest(const int16_t *pcm, size_t n) {
  if (!pcm)
    return;
  size_t take = (n < 160) ? n : 160;
  std::memcpy(g_mock_pcm, pcm, take * sizeof(int16_t));
  if (take < 160) {
    std::memset(g_mock_pcm + take, 0, (160 - take) * sizeof(int16_t));
  }
}

} // namespace tether::m5
