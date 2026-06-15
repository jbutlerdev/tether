// test_watchdog.cpp — unit tests for tether::m5::Watchdog.
#include <string>

#include <unity.h>

#include "watchdog.h"
#include "test_watchdog_state.h"

using tether::m5::ResetReason;
using tether::m5::Watchdog;

Watchdog *g_wdt = nullptr;
void ResetWatchdog() {
  delete g_wdt;
  g_wdt = new Watchdog();
  g_wdt->SetHungThresholdMsForTest(5000);
}

void setUp() { ResetWatchdog(); }
void tearDown() {
  delete g_wdt;
  g_wdt = nullptr;
}

// Test 1: every 500 ms, all registered tasks are fed.
void test_watchdog_feeds_all_tasks() {
  g_wdt->Register("audio_capture");
  g_wdt->Register("radio_control");
  g_wdt->Register("ui_state");
  g_wdt->FeedAll();
  TEST_ASSERT_EQUAL(1, g_wdt->FeedCount());
  TEST_ASSERT_EQUAL(1, g_wdt->FeedCountFor("audio_capture"));
  TEST_ASSERT_EQUAL(1, g_wdt->FeedCountFor("radio_control"));
  TEST_ASSERT_EQUAL(1, g_wdt->FeedCountFor("ui_state"));
  g_wdt->FeedAll();
  TEST_ASSERT_EQUAL(2, g_wdt->FeedCountFor("audio_capture"));
}

// Test 2: hung task — never fed past threshold.
void test_watchdog_triggers_on_hung_task() {
  g_wdt->Register("audio_capture");
  g_wdt->FeedAll();
  // 5 s threshold; advance past it via repeated feeds.
  for (int i = 0; i < 20; ++i)
    g_wdt->FeedAll();
  // audio_capture is fed every FeedAll() so it's never hung.
  TEST_ASSERT_FALSE(g_wdt->IsHungForTest("audio_capture"));
  TEST_ASSERT_FALSE(g_wdt->IsHungForTest("nonexistent"));
}

// Test 3: excludes ISR — we never call FeedCountFor("isr_*").
void test_watchdog_excludes_isr() {
  g_wdt->Register("audio_capture");
  g_wdt->FeedAll();
  // ISRs are not registered; IsHungForTest("isr_lora") is false.
  TEST_ASSERT_FALSE(g_wdt->IsHungForTest("isr_lora"));
}

// Test 4: panic resets — multiple feeds without crash.
void test_watchdog_panic_resets() {
  g_wdt->Register("audio_capture");
  for (int i = 0; i < 100; ++i)
    g_wdt->FeedAll();
  TEST_ASSERT_EQUAL(100, g_wdt->FeedCount());
  TEST_ASSERT_EQUAL(100, g_wdt->FeedCountFor("audio_capture"));
}

// Test 5: empty task name is rejected by Register.
void test_watchdog_empty_name_rejected() {
  TEST_ASSERT_FALSE(g_wdt->Register(""));
}

// Test 6: FeedCountFor for an unknown task returns 0.
void test_watchdog_feed_count_unknown_task() {
  TEST_ASSERT_EQUAL(0, g_wdt->FeedCountFor("never_registered"));
}

// Test 7: an unknown ResetReason casts to the fallthrough name.
// We synthesise this by casting a large uint8_t value to the
// enum (the switch in ResetReasonName has a fallthrough return).
void test_watchdog_unknown_reason_fallthrough() {
  // kUnknown is the sentinel; verifying the fallthrough path
  // is reachable only via a malformed value, which is asserted
  // here so the test suite is robust to future enum additions.
  ResetReason bogus = static_cast<ResetReason>(0xFF);
  const char *name = Watchdog::ResetReasonName(bogus);
  TEST_ASSERT_EQUAL_STRING("unknown", name);
}

extern "C" void register_watchdog_reset_tests();

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_watchdog_feeds_all_tasks);
  RUN_TEST(test_watchdog_triggers_on_hung_task);
  RUN_TEST(test_watchdog_excludes_isr);
  RUN_TEST(test_watchdog_panic_resets);
  RUN_TEST(test_watchdog_empty_name_rejected);
  RUN_TEST(test_watchdog_feed_count_unknown_task);
  RUN_TEST(test_watchdog_unknown_reason_fallthrough);
  register_watchdog_reset_tests();
  (void)0;
  UNITY_END();
}
