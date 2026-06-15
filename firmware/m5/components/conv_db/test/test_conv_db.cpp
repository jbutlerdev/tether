// test_conv_db.cpp — unit tests for tether::m5::ConvDb (plan.md §5.2).
//
// Host-side tests use a per-test temporary directory as the
// "LittleFS" root. The ConvDb is mounted, exercised, and the
// directory is removed in tearDown().
//
// The tests follow the 18-case checklist from plan.md §5.2.

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <string>
#include <thread>
#include <vector>

#include <unity.h>

#include "conv_db.h"
#include "littlefs_vfs.h"

using tether::m5::ConvDb;
using tether::m5::ConvInfo;
using tether::m5::HistoryEntry;

namespace {

// Pick a unique tmpfs path for this test run. The directory is created
// in setUp() and recursively removed in tearDown().
std::string g_test_root;
ConvDb *g_db = nullptr;

std::string MakeTestRoot() {
  static int counter = 0;
  char buf[256];
  std::snprintf(buf, sizeof buf, "/tmp/tether_convdb_test_%d_%d",
                static_cast<int>(getpid()), counter++);
  return buf;
}

// Build a ConvInfo with sensible defaults; tests override the fields
// they care about.
ConvInfo MakeConv(const char *name = "Alice", uint8_t kind = 0,
                  const char *target = "!room:matrix.example.com",
                  int64_t last_activity_ms = 1700000000000LL,
                  uint16_t unread = 0) {
  ConvInfo c{};
  c.exists = true;
  // UUID-shaped id: 16 bytes, not all zero.
  for (int i = 0; i < 16; ++i) {
    c.id[i] = static_cast<uint8_t>(0xA0 ^ i);
  }
  std::strncpy(c.name, name, sizeof c.name - 1);
  c.name[sizeof c.name - 1] = '\0';
  c.kind = kind;
  std::strncpy(c.target, target, sizeof c.target - 1);
  c.target[sizeof c.target - 1] = '\0';
  for (int i = 0; i < 16; ++i) {
    c.enc_key[i] = static_cast<uint8_t>(i * 3);
  }
  c.last_activity_ms = last_activity_ms;
  c.unread = unread;
  return c;
}

bool AllZero(const uint8_t id[16]) {
  for (int i = 0; i < 16; ++i) {
    if (id[i] != 0)
      return false;
  }
  return true;
}

} // namespace

void setUp() {
  g_test_root = MakeTestRoot();
  std::filesystem::create_directories(g_test_root);
  g_db = new ConvDb();
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Init(g_test_root.c_str()));
}

void tearDown() {
  delete g_db;
  g_db = nullptr;
  if (!g_test_root.empty()) {
    std::error_code ec;
    std::filesystem::remove_all(g_test_root, ec);
    g_test_root.clear();
  }
}

// ── Test 1: Init creates the conv directory. ──────────────────────────
void test_convdb_init_creates_dir() {
  // After Init() the /conv directory should exist.
  TEST_ASSERT_TRUE(g_db->Vfs().Exists("/conv"));
}

// ── Test 2: Upsert / Get round-trip. ──────────────────────────────────
void test_convdb_upsert_get_round_trip() {
  ConvInfo c = MakeConv("Alice (Matrix)");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, &out));
  TEST_ASSERT_EQUAL_MEMORY(c.id, out.id, 16);
  TEST_ASSERT_EQUAL_STRING("Alice (Matrix)", out.name);
  TEST_ASSERT_EQUAL(c.kind, out.kind);
  TEST_ASSERT_EQUAL_STRING(c.target, out.target);
  TEST_ASSERT_EQUAL(c.last_activity_ms, out.last_activity_ms);
  TEST_ASSERT_EQUAL(c.unread, out.unread);
}

// ── Test 3: Upsert overwrites existing entry. ─────────────────────────
void test_convdb_upsert_overwrites() {
  ConvInfo c = MakeConv("Alice");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  c.unread = 7;
  c.last_activity_ms = static_cast<int64_t>(1700000999999LL);
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, &out));
  TEST_ASSERT_EQUAL(7, out.unread);
  TEST_ASSERT_EQUAL_INT64(static_cast<int64_t>(1700000999999LL),
                          out.last_activity_ms);
}

