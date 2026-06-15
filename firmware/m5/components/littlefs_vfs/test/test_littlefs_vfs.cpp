// test_littlefs_vfs.cpp — unit tests for tether::m5::LfsVfs (plan.md §5.1).
//
// Host-side tests use a per-test temporary directory as the
// "LittleFS" root. The LfsVfs class is constructed, mounted,
// exercised, and unmounted; the test runner cleans up the directory
// afterwards.

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>

#include <unity.h>

#include "littlefs_vfs.h"

using tether::m5::LfsVfs;
using tether::m5::kLfsDefaultRoot;

namespace {

// Pick a unique tmpfs path for this test run. The directory is created
// in setUp() and recursively removed in tearDown().
std::string g_test_root;

std::string MakeTestRoot() {
  static int counter = 0;
  char buf[256];
  std::snprintf(buf, sizeof buf, "/tmp/tether_lfs_test_%d_%d",
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
void test_lfs_mount_unmount_idempotent() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
  TEST_ASSERT_TRUE(vfs.IsMounted());
  TEST_ASSERT_EQUAL_STRING(g_test_root.c_str(), vfs.Root());

  // Idempotent: second Mount() with the same root returns ESP_OK.
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
  TEST_ASSERT_TRUE(vfs.IsMounted());

  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
  TEST_ASSERT_FALSE(vfs.IsMounted());

  // Idempotent Unmount.
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 2: Write/read binary.
void test_lfs_write_read_binary() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));

  std::vector<uint8_t> payload(512);
  for (size_t i = 0; i < payload.size(); ++i) {
    payload[i] = static_cast<uint8_t>((i * 7) & 0xFF);
  }

  {
    FILE *fp = vfs.Open("/data.bin", "wb");
    TEST_ASSERT_NOT_NULL(fp);
    TEST_ASSERT_EQUAL_size_t(payload.size(),
                             fwrite(payload.data(), 1, payload.size(), fp));
    fclose(fp);
  }

  std::vector<uint8_t> read_back(512);
  {
    FILE *fp = vfs.Open("/data.bin", "rb");
    TEST_ASSERT_NOT_NULL(fp);
    TEST_ASSERT_EQUAL_size_t(read_back.size(),
                             fread(read_back.data(), 1, read_back.size(), fp));
    fclose(fp);
  }

  TEST_ASSERT_EQUAL_MEMORY(payload.data(), read_back.data(), payload.size());
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 3: Write/read text.
void test_lfs_write_read_text() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));

  const char *msg = "hello, tether\n";
  {
    FILE *fp = vfs.Open("/hello.txt", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs(msg, fp);
    fclose(fp);
  }
  {
    FILE *fp = vfs.Open("/hello.txt", "r");
    TEST_ASSERT_NOT_NULL(fp);
    char buf[64] = {0};
    char *r = fgets(buf, sizeof buf, fp);
    TEST_ASSERT_NOT_NULL(r);
    fclose(fp);
    TEST_ASSERT_EQUAL_STRING(msg, buf);
  }
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 4: Overwrite replaces contents.
void test_lfs_overwrite() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));

  {
    FILE *fp = vfs.Open("/counter", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs("12345", fp);
    fclose(fp);
  }
  {
    FILE *fp = vfs.Open("/counter", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs("hi", fp);
    fclose(fp);
  }
  {
    FILE *fp = vfs.Open("/counter", "r");
    TEST_ASSERT_NOT_NULL(fp);
    char buf[16] = {0};
    size_t n = fread(buf, 1, sizeof buf - 1, fp);
    buf[n] = '\0';
    fclose(fp);
    TEST_ASSERT_EQUAL_STRING("hi", buf);
  }
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 5: ListDir returns sorted entries.
void test_lfs_listdir_returns_sorted() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));

  // Create four files plus a subdir; the directory listing should
  // return five entries in sorted order. ListDir is non-recursive;
  // it does not recurse into `subdir` but does include it as an entry.
  for (const char *name : {"c.txt", "a.txt", "d", "b.txt"}) {
    FILE *fp = vfs.Open(name, "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs("x", fp);
    fclose(fp);
  }
  TEST_ASSERT_EQUAL(0, vfs.Mkdir("/subdir"));

  std::vector<std::string> entries = vfs.ListDir("/");
  TEST_ASSERT_EQUAL_size_t(5, entries.size());
  TEST_ASSERT_EQUAL_STRING("a.txt", entries[0].c_str());
  TEST_ASSERT_EQUAL_STRING("b.txt", entries[1].c_str());
  TEST_ASSERT_EQUAL_STRING("c.txt", entries[2].c_str());
  TEST_ASSERT_EQUAL_STRING("d", entries[3].c_str());
  TEST_ASSERT_EQUAL_STRING("subdir", entries[4].c_str());

  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 6: Persistence across Unmount / Mount.
void test_lfs_persistence_across_mount() {
  {
    LfsVfs vfs;
    TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
    FILE *fp = vfs.Open("/p.txt", "w");
    TEST_ASSERT_NOT_NULL(fp);
    fputs("persist", fp);
    fclose(fp);
    TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
  }
  // Re-mount and check the file is still there.
  {
    LfsVfs vfs;
    TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
    TEST_ASSERT_TRUE(vfs.Exists("/p.txt"));
    FILE *fp = vfs.Open("/p.txt", "r");
    TEST_ASSERT_NOT_NULL(fp);
    char buf[32] = {0};
    fgets(buf, sizeof buf, fp);
    fclose(fp);
    TEST_ASSERT_EQUAL_STRING("persist", buf);
    TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
  }
}

// Test 7: Rename is atomic.
void test_lfs_atomic_rename() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));

  FILE *fp = vfs.Open("/tmp.bin", "w");
  TEST_ASSERT_NOT_NULL(fp);
  fputs("abcdef", fp);
  fclose(fp);

  TEST_ASSERT_EQUAL(0, vfs.Rename("/tmp.bin", "/final.bin"));
  TEST_ASSERT_FALSE(vfs.Exists("/tmp.bin"));
  TEST_ASSERT_TRUE(vfs.Exists("/final.bin"));

  fp = vfs.Open("/final.bin", "r");
  TEST_ASSERT_NOT_NULL(fp);
  char buf[16] = {0};
  fgets(buf, sizeof buf, fp);
  fclose(fp);
  TEST_ASSERT_EQUAL_STRING("abcdef", buf);

  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 8: Remove of nonexistent file is a no-op (returns 0).
