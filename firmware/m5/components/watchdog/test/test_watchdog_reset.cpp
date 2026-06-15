// test_watchdog_reset_addon.cpp — extra tests for the Phase 8
// reset-reason API. Linked into the test_watchdog binary
// alongside test_watchdog.cpp via the test_host/CMakeLists.txt
// `add_host_test` collector. The tests are registered with
// Unity via a side-effect-free helper that the main test file
// pulls in.
//
// This file does NOT define main() or setUp/tearDown; those
// live in test_watchdog.cpp and are shared with the other
// tests in the binary.

#include <string>

#include <unity.h>

#include "watchdog.h"
#include "test_watchdog_state.h"

using tether::m5::ResetReason;
using tether::m5::Watchdog;

// ── Phase 8 tests ────────────────────────────────────────────────────

void test_watchdog_default_reset_reason() {
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPowerOn),
                    static_cast<int>(g_wdt->LastResetReason()));
  TEST_ASSERT_EQUAL_STRING("", g_wdt->LastPanickedTask().c_str());
}

void test_watchdog_record_watchdog_reset() {
  g_wdt->Register("audio_capture");
  g_wdt->NotifyHung("audio_capture");
  g_wdt->RecordReset(ResetReason::kTaskWdt, "audio_capture");
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kTaskWdt),
                    static_cast<int>(g_wdt->LastResetReason()));
  TEST_ASSERT_EQUAL_STRING("audio_capture",
                          g_wdt->LastPanickedTask().c_str());
}

void test_watchdog_record_panic_reset() {
  g_wdt->RecordReset(ResetReason::kPanic, "ui_state");
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPanic),
                    static_cast<int>(g_wdt->LastResetReason()));
  TEST_ASSERT_EQUAL_STRING("ui_state", g_wdt->LastPanickedTask().c_str());
}

void test_watchdog_notify_hung_marks_only() {
  // NotifyHung increments the hung-event counter and must NOT
  // auto-record a reset. The actual IsHungForTest() check is
  // gated on the threshold-missed-feed logic, which is
  // covered by the existing test_watchdog_triggers_on_hung_task
  // test in test_watchdog.cpp. Here we just verify that
  // NotifyHung + a ResetReason::kPowerOn default leaves the
  // reset reason untouched (NotifyHung is a flag-setter, not a
  // reset trigger).
  g_wdt->Register("audio_capture");
  uint32_t before = g_wdt->HungEventCount();
  g_wdt->NotifyHung("audio_capture");
  TEST_ASSERT_EQUAL(before + 1, g_wdt->HungEventCount());
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPowerOn),
                    static_cast<int>(g_wdt->LastResetReason()));
}

void test_watchdog_notify_hung_unknown_task() {
  g_wdt->NotifyHung("never_registered");
  TEST_ASSERT_FALSE(g_wdt->IsHungForTest("never_registered"));
}

void test_watchdog_hung_count_increments() {
  uint32_t before = g_wdt->HungEventCount();
  g_wdt->Register("audio_capture");
  g_wdt->NotifyHung("audio_capture");
  TEST_ASSERT_EQUAL(before + 1, g_wdt->HungEventCount());
  g_wdt->NotifyHung("audio_capture");
  TEST_ASSERT_EQUAL(before + 2, g_wdt->HungEventCount());
}

void test_watchdog_reset_history() {
  g_wdt->RecordReset(ResetReason::kPowerOn, "");
  g_wdt->RecordReset(ResetReason::kTaskWdt, "audio_capture");
  g_wdt->RecordReset(ResetReason::kPanic, "ui_state");
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPanic),
                    static_cast<int>(g_wdt->LastResetReason()));
  TEST_ASSERT_EQUAL_STRING("ui_state", g_wdt->LastPanickedTask().c_str());
  const auto &hist = g_wdt->ResetHistory();
  TEST_ASSERT_EQUAL(3, hist.size());
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPowerOn),
                    static_cast<int>(hist[0].reason));
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kTaskWdt),
                    static_cast<int>(hist[1].reason));
  TEST_ASSERT_EQUAL_STRING("audio_capture", hist[1].task_name.c_str());
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kPanic),
                    static_cast<int>(hist[2].reason));
  TEST_ASSERT_EQUAL_STRING("ui_state", hist[2].task_name.c_str());
}

void test_watchdog_reset_history_bounded() {
  for (int i = 0; i < 200; ++i) {
    g_wdt->RecordReset(ResetReason::kTaskWdt, "x");
  }
  const auto &hist = g_wdt->ResetHistory();
  TEST_ASSERT_LESS_OR_EQUAL(16, hist.size());
  TEST_ASSERT_EQUAL(static_cast<int>(ResetReason::kTaskWdt),
                    static_cast<int>(hist.back().reason));
}

void test_watchdog_boot_count() {
  uint32_t before = g_wdt->BootCount();
  g_wdt->NoteBoot();
  TEST_ASSERT_EQUAL(before + 1, g_wdt->BootCount());
  g_wdt->NoteBoot();
  TEST_ASSERT_EQUAL(before + 2, g_wdt->BootCount());
}

void test_watchdog_reason_name() {
  TEST_ASSERT_EQUAL_STRING("power-on",
                           Watchdog::ResetReasonName(ResetReason::kPowerOn));
  TEST_ASSERT_EQUAL_STRING("task-wdt",
                           Watchdog::ResetReasonName(ResetReason::kTaskWdt));
  TEST_ASSERT_EQUAL_STRING("panic",
                           Watchdog::ResetReasonName(ResetReason::kPanic));
  TEST_ASSERT_EQUAL_STRING("soft-restart",
                           Watchdog::ResetReasonName(ResetReason::kSoftRestart));
  TEST_ASSERT_EQUAL_STRING("brownout",
                           Watchdog::ResetReasonName(ResetReason::kBrownout));
  TEST_ASSERT_EQUAL_STRING("unknown",
                           Watchdog::ResetReasonName(ResetReason::kUnknown));
}

// register_watchdog_reset_tests is called from test_watchdog.cpp's
// main(). It registers the Phase 8 tests with Unity.
extern "C" void register_watchdog_reset_tests() {
  RUN_TEST(test_watchdog_default_reset_reason);
  RUN_TEST(test_watchdog_record_watchdog_reset);
  RUN_TEST(test_watchdog_record_panic_reset);
  RUN_TEST(test_watchdog_notify_hung_marks_only);
  RUN_TEST(test_watchdog_notify_hung_unknown_task);
  RUN_TEST(test_watchdog_hung_count_increments);
  RUN_TEST(test_watchdog_reset_history);
  RUN_TEST(test_watchdog_reset_history_bounded);
  RUN_TEST(test_watchdog_boot_count);
  RUN_TEST(test_watchdog_reason_name);
}
