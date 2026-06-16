// opus_enc.cpp — implementation of tether::m5::OpusEncoder.
//
// On host tests, we link against the system libopus (libopus-dev). The
// `OpusEncoder` opaque pointer holds an `::OpusEncoder *` from
// <opus/opus.h>. On real hardware the same API is implemented on top
// of micro-opus or esp-libopus; only this file changes.

#include "opus_enc.h"

#include <cstring>
#include <stdexcept>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h" // provides ESP_LOGI/ESP_LOGE etc.
#else
#include "esp_log.h"
#endif

#ifdef TETHER_M5_HOST_TEST
#include <opus/opus.h>
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.opus";
constexpr int kOpusMaxPacketBytes = 4000;
} // namespace

OpusEncoder::OpusEncoder(int sample_rate, int bitrate, int complexity)
    : sample_rate_(sample_rate), bitrate_(bitrate), complexity_(complexity) {
#ifdef TETHER_M5_HOST_TEST
  int err = OPUS_OK;
  ::OpusEncoder *e = opus_encoder_create(sample_rate_, 1 /* mono */,
                                         OPUS_APPLICATION_VOIP, &err);
  if (err != OPUS_OK || !e) {
    ESP_LOGE(kTag, "opus_encoder_create failed: %d", err);
    return;
  }
  opus_encoder_ctl(e, OPUS_SET_BITRATE(bitrate_));
  opus_encoder_ctl(e, OPUS_SET_COMPLEXITY(complexity_));
  opus_encoder_ctl(e, OPUS_SET_VBR(1)); // VBR on
  opus_encoder_ctl(e, OPUS_SET_VBR_CONSTRAINT(0));
  opus_encoder_ctl(e, OPUS_SET_PACKET_LOSS_PERC(0));
  enc_ = e;
  // 20 ms frame at 8 kHz = 160 samples.
  frame_size_ = sample_rate_ * 20 / 1000;
#endif
}

OpusEncoder::~OpusEncoder() {
#ifdef TETHER_M5_HOST_TEST
  if (enc_) {
    opus_encoder_destroy(static_cast<::OpusEncoder *>(enc_));
    enc_ = nullptr;
  }
#endif
}

std::vector<uint8_t> OpusEncoder::EncodeFrame(const int16_t *pcm) {
  std::vector<uint8_t> out;
  if (!enc_ || !pcm)
    return out;
#ifdef TETHER_M5_HOST_TEST
  out.resize(kOpusMaxPacketBytes);
  opus_int32 n = opus_encode(static_cast<::OpusEncoder *>(enc_), pcm,
                             frame_size_, out.data(), out.size());
  if (n < 0) {
    ESP_LOGE(kTag, "opus_encode: %s", opus_strerror(n));
    out.clear();
    return out;
  }
  out.resize(n);
#endif
  return out;
}

} // namespace tether::m5
