// test_i2s_mic.cpp — minimal smoke test for tether::m5::I2SMic.
//
// The mic is a thin wrapper around the ESP-IDF I2S driver. On host
// we just verify that InjectForTest/ReadSamples is consistent (no
// real audio path is exercised here).
#include <unity.h>

#include "i2s_mic.h"

using tether::m5::I2SMic;

void setUp() {}
void tearDown() {}

// Test: a host test can inject samples and read them back.
void test_mic_inject_read() {
  I2SMic mic;
  int16_t in[4] = {1, 2, 3, 4};
  mic.InjectForTest(in, 4);
  int16_t out[4] = {0, 0, 0, 0};
  size_t got = mic.ReadSamples(out, 4);
  TEST_ASSERT_EQUAL_size_t(4, got);
  TEST_ASSERT_EQUAL(1, out[0]);
  TEST_ASSERT_EQUAL(4, out[3]);
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_mic_inject_read);
  (void)0;
  UNITY_END();
}
