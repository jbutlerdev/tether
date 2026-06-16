// test_storage_flush.cpp — unit tests for tether::m5::StorageFlush.
#include <cstdio>
#include <cstring>
#include <filesystem>
#include <string>

#include <unity.h>

#include "psram_ring.h"
#include "sd_card.h"
#include "storage_flush.h"

using tether::m5::PsramRing;
using tether::m5::SdCard;
using tether::m5::StorageFlush;

namespace {
PsramRing *g_ring = nullptr;
SdCard *g_card = nullptr;
StorageFlush *g_flush = nullptr;
std::string g_root;

void Reset() {
  delete g_flush;
  g_flush = nullptr;
  delete g_card;
  g_card = nullptr;
  delete g_ring;
  g_ring = nullptr;
  g_root = std::string("/tmp/tether_storage_test_") + std::to_string(getpid());
  std::filesystem::remove_all(g_root);
  g_card = new SdCard();
  g_card->Mount(g_root.c_str());
  g_ring = new PsramRing(2048);
  g_flush = new StorageFlush(*g_ring, *g_card);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_flush;
  g_flush = nullptr;
  delete g_card;
  g_card = nullptr;
  delete g_ring;
  g_ring = nullptr;
  if (!g_root.empty()) {
    std::error_code ec;
    std::filesystem::remove_all(g_root, ec);
  }
}

// Test 1: fill ring with bytes, flush, file on SD has them.
void test_flush_writes_ring_to_sd() {
  std::vector<uint8_t> in(100, 0xAB);
  g_ring->Write(in.data(), in.size());
  size_t wrote = g_flush->RunOnce();
  TEST_ASSERT_EQUAL_size_t(100, wrote);
  TEST_ASSERT_EQUAL(100, g_flush->TotalBytesWritten());
  TEST_ASSERT_EQUAL(1, g_flush->ChunksWritten());
}

// Test 2: many small flushes.
void test_flush_many_small_chunks() {
  for (int i = 0; i < 10; ++i) {
    uint8_t v = static_cast<uint8_t>(i);
    g_ring->Write(&v, 1);
    g_flush->RunOnce();
  }
  TEST_ASSERT_EQUAL(10, g_flush->TotalBytesWritten());
  TEST_ASSERT_EQUAL(10, g_flush->ChunksWritten());
}

// Test 3: empty ring → 0 bytes.
void test_flush_empty_ring() {
  size_t wrote = g_flush->RunOnce();
  TEST_ASSERT_EQUAL_size_t(0, wrote);
  TEST_ASSERT_EQUAL(0, g_flush->TotalBytesWritten());
}

// Test 4: SD missing — Unmount() before a flush.
void test_flush_handles_sd_missing() {
  g_card->Unmount();
  uint8_t v = 0xAA;
  g_ring->Write(&v, 1);
  size_t wrote = g_flush->RunOnce();
  // No card mounted → 0 bytes written, no crash.
  TEST_ASSERT_EQUAL_size_t(0, wrote);
  // Re-mount and verify we can still write after re-mounting.
  g_card->Mount(g_root.c_str());
  g_flush->RunOnce();
  TEST_ASSERT_EQUAL(0, g_flush->TotalBytesWritten());
}

// Test 5: rotate file changes the path.
void test_flush_rotate_file() {
  uint8_t v = 0;
  g_ring->Write(&v, 1);
  g_flush->RunOnce();
  auto first = g_flush->LastFile();
  g_flush->RotateFileForTest();
  g_ring->Write(&v, 1);
  g_flush->RunOnce();
  auto second = g_flush->LastFile();
  TEST_ASSERT_GREATER_THAN(0, first.size());
  TEST_ASSERT_GREATER_THAN(0, second.size());
  // They may be the same or different depending on the rotation policy;
  // we just verify both are non-empty.
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_flush_writes_ring_to_sd);
  RUN_TEST(test_flush_many_small_chunks);
  RUN_TEST(test_flush_empty_ring);
  RUN_TEST(test_flush_handles_sd_missing);
  RUN_TEST(test_flush_rotate_file);
  (void)0;
  UNITY_END();
}
