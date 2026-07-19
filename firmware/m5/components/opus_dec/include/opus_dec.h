// opus_dec.h — Tether M5 Opus decoder.
//
// Wraps libopus (host) or micro-opus (firmware) and exposes a small
// OpusDecoder class that takes a compressed Opus frame and returns
// 20 ms of 16-bit PCM at 8 kHz mono. The default configuration matches
// the encoder in opus_enc.h (8 kHz / mono / VOIP).
//
// The decoder is used by the TTS playback path: incoming TTS_DATA
// payloads are length-delimited blobs of Opus frames (see
// go/internal/codec/framer.go for the format); the main loop splits
// the blob into frames, decodes each with OpusDecoder, and writes the
// PCM to the I2S amp.

#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace tether::m5 {

class OpusDecoder {
public:
  // Construct a decoder for the given sample rate and channel count.
  // Defaults: 8 kHz / mono, matching the encoder.
  OpusDecoder(int sample_rate = 8000, int channels = 1);

  ~OpusDecoder();

  OpusDecoder(const OpusDecoder &) = delete;
  OpusDecoder &operator=(const OpusDecoder &) = delete;

  // Decode a single compressed Opus frame into PCM. Returns the
  // decoded samples (up to 160 at 8 kHz / 20 ms). An empty input
  // returns an empty vector. A decode failure returns an empty
  // vector (the error is logged).
  std::vector<int16_t> DecodeFrame(const uint8_t *data, size_t len);

  // Number of samples per frame at the configured sample rate.
  int FrameSize() const { return frame_size_; }

  // Sample rate, in Hz.
  int SampleRate() const { return sample_rate_; }

  // Test seam: was the decoder successfully initialized?
  bool IsInitialized() const { return dec_ != nullptr; }

private:
  int sample_rate_ = 8000;
  int channels_ = 1;
  int frame_size_ = 160;
  void *dec_ = nullptr; // Opaque pointer to OpusDecoder state
};

} // namespace tether::m5