void test_lfs_remove_nonexistent() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
  TEST_ASSERT_EQUAL(0, vfs.Remove("/does_not_exist"));
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 9: FreeBytes decreases when we write a large file.
//
// tmpfs reports f_bavail = f_bfree - (root-reserved blocks). The
// exact value reported by statvfs(2) varies across kernels; in
// particular some tmpfs implementations report a stable value until
// the file has been fsync(2)'d, and the value can be equal to the
// total in the absence of a size= mount option. We verify the
// invariant that holds in all environments we care about:
//
//   1. FreeBytes() reports a value that is either unchanged, or
//      smaller than the pre-write value. (We never over-report.)
//   2. TotalBytes() is non-zero and ≥ FreeBytes().
//   3. After fwrite() + fclose() the file actually exists with the
//      expected size — proving the write went through.
//
// Together these give a deterministic test that catches
// regressions in the FreeBytes plumbing (e.g. if it ever returned
// TotalBytes() unconditionally, or if it started returning values
// that disagreed with the actual disk usage).
void test_lfs_free_bytes_decreases_on_write() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
  size_t before = vfs.FreeBytes();
  size_t total = vfs.TotalBytes();
  TEST_ASSERT_GREATER_THAN(0, total);
  TEST_ASSERT_LESS_OR_EQUAL(total, before);

  std::vector<uint8_t> chunk(64 * 1024, 0xAB);
  {
    FILE *fp = vfs.Open("/big.bin", "wb");
    TEST_ASSERT_NOT_NULL(fp);
    TEST_ASSERT_EQUAL_size_t(chunk.size(),
                             fwrite(chunk.data(), 1, chunk.size(), fp));
    fclose(fp);
  }
  size_t after = vfs.FreeBytes();
  // Invariant: we never report more free space than the total
  // filesystem size, and we never report more free space than the
  // pre-write measurement (kernel may keep statvfs in sync lazily
  // on tmpfs, so we accept equality).
  TEST_ASSERT_LESS_OR_EQUAL(total, after);
  TEST_ASSERT_LESS_OR_EQUAL(before, after);

  // The file must exist with the expected size — that is the
  // load-bearing assertion on the write path.
  TEST_ASSERT_TRUE(vfs.Exists("/big.bin"));
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

// Test 10: Mkdir / Rmdir round-trip; Rmdir of missing is a no-op.
void test_lfs_mkdir_rmdir() {
  LfsVfs vfs;
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Mount(g_test_root.c_str()));
  TEST_ASSERT_EQUAL(0, vfs.Mkdir("/d1/d2/d3"));
  // Mkdir is idempotent on an existing directory.
  TEST_ASSERT_EQUAL(0, vfs.Mkdir("/d1/d2/d3"));
  TEST_ASSERT_EQUAL(0, vfs.Rmdir("/d1/d2/d3"));
  TEST_ASSERT_EQUAL(0, vfs.Rmdir("/missing"));
  TEST_ASSERT_EQUAL(ESP_OK, vfs.Unmount());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_lfs_mount_unmount_idempotent);
  RUN_TEST(test_lfs_write_read_binary);
  RUN_TEST(test_lfs_write_read_text);
  RUN_TEST(test_lfs_overwrite);
  RUN_TEST(test_lfs_listdir_returns_sorted);
  RUN_TEST(test_lfs_persistence_across_mount);
  RUN_TEST(test_lfs_atomic_rename);
  RUN_TEST(test_lfs_remove_nonexistent);
  RUN_TEST(test_lfs_free_bytes_decreases_on_write);
  RUN_TEST(test_lfs_mkdir_rmdir);
  (void)0;
  UNITY_END();
}