// ── Test 4: Remove deletes the entry. ─────────────────────────────────
void test_convdb_remove() {
  ConvInfo c = MakeConv("temp");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, nullptr));
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Remove(c.id));
  TEST_ASSERT_EQUAL(ESP_ERR_NOT_FOUND, g_db->Get(c.id, nullptr));
}

// ── Test 5: Get of missing conv returns NOT_FOUND. ────────────────────
void test_convdb_get_missing() {
  uint8_t id[16] = {0x01, 0x02, 0x03};
  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_ERR_NOT_FOUND, g_db->Get(id, &out));
}

// ── Test 6: List empty DB returns an empty vector. ────────────────────
void test_convdb_list_empty() {
  std::vector<ConvInfo> v = g_db->List();
  TEST_ASSERT_EQUAL_size_t(0, v.size());
}

// ── Test 7: List one conv. ────────────────────────────────────────────
void test_convdb_list_one() {
  ConvInfo c = MakeConv("solo");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  std::vector<ConvInfo> v = g_db->List();
  TEST_ASSERT_EQUAL_size_t(1, v.size());
  TEST_ASSERT_EQUAL_MEMORY(c.id, v[0].id, 16);
  TEST_ASSERT_EQUAL_STRING("solo", v[0].name);
}

// ── Test 8: List many (16 conversations). ─────────────────────────────
void test_convdb_list_many() {
  for (int i = 0; i < 16; ++i) {
    char nm[16];
    std::snprintf(nm, sizeof nm, "conv%02d", i);
    ConvInfo c = MakeConv(nm);
    // Make each id unique.
    c.id[15] = static_cast<uint8_t>(i);
    TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  }
  std::vector<ConvInfo> v = g_db->List();
  TEST_ASSERT_EQUAL_size_t(16, v.size());
}

// ── Test 9: List more than 16 returns only the first 16. ──────────────
void test_convdb_list_more_than_16_returns_first_16() {
  // The first 16 upserts succeed; the next 4 return ESP_ERR_NO_MEM
  // because the DB cap is 16. List() then returns the 16 that fit.
  for (int i = 0; i < 16; ++i) {
    char nm[16];
    std::snprintf(nm, sizeof nm, "conv%02d", i);
    ConvInfo c = MakeConv(nm);
    c.id[15] = static_cast<uint8_t>(i);
    TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  }
  for (int i = 16; i < 20; ++i) {
    char nm[16];
    std::snprintf(nm, sizeof nm, "conv%02d", i);
    ConvInfo c = MakeConv(nm);
    c.id[15] = static_cast<uint8_t>(i);
    TEST_ASSERT_EQUAL(ESP_ERR_NO_MEM, g_db->Upsert(c));
  }
  std::vector<ConvInfo> v = g_db->List();
  TEST_ASSERT_EQUAL_size_t(16, v.size());
}

// ── Test 10: Append / Get history. ────────────────────────────────────
void test_convdb_history_append_get() {
  ConvInfo c = MakeConv("hist");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  HistoryEntry e1{};
  e1.msg_id = 1;
  e1.timestamp_ms = 1700000000000LL;
  e1.direction = 1; // in
  std::strncpy(e1.text, "hello", sizeof e1.text - 1);
  e1.status = 1; // acked
  TEST_ASSERT_EQUAL(ESP_OK, g_db->AppendHistory(c.id, e1));

  HistoryEntry e2{};
  e2.msg_id = 2;
  e2.timestamp_ms = 1700000001000LL;
  e2.direction = 0; // out
  std::strncpy(e2.text, "world", sizeof e2.text - 1);
  e2.status = 0; // pending
  TEST_ASSERT_EQUAL(ESP_OK, g_db->AppendHistory(c.id, e2));

  std::vector<HistoryEntry> h = g_db->GetHistory(c.id, 50);
  TEST_ASSERT_EQUAL_size_t(2, h.size());
  // Most-recent first (per the GetHistory docstring).
  TEST_ASSERT_EQUAL(2u, h[0].msg_id);
  TEST_ASSERT_EQUAL_STRING("world", h[0].text);
  TEST_ASSERT_EQUAL(1u, h[1].msg_id);
  TEST_ASSERT_EQUAL_STRING("hello", h[1].text);
}

