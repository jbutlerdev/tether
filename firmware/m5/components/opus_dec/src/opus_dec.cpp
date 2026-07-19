// opus_dec.cpp — implementation of tether::m5::OpusDecoder.
//
// On host tests, we link against the system libopus (libopus-dev).
// On real hardware the same API is implemented on top of micro-opus
// or esp-libopus; only this file changes (same pattern as opus_enc.cpp).

#include "opus_dec.h"

#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#include <opus/opus.h>
#else
#include "esp_log.h"
#include "opus.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.opusdec";
} // namespace

OpusDecoder::OpusDecoder(int sample_rate, int channels)
    : sample_rate_(sample_rate), channels_(channels) {
#ifdef TETHER_M5_HOST_TEST
  int err = OPUS_OK;
  ::OpusDecoder *d = opus_decoder_create(sample_rate_, channels_, &err);
  if (err != OPUS_OK || !d) {
    ESP_LOGE(kTag, "opus_decoder_create failed: %d", err);
    return;
  }
  dec_ = d;
#else
  int err = OPUS_OK;
  ::OpusDecoder *d = opus_decoder_create(sample_rate_, channels_, &err);
  if (err != OPUS_OK || !d) {
    ESP_LOGE(kTag, "opus_decoder_create failed: %d", err);
    return;
  }
  dec_ = d;
#endif
  frame_size_ = sample_rate_ * 20 / 1000; // 20 ms frame
}

OpusDecoder::~OpusDecoder() {
  if (dec_) {
#ifdef TETHER_M5_HOST_TEST
    opus_decoder_destroy(static_cast<::OpusDecoder *>(dec_));
#else
    opus_decoder_destroy(static_cast<::OpusDecoder *>(dec_));
#endif
    dec_ = nullptr;
  }
}

std::vector<int16_t> OpusDecoder::DecodeFrame(const uint8_t *data, size_t len) {
  std::vector<int16_t> out;
  if (!dec_ || !data || len == 0)
    return out;
  out.resize(frame_size_);
#ifdef TETHER_M5_HOST_TEST
  int n = opus_decode(static_cast<::OpusDecoder *>(dec_), data,
                      static_cast<opus_int32>(len), out.data(), frame_size_, 0);
#else
  int n = opus_decode(static_cast<::OpusDecoder *>(dec_), data,
                      static_cast<opus_int32>(len), out.data(), frame_size_, 0);
#endif
  if (n < 0) {
    ESP_LOGE(kTag, "opus_decode failed: %s", opus_strerror(n));
    out.clear();
    return out;
  }
  out.resize(n);
  return out;
}

} // namespace tether::m5
