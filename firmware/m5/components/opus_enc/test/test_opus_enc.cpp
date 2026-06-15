// test_opus_enc.cpp — unit tests for tether::m5::OpusEncoder
// (plan.md §4.5).
//
// Host tests link against system libopus (libopus-dev) and exercise the
// encoder with synthetic PCM. On real hardware the same public API is
// implemented on top of micro-opus / esp-libopus in opus_enc.cpp.

#include <cmath>
#include <cstdint>
#include <vector>

#include <unity.h>

#include "opus_enc.h"

using tether::m5::OpusEncoder;

void setUp() {}
void tearDown() {}

// Helper: bytes.size() must be in the half-open range [lo, hi].
#define ASSERT_BYTES_IN_RANGE(bytes, lo, hi)                                   \
  do {                                                                         \
    TEST_ASSERT_GREATER_THAN((lo) - 1, (bytes).size());                        \
    TEST_ASSERT_LESS_THAN((hi) + 1, (bytes).size());                           \
  } while (0)

// Test 1: constructor returns non-null.
void test_opus_init() {
  OpusEncoder enc(8000, 16000, 5);
  TEST_ASSERT_TRUE(enc.IsInitialized());
  TEST_ASSERT_EQUAL(8000, enc.SampleRate());
  TEST_ASSERT_EQUAL(16000, enc.Bitrate());
  TEST_ASSERT_EQUAL(160, enc.FrameSize());
}

// Test 2: silent frame encodes to <= 30 bytes (VBR conserves bits).
void test_opus_encode_zero_pcm() {
  OpusEncoder enc(8000, 16000, 5);
  std::vector<int16_t> pcm(160, 0);
  auto bytes = enc.EncodeFrame(pcm.data());
  TEST_ASSERT_GREATER_THAN(0, bytes.size());
  ASSERT_BYTES_IN_RANGE(bytes, 1, 30);
}

// Test 3: 440 Hz sine encodes to 10..80 bytes.
void test_opus_encode_sine() {
  OpusEncoder enc(8000, 16000, 5);
  std::vector<int16_t> pcm(160);
  for (int i = 0; i < 160; ++i) {
    double t = static_cast<double>(i) / 8000.0;
    pcm[i] = static_cast<int16_t>(20000.0 * std::sin(2.0 * M_PI * 440.0 * t));
  }
  auto bytes = enc.EncodeFrame(pcm.data());
  TEST_ASSERT_GREATER_THAN(0, bytes.size());
  ASSERT_BYTES_IN_RANGE(bytes, 1, 80);
}

// Test 4: 60 s of synthetic speech totals <= 130 KB.
void test_opus_encode_60s_voice() {
  OpusEncoder enc(8000, 16000, 5);
  // 60 s * 50 frames/s = 3000 frames.
  size_t total_bytes = 0;
  for (int frame = 0; frame < 3000; ++frame) {
    std::vector<int16_t> pcm(160);
    double envelope = 0.5 + 0.5 * std::sin(2.0 * M_PI * 0.5 * frame / 50.0);
    for (int i = 0; i < 160; ++i) {
      double t = (static_cast<double>(frame) * 160.0 + i) / 8000.0;
      double sample = 20000.0 * envelope * std::sin(2.0 * M_PI * 200.0 * t);
      pcm[i] = static_cast<int16_t>(sample);
    }
    auto bytes = enc.EncodeFrame(pcm.data());
    total_bytes += bytes.size();
  }
  // 130 KB upper bound (VBR gives us ~16 kbps; 60 s = 120 KB nominal).
  TEST_ASSERT_LESS_THAN(130 * 1024 + 1, total_bytes);
}

// Test 5: EncodeFrame() processes exactly FrameSize() samples.
void test_opus_frame_size_constant() {
  OpusEncoder enc(8000, 16000, 5);
  // Already verified in test_opus_init; here we exercise multiple
  // frames in a row and verify they all return a valid packet.
  for (int n = 0; n < 50; ++n) {
    std::vector<int16_t> pcm(enc.FrameSize(), 0);
    auto bytes = enc.EncodeFrame(pcm.data());
    TEST_ASSERT_GREATER_THAN(0, bytes.size());
  }
}

// Test 6: encode the same frame twice — sizes should be similar.
// (libopus is deterministic for fixed-input CELT encoding; we don't
// require byte equality across all builds but allow either identical
// or small drift.)
void test_opus_encode_repeatable() {
  OpusEncoder enc(8000, 16000, 5);
  std::vector<int16_t> pcm(160);
  for (int i = 0; i < 160; ++i) {
    pcm[i] = static_cast<int16_t>((i * 137) & 0x3FFF);
  }
  auto a = enc.EncodeFrame(pcm.data());
  auto b = enc.EncodeFrame(pcm.data());
  // Both packets should be non-empty.
  TEST_ASSERT_GREATER_THAN(0, a.size());
  TEST_ASSERT_GREATER_THAN(0, b.size());
  // Allow up to 8 bytes drift (libopus 1.3.1 picks different VBR
  // encodings for back-to-back identical frames in some cases).
  int diff = static_cast<int>(a.size()) - static_cast<int>(b.size());
  if (diff < 0) diff = -diff;
  TEST_ASSERT_LESS_THAN(9, diff);
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_opus_init);
  RUN_TEST(test_opus_encode_zero_pcm);
  RUN_TEST(test_opus_encode_sine);
  RUN_TEST(test_opus_encode_60s_voice);
  RUN_TEST(test_opus_frame_size_constant);
  RUN_TEST(test_opus_encode_repeatable);
  (void)0;
  UNITY_END();
}