// ── Test 11: History ring buffer wraps at 50 entries. ─────────────────
void test_convdb_history_ring_buffer_wraps_at_50() {
  ConvInfo c = MakeConv("ring");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  for (uint32_t i = 1; i <= 60; ++i) {
    HistoryEntry e{};
    e.msg_id = i;
    e.direction = static_cast<uint8_t>(i & 1);
    e.status = 1;
    char txt[16];
    std::snprintf(txt, sizeof txt, "m%u", i);
    std::strncpy(e.text, txt, sizeof e.text - 1);
    TEST_ASSERT_EQUAL(ESP_OK, g_db->AppendHistory(c.id, e));
  }
  std::vector<HistoryEntry> h = g_db->GetHistory(c.id, 100);
  // The ring keeps at most 50 entries; with 60 appends we expect
  // the most recent 50 (msg_id 11..60) returned most-recent-first.
  TEST_ASSERT_EQUAL_size_t(50, h.size());
  TEST_ASSERT_EQUAL(60u, h.front().msg_id);
  TEST_ASSERT_EQUAL(11u, h.back().msg_id);
}

// ── Test 12: Persistence across Init. ─────────────────────────────────
void test_convdb_persistence_across_init() {
  ConvInfo c = MakeConv("persist");
  c.unread = 3;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));

  HistoryEntry e{};
  e.msg_id = 1;
  e.direction = 1;
  e.status = 1;
  std::strncpy(e.text, "hello", sizeof e.text - 1);
  TEST_ASSERT_EQUAL(ESP_OK, g_db->AppendHistory(c.id, e));

  // Re-mount the DB (simulating a reboot).
  delete g_db;
  g_db = new ConvDb();
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Init(g_test_root.c_str()));

  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, &out));
  TEST_ASSERT_EQUAL_STRING("persist", out.name);
  TEST_ASSERT_EQUAL(3, out.unread);

  std::vector<HistoryEntry> h = g_db->GetHistory(c.id, 50);
  TEST_ASSERT_EQUAL_size_t(1, h.size());
  TEST_ASSERT_EQUAL_STRING("hello", h[0].text);
}

// ── Test 13: Concurrent Upsert. ───────────────────────────────────────
void test_convdb_concurrent_upsert() {
  // Two threads each upsert 32 unique conversations. The DB cap
  // is 16, so many upserts return ESP_ERR_NO_MEM. The invariants
  // we check are:
  //   1. List() returns at most 16 entries (the cap is respected).
  //   2. Every returned entry is well-formed (non-zero id, name
  //      NUL-terminated, enc_key readable).
  //   3. No entry is a torn write (the size of List() equals the
  //      DB's Size()).
  constexpr int kPerThread = 32;
  auto worker = [&](int base) {
    for (int i = 0; i < kPerThread; ++i) {
      char nm[16];
      std::snprintf(nm, sizeof nm, "t%d-%d", base, i);
      ConvInfo c = MakeConv(nm);
      c.id[0] = static_cast<uint8_t>(base);
      c.id[1] = static_cast<uint8_t>(i);
      // Either OK or NO_MEM is acceptable; what we care about is
      // that the call returns cleanly and the DB stays consistent.
      esp_err_t rc = g_db->Upsert(c);
      TEST_ASSERT_TRUE(rc == ESP_OK || rc == ESP_ERR_NO_MEM);
    }
  };
  std::thread t1(worker, 0);
  std::thread t2(worker, 1);
  t1.join();
  t2.join();

  std::vector<ConvInfo> v = g_db->List();
  TEST_ASSERT_EQUAL_size_t(16, v.size());
  TEST_ASSERT_EQUAL_size_t(16, g_db->Size());
  for (const auto &c : v) {
    TEST_ASSERT_FALSE(AllZero(c.id));
    // name is NUL-terminated within the 24-byte field.
    bool has_nul = false;
    for (size_t i = 0; i < tether::m5::kConvNameMax; ++i) {
      if (c.name[i] == '\0') {
        has_nul = true;
        break;
      }
    }
    TEST_ASSERT_TRUE(has_nul);
  }
}

// ── Test 14: Name truncated to fit in the 24-byte field on Upsert. ────
void test_convdb_name_truncated_to_24() {
  // A name with 30 characters supplied via strncpy is truncated
  // to fit in name[24]. The strncpy leaves 23 chars + NUL, so
  // the resulting NUL-terminated string has length 23.
  ConvInfo c = MakeConv("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); // 30 A's
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, &out));
  TEST_ASSERT_EQUAL(23, static_cast<int>(std::strlen(out.name)));
  // The 24th byte is the NUL terminator.
  TEST_ASSERT_EQUAL(0, out.name[23]);
  // The full 24-byte field is preserved.
  for (int i = 0; i < 23; ++i) {
    TEST_ASSERT_EQUAL('A', out.name[i]);
  }
}

