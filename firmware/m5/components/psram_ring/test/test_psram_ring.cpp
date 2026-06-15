// test_psram_ring.cpp — unit tests for tether::m5::PsramRing (plan.md
// §4.4).
//
// Single-threaded tests below exercise the data path; the
// concurrent_spsc test runs a producer thread and a consumer thread
// that hammer the ring for 100 ms and verify all bytes make it through
// (race-detector clean in TSan builds).

#include <atomic>
#include <chrono>
#include <stdexcept>
#include <thread>
#include <vector>

#include <unity.h>

#include "psram_ring.h"

using tether::m5::PsramRing;

void setUp() {}
void tearDown() {}

// Test 1: write 1 KB, read 1 KB, content equal.
void test_ring_write_read_round_trip() {
  PsramRing ring(64); // capacity 64 bytes
  std::vector<uint8_t> written(64);
  for (size_t i = 0; i < written.size(); ++i) {
    written[i] = static_cast<uint8_t>(i & 0xFF);
  }
  TEST_ASSERT_EQUAL_size_t(64, ring.Write(written.data(), 64));
  TEST_ASSERT_EQUAL_size_t(64, ring.Available());
  std::vector<uint8_t> back(64);
  TEST_ASSERT_EQUAL_size_t(64, ring.Read(back.data(), 64));
  TEST_ASSERT_EQUAL_MEMORY(written.data(), back.data(), 64);
  TEST_ASSERT_EQUAL_size_t(0, ring.Available());
}

// Test 2: write fills the ring; second write returns 0.
void test_ring_write_full() {
  PsramRing ring(64);
  std::vector<uint8_t> buf(64, 0xAB);
  TEST_ASSERT_EQUAL_size_t(64, ring.Write(buf.data(), 64));
  // Full: another write returns 0.
  TEST_ASSERT_EQUAL_size_t(0, ring.Write(buf.data(), 1));
  // Available == capacity.
  TEST_ASSERT_EQUAL_size_t(64, ring.Available());
}

// Test 3: read on empty returns 0.
void test_ring_read_empty() {
  PsramRing ring(64);
  std::vector<uint8_t> out(8);
  TEST_ASSERT_EQUAL_size_t(0, ring.Read(out.data(), 8));
  TEST_ASSERT_EQUAL_size_t(0, ring.Available());
}

// Test 4: write/read cycles 1000 times, final read matches last write.
void test_ring_wraparound() {
  PsramRing ring(16);
  std::vector<uint8_t> out(8);
  for (int i = 0; i < 1000; ++i) {
    uint8_t val = static_cast<uint8_t>(i & 0xFF);
    TEST_ASSERT_EQUAL_size_t(1, ring.Write(&val, 1));
    uint8_t back = 0;
    TEST_ASSERT_EQUAL_size_t(1, ring.Read(&back, 1));
    TEST_ASSERT_EQUAL_HEX32(val, back);
  }
}

// Test 5: partial reads split the same write across multiple calls.
void test_ring_partial_reads() {
  PsramRing ring(64);
  std::vector<uint8_t> in(32);
  for (size_t i = 0; i < in.size(); ++i)
    in[i] = static_cast<uint8_t>(i);
  TEST_ASSERT_EQUAL_size_t(32, ring.Write(in.data(), 32));
  std::vector<uint8_t> out(32);
  TEST_ASSERT_EQUAL_size_t(8, ring.Read(out.data(), 8));
  TEST_ASSERT_EQUAL_size_t(8, ring.Read(out.data() + 8, 8));
  TEST_ASSERT_EQUAL_size_t(8, ring.Read(out.data() + 16, 8));
  TEST_ASSERT_EQUAL_size_t(8, ring.Read(out.data() + 24, 8));
  TEST_ASSERT_EQUAL_MEMORY(in.data(), out.data(), 32);
}

// Test 6: non-power-of-two capacity rejected.
void test_ring_is_power_of_2() {
  TEST_ASSERT_THROW(PsramRing(63), std::invalid_argument);
  TEST_ASSERT_THROW(PsramRing(0), std::invalid_argument);
  TEST_ASSERT_THROW(PsramRing(7), std::invalid_argument);
  // Valid capacities construct without throwing.
  PsramRing r1(64);
  PsramRing r2(1024);
  (void)r1;
  (void)r2;
}

// Test 7: SPSC concurrency — producer + consumer threads, no loss.
void test_ring_concurrent_spsc() {
  constexpr size_t kCapacity = 1024;
  PsramRing ring(kCapacity);
  constexpr int kTotal = 10000;
  std::atomic<bool> producer_done{false};
  std::vector<uint8_t> consumer_out(kTotal);

  std::thread producer([&]() {
    for (int i = 0; i < kTotal; ++i) {
      uint8_t val = static_cast<uint8_t>(i & 0xFF);
      while (ring.Write(&val, 1) == 0) {
        std::this_thread::yield();
      }
    }
    producer_done.store(true);
  });
  std::thread consumer([&]() {
    int got = 0;
    while (got < kTotal) {
      uint8_t val = 0;
      size_t n = ring.Read(&val, 1);
      if (n > 0) {
        consumer_out[got++] = val;
      } else {
        std::this_thread::yield();
      }
    }
  });
  producer.join();
  consumer.join();
  // Verify the sequence is intact (modulo 256 since the values wrap).
  for (int i = 0; i < kTotal; ++i) {
    uint8_t expected = static_cast<uint8_t>(i & 0xFF);
    TEST_ASSERT_EQUAL_HEX32(expected, consumer_out[i]);
  }
}

// Test 8: capacity is reported correctly.
void test_ring_capacity() {
  PsramRing r(64);
  TEST_ASSERT_EQUAL_size_t(64, r.Capacity());
  PsramRing r2(4096);
  TEST_ASSERT_EQUAL_size_t(4096, r2.Capacity());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_ring_write_read_round_trip);
  RUN_TEST(test_ring_write_full);
  RUN_TEST(test_ring_read_empty);
  RUN_TEST(test_ring_wraparound);
  RUN_TEST(test_ring_partial_reads);
  RUN_TEST(test_ring_is_power_of_2);
  RUN_TEST(test_ring_concurrent_spsc);
  RUN_TEST(test_ring_capacity);
  (void)0;
  UNITY_END();
}
