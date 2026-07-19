// test_opus_dec.cpp — unit tests for tether::m5::OpusDecoder.
//
// Host tests encode a synthetic PCM frame with OpusEncoder, then
// decode it with OpusDecoder and verify the round-trip produces the
// correct number of samples. The host build links system libopus.

#include <cmath>
#include <cstdint>
#include <vector>

#include <unity.h>

#include "opus_dec.h"
#include "opus_enc.h"

using tether::m5::OpusDecoder;
using tether::m5::OpusEncoder;

namespace {
OpusEncoder *g_enc = nullptr;
OpusDecoder *g_dec = nullptr;

void Reset() {
  delete g_dec;
  delete g_enc;
  g_enc = new OpusEncoder(8000, 16000, 5);
  g_dec = new OpusDecoder(8000, 1);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_dec;
  g_dec = nullptr;
  delete g_enc;
  g_enc = nullptr;
}

// Test 1: decode a single encoded frame → 160 samples.
void test_dec_round_trip_single_frame() {
  TEST_ASSERT_TRUE(g_enc->IsInitialized());
  TEST_ASSERT_TRUE(g_dec->IsInitialized());

  // Encode a sine-wave frame.
  int16_t pcm[160];
  for (int i = 0; i < 160; ++i) {
    double t = static_cast<double>(i) / 8000.0;
    pcm[i] = static_cast<int16_t>(8000.0 * std::sin(2.0 * M_PI * 440.0 * t));
  }
  auto encoded = g_enc->EncodeFrame(pcm);
  TEST_ASSERT_GREATER_THAN(0, encoded.size());

  auto decoded = g_dec->DecodeFrame(encoded.data(), encoded.size());
  TEST_ASSERT_EQUAL(160, decoded.size());
}

// Test 2: decode empty input → empty output.
void test_dec_empty_input() {
  auto decoded = g_dec->DecodeFrame(nullptr, 0);
  TEST_ASSERT_EQUAL(0, decoded.size());
}

// Test 3: frame size matches encoder.
void test_dec_frame_size() {
  TEST_ASSERT_EQUAL(160, g_dec->FrameSize());
  TEST_ASSERT_EQUAL(8000, g_dec->SampleRate());
}

// Test 4: multiple frames decode independently.
void test_dec_multiple_frames() {
  int16_t pcm[160];
  for (int i = 0; i < 160; ++i) {
    pcm[i] = static_cast<int16_t>(i * 100);
  }
  for (int frame = 0; frame < 5; ++frame) {
    auto encoded = g_enc->EncodeFrame(pcm);
    TEST_ASSERT_GREATER_THAN(0, encoded.size());
    auto decoded = g_dec->DecodeFrame(encoded.data(), encoded.size());
    TEST_ASSERT_EQUAL(160, decoded.size());
  }
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_dec_round_trip_single_frame);
  RUN_TEST(test_dec_empty_input);
  RUN_TEST(test_dec_frame_size);
  RUN_TEST(test_dec_multiple_frames);
  UNITY_END();
}