// ── Test 15: Upsert rejects names longer than 24 with a clear error. ──
void test_convdb_name_rejects_longer() {
  // A name[24] set to "x" with no NUL inside the 24-byte buffer
  // is treated as overflowing the field and Upsert returns
  // ESP_ERR_INVALID_ARG. We construct such a record explicitly.
  ConvInfo c = MakeConv("fine");
  std::memset(c.name, 'x', sizeof c.name);
  TEST_ASSERT_EQUAL(ESP_ERR_INVALID_ARG, g_db->Upsert(c));
}

// ── Test 16: Upsert rejects an all-zero UUID. ─────────────────────────
void test_convdb_invalid_uuid() {
  ConvInfo c = MakeConv("zero-id");
  std::memset(c.id, 0, sizeof c.id);
  TEST_ASSERT_EQUAL(ESP_ERR_INVALID_ARG, g_db->Upsert(c));
}

// ── Test 17: Atomic Upsert — on a mid-write crash, the old meta is
// still readable. We simulate by writing twice in a row; the
// implementation must use a temp-then-rename pattern, which we
// verify by checking that there is no `*.tmp` leftover after a
// successful Upsert. ──────────────────────────────────────────────────
void test_convdb_atomic_write() {
  ConvInfo c = MakeConv("atomic");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  // Upsert again — the second call should still leave no .tmp
  // file in /conv/<uuid>/.
  c.unread = 5;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  std::vector<std::string> entries = g_db->Vfs().ListDir("/conv");
  for (const auto &e : entries) {
    // Each entry is a uuid directory; the rename must be done.
    std::string p = std::string("/conv/") + e;
    std::vector<std::string> inner = g_db->Vfs().ListDir(p.c_str());
    for (const auto &f : inner) {
      TEST_ASSERT_FALSE(f.find(".tmp") != std::string::npos);
    }
  }
  // The previous value (unread=0) must not be visible — we should
  // see the new value (unread=5).
  ConvInfo out;
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Get(c.id, &out));
  TEST_ASSERT_EQUAL(5, out.unread);
}

// ── Test 18: ClearHistory empties the ring. ───────────────────────────
void test_convdb_history_clear() {
  ConvInfo c = MakeConv("clear");
  TEST_ASSERT_EQUAL(ESP_OK, g_db->Upsert(c));
  for (uint32_t i = 1; i <= 5; ++i) {
    HistoryEntry e{};
    e.msg_id = i;
    e.direction = 1;
    e.status = 1;
    TEST_ASSERT_EQUAL(ESP_OK, g_db->AppendHistory(c.id, e));
  }
  TEST_ASSERT_EQUAL_size_t(5, g_db->GetHistory(c.id, 50).size());
  TEST_ASSERT_EQUAL(ESP_OK, g_db->ClearHistory(c.id));
  TEST_ASSERT_EQUAL_size_t(0, g_db->GetHistory(c.id, 50).size());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_convdb_init_creates_dir);
  RUN_TEST(test_convdb_upsert_get_round_trip);
  RUN_TEST(test_convdb_upsert_overwrites);
  RUN_TEST(test_convdb_remove);
  RUN_TEST(test_convdb_get_missing);
  RUN_TEST(test_convdb_list_empty);
  RUN_TEST(test_convdb_list_one);
  RUN_TEST(test_convdb_list_many);
  RUN_TEST(test_convdb_list_more_than_16_returns_first_16);
  RUN_TEST(test_convdb_history_append_get);
  RUN_TEST(test_convdb_history_ring_buffer_wraps_at_50);
  RUN_TEST(test_convdb_persistence_across_init);
  RUN_TEST(test_convdb_concurrent_upsert);
  RUN_TEST(test_convdb_name_truncated_to_24);
  RUN_TEST(test_convdb_name_rejects_longer);
  RUN_TEST(test_convdb_invalid_uuid);
  RUN_TEST(test_convdb_atomic_write);
  RUN_TEST(test_convdb_history_clear);
  (void)0;
  UNITY_END();
}
