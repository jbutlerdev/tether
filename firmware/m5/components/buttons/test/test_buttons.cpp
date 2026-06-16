// test_buttons.cpp — unit tests for tether::m5::Buttons (plan.md §4.7).
//
// Host tests inject virtual GPIO events via SimulatePressForTest /
// SimulateReleaseForTest and pump the state machine with Tick() to
// advance the clock. On real hardware the GPIO ISR feeds a queue that
// the debounce task drains; the same Buttons class works for both.

#include <vector>

#include <unity.h>

#include "buttons.h"

using tether::m5::Button;
using tether::m5::ButtonEvent;
using tether::m5::Buttons;
using tether::m5::Event;

namespace {
std::vector<ButtonEvent> g_events;
Buttons *g_buttons = nullptr;

void Capture(ButtonEvent e) { g_events.push_back(e); }

void Reset() {
  g_events.clear();
  if (g_buttons) {
    delete g_buttons;
  }
  g_buttons = new Buttons();
  g_buttons->SetDebounceMsForTest(20);
  g_buttons->SetLongPressPttMsForTest(200);
  g_buttons->SetLongPressNextMsForTest(100);
  g_buttons->Init(Capture);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  if (g_buttons) {
    delete g_buttons;
    g_buttons = nullptr;
  }
  g_events.clear();
}

// Test 1: simulate press, debounce expires, event fires.
void test_buttons_press_release() {
  g_buttons->SimulatePressForTest(Button::kPtt);
  // Debounce 20 ms, plus 5 ms extra.
  g_buttons->Tick(25);
  // 1 event: kPress.
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Button::kPtt),
                    static_cast<int>(g_events[0].button));
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kPress),
                    static_cast<int>(g_events[0].event));
  // Release.
  g_buttons->SimulateReleaseForTest(Button::kPtt);
  g_buttons->Tick(25);
  // 2 events: kPress, kRelease.
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kRelease),
                    static_cast<int>(g_events[1].event));
}

// Test 2: 50 ms bouncing — only one event fires.
void test_buttons_debounce() {
  // Press.
  g_buttons->SimulatePressForTest(Button::kPtt);
  g_buttons->Tick(5); // not enough to debounce
  g_buttons->SimulateReleaseForTest(Button::kPtt);
  g_buttons->Tick(5); // not enough to debounce
  g_buttons->SimulatePressForTest(Button::kPtt);
  g_buttons->Tick(5); // not enough to debounce
  g_buttons->SimulateReleaseForTest(Button::kPtt);
  // After 15 ms, no event has fired yet because debounce never settled.
  TEST_ASSERT_EQUAL_size_t(0, g_events.size());
  // Settle: press again and hold past debounce.
  g_buttons->SimulatePressForTest(Button::kPtt);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kPress),
                    static_cast<int>(g_events[0].event));
}

// Test 3: 3 s hold → kLongPressPtt fires (configured to 200 ms here).
void test_buttons_long_press_ptt() {
  g_buttons->SimulatePressForTest(Button::kPtt);
  g_buttons->Tick(25); // debounce settles
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  g_buttons->Tick(200); // long-press threshold reached
  // Should have fired kLongPressPtt.
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kLongPressPtt),
                    static_cast<int>(g_events[1].event));
  // Releasing should NOT fire kRelease.
  g_buttons->SimulateReleaseForTest(Button::kPtt);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
}

// Test 4: 2 s hold on Next → kLongPressNext fires.
void test_buttons_long_press_next() {
  g_buttons->SimulatePressForTest(Button::kMenu);
  g_buttons->Tick(25);
  g_buttons->Tick(100); // long-press threshold reached
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kLongPressMenu),
                    static_cast<int>(g_events[1].event));
  g_buttons->SimulateReleaseForTest(Button::kMenu);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
}

// Test 5: Menu (a.k.a. kNext) button long-press fires kLongPressMenu
// after the configured threshold. The M5 has 2 physical buttons —
// see buttons.h — and the second button is "Menu/Cycle" (the
// legacy kNext alias). kLongPressMenu fires once and is followed
// by no kRelease (the release is suppressed, see the header).
void test_buttons_menu_long_press() {
  g_buttons->SimulatePressForTest(Button::kMenu);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  // Hold past the long-press threshold (2 s default).
  g_buttons->Tick(2500);
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Button::kMenu),
                    static_cast<int>(g_events[1].button));
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kLongPressMenu),
                    static_cast<int>(g_events[1].event));
  // A long-press suppresses the matching kRelease, but a subsequent
  // press/release should still be observed.
  g_buttons->SimulateReleaseForTest(Button::kMenu);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_buttons_press_release);
  RUN_TEST(test_buttons_debounce);
  RUN_TEST(test_buttons_long_press_ptt);
  RUN_TEST(test_buttons_long_press_next);
  RUN_TEST(test_buttons_menu_long_press);
  (void)0;
  UNITY_END();
}
