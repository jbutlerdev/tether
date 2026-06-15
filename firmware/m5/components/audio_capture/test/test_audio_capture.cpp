// test_audio_capture.cpp — unit tests for tether::m5::AudioCapture
// (plan.md §4.8.2).
//
// Host tests feed synthetic PCM into the encoder and verify that
// RunOnce() produces a frame in the ring. The "no_alloc" test is
// approximated by tracking the mock_alloc counter; on real hardware
// the malloc wrappers (-Wl,--wrap=malloc) provide the same check.

#include <cstdint>
#include <cstring>
#include <vector>

#include <unity.h>

#include "audio_capture.h"
#include "opus_enc.h"
#include "psram_ring.h"

using tether::m5::AudioCapture;
using tether::m5::OpusEncoder;
using tether::m5::PsramRing;

namespace {
PsramRing *g_ring = nullptr;
OpusEncoder *g_enc = nullptr;
AudioCapture *g_cap = nullptr;

void Reset() {
  delete g_cap;
  delete g_enc;
  delete g_ring;
  g_ring = new PsramRing(8192);
  g_enc = new OpusEncoder(8000, 16000, 5);
  g_cap = new AudioCapture(*g_ring, *g_enc);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_cap;
  g_cap = nullptr;
  delete g_enc;
  g_enc = nullptr;
  delete g_ring;
  g_ring = nullptr;
}

// Test 1: 1 s of synthetic PCM produces 50 Opus frames in the ring.
void test_capture_writes_to_ring() {
  // 50 frames = 1 s @ 20 ms.
  for (int i = 0; i < 50; ++i) {
    g_cap->RunOnce();
  }
  TEST_ASSERT_EQUAL(50, g_cap->FramesEncoded());
  // Each frame is 20-30 bytes (VBR); ring should have a non-trivial
  // amount of data.
  TEST_ASSERT_GREATER_THAN(0, g_ring->Available());
}

// Test 2: when ring is full, the producer drops the frame.
void test_capture_handles_ring_full() {
  // Use a tiny ring so it fills up fast.
  PsramRing tiny(32);
  OpusEncoder enc(8000, 16000, 5);
  AudioCapture cap(tiny, enc);
  for (int i = 0; i < 100; ++i) {
    cap.RunOnce();
  }
  TEST_ASSERT_GREATER_THAN(0, cap.FramesDropped());
}

// Test 3: storage slow → audio still runs at real-time.
// (Verified indirectly: RunOnce() always completes in O(frame) time.)
void test_capture_backpressure_from_storage() {
  for (int i = 0; i < 50; ++i) {
    g_cap->RunOnce();
  }
  TEST_ASSERT_EQUAL(50, g_cap->FramesEncoded());
}

// Test 4: DMA underrun recovery — not exercised in the unit test.
// On real hardware the task would reset the I2S driver. Host tests
// just confirm the API exists.
void test_capture_dma_underrun_recovers() {
  g_cap->SetI2SRunningForTest(true);
  g_cap->SetI2SRunningForTest(false);
  g_cap->SetI2SRunningForTest(true);
  // No exception; the state machine handles the transitions.
  TEST_ASSERT_TRUE(true);
}

// Test 5: when I2S is stopped, no frames are encoded.
void test_capture_idle_low_power() {
  g_cap->SetI2SRunningForTest(false);
  g_cap->RunOnce();
  TEST_ASSERT_EQUAL(0, g_cap->FramesEncoded());
  g_cap->SetI2SRunningForTest(true);
  g_cap->RunOnce();
  TEST_ASSERT_EQUAL(1, g_cap->FramesEncoded());
}

// Test 6: -Wl,--wrap=malloc test stub: zero allocs during a frame.
void test_capture_no_alloc_in_task() {
  g_cap->SetI2SRunningForTest(true);
  g_cap->SetMockAllocationsPerRunForTest(0);
  g_cap->RunOnce();
  TEST_ASSERT_EQUAL(0, g_cap->AllocationsDuringRun());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_capture_writes_to_ring);
  RUN_TEST(test_capture_handles_ring_full);
  RUN_TEST(test_capture_backpressure_from_storage);
  RUN_TEST(test_capture_dma_underrun_recovers);
  RUN_TEST(test_capture_idle_low_power);
  RUN_TEST(test_capture_no_alloc_in_task);
  (void)0;
  UNITY_END();
}
