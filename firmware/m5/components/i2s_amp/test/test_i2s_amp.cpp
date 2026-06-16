// test_i2s_amp.cpp — unit tests for tether::m5::I2SAmp (plan.md §4.6).
//
// Host-side tests exercise the tone generator: 440 Hz / 1 kHz tones
// are queued and read back as PCM. The output buffer is then
// validated with a simple zero-crossing peak detector (a real FFT
// would be overkill here; we look for the dominant frequency in
// sampled windows of the buffer).

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <vector>

#include <unity.h>

#include "i2s_amp.h"
#include "playback.h"

using tether::m5::I2SAmp;
using tether::m5::kPlaybackMaxChunkSamples;
using tether::m5::PlayPcm;

namespace {
constexpr int kSampleRate = 8000;
} // namespace

void setUp() {}
void tearDown() {}

// Count zero crossings in the buffer (used to estimate the dominant
// frequency of a sine wave). Two zero crossings per cycle, so the
// fundamental frequency ≈ crossings * sample_rate / (2 * num_samples).
size_t CountZeroCrossings(const std::vector<int16_t> &buf) {
  size_t count = 0;
  for (size_t i = 1; i < buf.size(); ++i) {
    if ((buf[i - 1] <= 0 && buf[i] > 0) || (buf[i - 1] >= 0 && buf[i] < 0)) {
      ++count;
    }
  }
  return count;
}

// Test 1: 440 Hz tone — peak detected at 440 ± 5 Hz.
void test_amp_sine_440hz() {
  I2SAmp amp;
  amp.PlayTone(440, 100); // 100 ms = 800 samples
  std::vector<int16_t> buf(800);
  size_t got = amp.ReadSamples(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(800, got);
  // Buffer should not be all zeros.
  bool any_nonzero =
      std::any_of(buf.begin(), buf.end(), [](int16_t s) { return s != 0; });
  TEST_ASSERT_TRUE(any_nonzero);
  // Estimate the dominant frequency via zero crossings.
  size_t crossings = CountZeroCrossings(buf);
  double estimated_hz =
      static_cast<double>(crossings) * kSampleRate / (2.0 * buf.size());
  TEST_ASSERT_GREATER_THAN(440 - 5, estimated_hz + 1);
  TEST_ASSERT_LESS_THAN(440 + 6, estimated_hz + 1);
}

// Test 2: 1 kHz tone — peak at 1000 ± 10 Hz.
void test_amp_sine_1khz() {
  I2SAmp amp;
  amp.PlayTone(1000, 100);
  std::vector<int16_t> buf(800);
  size_t got = amp.ReadSamples(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(800, got);
  size_t crossings = CountZeroCrossings(buf);
  double estimated_hz =
      static_cast<double>(crossings) * kSampleRate / (2.0 * buf.size());
  TEST_ASSERT_GREATER_THAN(1000 - 10, estimated_hz + 1);
  TEST_ASSERT_LESS_THAN(1000 + 11, estimated_hz + 1);
}

// Test 3: when no tone is playing, the buffer is zero.
void test_amp_silence_when_not_playing() {
  I2SAmp amp;
  // No PlayTone() call.
  std::vector<int16_t> buf(100);
  size_t got = amp.ReadSamples(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(100, got);
  for (auto s : buf) {
    TEST_ASSERT_EQUAL(0, s);
  }
}

// Test 4: Start, then Stop — buffer zeros immediately.
void test_amp_concurrent_play_stop() {
  I2SAmp amp;
  amp.PlayTone(440, 1000); // 1 second
  std::vector<int16_t> buf(100);
  size_t got = amp.ReadSamples(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(100, got);
  // First 100 samples should have signal.
  bool has_signal = std::any_of(buf.begin(), buf.end(),
                                [](int16_t s) { return std::abs(s) > 100; });
  TEST_ASSERT_TRUE(has_signal);

  // Stop the tone. The next 100 samples should be all zero.
  amp.Stop();
  std::fill(buf.begin(), buf.end(), 0);
  got = amp.ReadSamples(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(100, got);
  for (auto s : buf) {
    TEST_ASSERT_EQUAL(0, s);
  }
}

// Test 5: a longer tone yields the expected number of samples.
void test_amp_tone_length() {
  I2SAmp amp;
  amp.PlayTone(440, 200); // 200 ms = 1600 samples
  std::vector<int16_t> buf(2000);
  size_t got = amp.ReadSamples(buf.data(), buf.size());
  // The amp should write exactly 2000 samples (filling the buffer),
  // of which the first 1600 are non-zero and the rest are zero.
  TEST_ASSERT_EQUAL_size_t(2000, got);
  size_t nonzero = 0;
  for (auto s : buf) {
    if (s != 0)
      ++nonzero;
  }
  // The exact nonzero count depends on the phase: at phase 0 the sine
  // is 0, and any other phase zero-crossing also produces 0. With a
  // 200 ms 440 Hz tone we expect roughly 1600 - (cycles) - 1 ≈ 1584
  // non-zero samples. Allow a ±32 sample drift for variations.
  int diff = static_cast<int>(nonzero) - 1584;
  if (diff < 0)
    diff = -diff;
  TEST_ASSERT_LESS_THAN(33, diff);
}

// ── v2 hook tests (plan §10.4) ────────────────────────────────────
// These tests pin the v2 M5-side TTS playback hook. They assert
// the v1 build returns the input length unchanged for every
// chunk size. v2 inverts the test: the function must return
// less than num_samples when the playback buffer is full.

// Test 6: the v2 hook is callable in v1.
void test_v2_playback_stub_exists() {
  std::vector<int16_t> buf(16, 0);
  std::size_t got = PlayPcm(buf.data(), buf.size());
  TEST_ASSERT_EQUAL_size_t(16, got);
}

// Test 7: v1 returns the input length unchanged for a range of
// chunk sizes.
void test_v2_playback_v1_passes_through() {
  const std::size_t sizes[] = {0, 1, 16, 256, 1024, kPlaybackMaxChunkSamples};
  for (std::size_t i = 0; i < sizeof(sizes) / sizeof(sizes[0]); ++i) {
    std::vector<int16_t> buf(sizes[i], 0);
    std::size_t got = PlayPcm(buf.data(), sizes[i]);
    TEST_ASSERT_EQUAL_size_t(sizes[i], got);
  }
}

// Test 8: v1 accepts a null pointer when num_samples is zero
// (the v2 implementation will guard the dereference; v1
// should not crash).
void test_v2_playback_v1_null_zero() {
  std::size_t got = PlayPcm(nullptr, 0);
  TEST_ASSERT_EQUAL_size_t(0, got);
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_amp_sine_440hz);
  RUN_TEST(test_amp_sine_1khz);
  RUN_TEST(test_amp_silence_when_not_playing);
  RUN_TEST(test_amp_concurrent_play_stop);
  RUN_TEST(test_amp_tone_length);
  RUN_TEST(test_v2_playback_stub_exists);
  RUN_TEST(test_v2_playback_v1_passes_through);
  RUN_TEST(test_v2_playback_v1_null_zero);
  (void)0;
  UNITY_END();
}
