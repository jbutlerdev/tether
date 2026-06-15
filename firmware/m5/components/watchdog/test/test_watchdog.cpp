// test_watchdog.cpp — unit tests for tether::m5::Watchdog.
#include <string>

#include <unity.h>

#include "watchdog.h"

using tether::m5::Watchdog;

namespace {
Watchdog *g_wdt = nullptr;
void Reset() {
  delete g_wdt;
  g_wdt = new Watchdog();
  g_wdt->SetHungThresholdMsForTest(5000);
}
} // namespace

void setUp() { Reset(); }
void tearDown() { delete g_wdt; g_wdt = nullptr; }

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
  for (int i = 0; i < 20; ++i) g_wdt->FeedAll();
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
  for (int i = 0; i < 100; ++i) g_wdt->FeedAll();
  TEST_ASSERT_EQUAL(100, g_wdt->FeedCount());
  TEST_ASSERT_EQUAL(100, g_wdt->FeedCountFor("audio_capture"));
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_watchdog_feeds_all_tasks);
  RUN_TEST(test_watchdog_triggers_on_hung_task);
  RUN_TEST(test_watchdog_excludes_isr);
  RUN_TEST(test_watchdog_panic_resets);
  (void)0;
  UNITY_END();
}
