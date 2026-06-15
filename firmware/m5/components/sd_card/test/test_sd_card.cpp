// test_sd_card.cpp — unit tests for tether::m5::SdCard (plan.md §4.3).
//
// Host-side tests use a per-test temporary directory as the "LittleFS"
// root. The SdCard class is constructed, mounted, exercised, and
// unmounted; the test runner cleans up the directory afterwards.

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>

#include <unity.h>

#include "sd_card.h"

using tether::m5::SdCard;

namespace {

// Pick a unique tmpfs path for this test run. The directory is created
// in setUp() and recursively removed in tearDown().
std::string g_test_root;

std::string MakeTestRoot() {
  static int counter = 0;
  char buf[256];
  std::snprintf(buf, sizeof buf, "/tmp/tether_sd_test_%d_%d",
                static_cast<int>(getpid()), counter++);
  return buf;
}

} // namespace

void setUp() {
  g_test_root = MakeTestRoot();
  std::filesystem::create_directories(g_test_root);
}

void tearDown() {
  if (!g_test_root.empty()) {
    std::error_code ec;
    std::filesystem::remove_all(g_test_root, ec);
    g_test_root.clear();
  }
}

// Test 1: Mount/Unmount round-trip.
void test_sd_mount_unmount() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  TEST_ASSERT_TRUE(card.IsMounted());
  TEST_ASSERT_EQUAL_STRING(g_test_root.c_str(), card.MountRoot());
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
  TEST_ASSERT_FALSE(card.IsMounted());
}

// Test 2: Mount is idempotent.
void test_sd_mount_idempotent() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  TEST_ASSERT_TRUE(card.IsMounted());
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 3: Open existing file returns non-null.
void test_sd_open_existing() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  // Create a file directly in the test root.
  std::string path = g_test_root + "/hello.txt";
  {
    std::ofstream f(path);
    f << "hi\n";
  }
  FILE *fp = card.Open("/hello.txt", "r");
  TEST_ASSERT_NOT_NULL(fp);
  if (fp) fclose(fp);
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 4: Open missing file returns NULL.
void test_sd_open_missing() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  FILE *fp = card.Open("/does_not_exist.txt", "r");
  TEST_ASSERT_NULL(fp);
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 5: Write 1 KB, read back, content equal.
void test_sd_write_read() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  std::vector<uint8_t> written(1024);
  for (size_t i = 0; i < written.size(); ++i) {
    written[i] = static_cast<uint8_t>(i & 0xFF);
  }
  {
    FILE *fp = card.Open("/data.bin", "wb");
    TEST_ASSERT_NOT_NULL(fp);
    TEST_ASSERT_EQUAL_size_t(written.size(),
                            fwrite(written.data(), 1, written.size(), fp));
    fclose(fp);
  }
  std::vector<uint8_t> read_back(1024);
  {
    FILE *fp = card.Open("/data.bin", "rb");
    TEST_ASSERT_NOT_NULL(fp);
    TEST_ASSERT_EQUAL_size_t(read_back.size(),
                            fread(read_back.data(), 1, read_back.size(), fp));
    fclose(fp);
  }
  TEST_ASSERT_EQUAL_MEMORY(written.data(), read_back.data(), written.size());
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 6: Remove existing file succeeds.
void test_sd_remove() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  {
    FILE *fp = card.Open("/remove_me.txt", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fclose(fp);
  }
  TEST_ASSERT_EQUAL(0, card.Remove("/remove_me.txt"));
  // Removing again should fail.
  TEST_ASSERT_EQUAL(-1, card.Remove("/remove_me.txt"));
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 7: Rename existing file succeeds.
void test_sd_rename() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  {
    FILE *fp = card.Open("/old.txt", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs("hello", fp);
    fclose(fp);
  }
  TEST_ASSERT_EQUAL(0, card.Rename("/old.txt", "/new.txt"));
  // Old should be gone.
  TEST_ASSERT_NULL(card.Open("/old.txt", "r"));
  // New should exist with the same content.
  FILE *fp = card.Open("/new.txt", "r");
  TEST_ASSERT_NOT_NULL(fp);
  char buf[16] = {0};
  TEST_ASSERT_NOT_NULL(fgets(buf, sizeof buf, fp));
  fclose(fp);
  TEST_ASSERT_EQUAL_STRING("hello", buf);
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

// Test 8: FreeBytes > 0 after mount.
void test_sd_freespace() {
  SdCard card;
  TEST_ASSERT_EQUAL(ESP_OK, card.Mount(g_test_root.c_str()));
  TEST_ASSERT_GREATER_THAN(0, card.FreeBytes());
  TEST_ASSERT_GREATER_THAN(0, card.TotalBytes());
  TEST_ASSERT_EQUAL(ESP_OK, card.Unmount());
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_sd_mount_unmount);
  RUN_TEST(test_sd_mount_idempotent);
  RUN_TEST(test_sd_open_existing);
  RUN_TEST(test_sd_open_missing);
  RUN_TEST(test_sd_write_read);
  RUN_TEST(test_sd_remove);
  RUN_TEST(test_sd_rename);
  RUN_TEST(test_sd_freespace);
  (void)0;
  UNITY_END();
}
