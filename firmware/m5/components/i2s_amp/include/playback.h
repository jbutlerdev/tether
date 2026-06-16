// playback.h — v2 M5-side TTS playback hook. See plan.md §10.4
// (Task 9.4).
//
// v2: M5-side TTS playback. Today the M5 only plays test
// tones through the I2S amp (see I2SAmp in i2s_amp.h); v2 will
// add a PlayPcm() method that streams Opus-decoded PCM (the
// TTS audio from the base station) into the same amp buffer.
//
// The hook point is PlayPcm: today it is a no-op that returns
// the input length; v2 will enqueue the buffer and return the
// number of samples actually written to the DMA.
//
// The stub is unit-tested by test_playback.cpp on the host to
// assert the symbol exists and that v1 returns the input
// length unchanged. v2 will invert the test: the function
// must return less than the input length when the playback
// buffer is full (backpressure).

#pragma once

#include <cstddef>
#include <cstdint>

namespace tether::m5 {

// kPlaybackMaxChunkSamples is the maximum number of samples
// the v1 stub accepts in a single PlayPcm() call. v2 will
// have a real DMA-backed ring buffer; the constant exists so
// v1 callers can size their chunks without breaking when v2
// lands.
inline constexpr std::size_t kPlaybackMaxChunkSamples = 4096;

// PlayPcm is the v2 M5-side TTS playback hook. v1 is a
// no-op: it returns `num_samples` unchanged and the input
// buffer is dropped. v2 will enqueue the buffer into a
// DMA-backed ring and return the number of samples actually
// written, which may be less than `num_samples` when the
// ring is full (the caller is expected to retry).
//
// The input is 16-bit signed PCM at 8 kHz, mono — the same
// shape the Opus decoder emits on the M5 (see
// components/opus_enc/).
//
// The function is pure-C++: it does not touch SPI or DMA
// directly. The I2S driver task calls PlayPcm and reads
// samples out via I2SAmp::ReadSamples().
//
// v2 callers will not need to change: PlayPcm keeps the
// same signature; v2 just returns a meaningful value.
inline std::size_t PlayPcm(const int16_t *samples, std::size_t num_samples) {
  // v2: M5-side TTS playback. Replace this body with a
  // real implementation that:
  //   - Acquires the playback ring buffer mutex.
  //   - Copies min(num_samples, ring_free) samples into
  //     the ring.
  //   - Returns the number of samples copied.
  //   - Wakes the I2S DMA task if it was idle.
  (void)samples;
  return num_samples;
}

} // namespace tether::m5
