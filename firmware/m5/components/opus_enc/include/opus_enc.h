// opus_enc.h — Tether M5 Opus encoder (plan.md §4.5).
//
// Wraps libopus (host) or micro-opus / esp-libopus (firmware) and
// exposes a small `OpusEncoder` class that takes 20 ms frames of 16-bit
// PCM and returns VBR-encoded bytes. The default configuration is
// 8 kHz / 16 kbps / complexity 5, matching the audio_capture task
// expectations in plan.md §4.8.2.

#pragma once

#include <cstdint>
#include <vector>

namespace tether::m5 {

// Frame size at 8 kHz: 160 samples = 20 ms.
inline constexpr int kOpusFrameSamples = 160;

class OpusEncoder {
 public:
  // Construct an encoder for the given sample rate, bitrate, and
  // complexity. The default values match the spec.
  OpusEncoder(int sample_rate = 8000, int bitrate = 16000, int complexity = 5);

  ~OpusEncoder();

  OpusEncoder(const OpusEncoder &) = delete;
  OpusEncoder &operator=(const OpusEncoder &) = delete;

  // Encode a single 20 ms frame. `pcm` must point to exactly FrameSize()
  // int16_t samples (320 bytes at 8 kHz mono).
  std::vector<uint8_t> EncodeFrame(const int16_t *pcm);

  // Number of samples the encoder expects per EncodeFrame() call.
  int FrameSize() const { return frame_size_; }

  // Sample rate, in Hz.
  int SampleRate() const { return sample_rate_; }

  // Bitrate, in bits per second.
  int Bitrate() const { return bitrate_; }

  // Test seam: was the encoder successfully initialized?
  bool IsInitialized() const { return enc_ != nullptr; }

 private:
  int sample_rate_ = 8000;
  int bitrate_ = 16000;
  int complexity_ = 5;
  int frame_size_ = 160;
  void *enc_ = nullptr; // Opaque pointer to OpusEncoder state
};

}  // namespace tether::m5
