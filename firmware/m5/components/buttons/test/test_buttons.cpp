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
  g_buttons->SimulatePressForTest(Button::kNext);
  g_buttons->Tick(25);
  g_buttons->Tick(100); // long-press threshold reached
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kLongPressNext),
                    static_cast<int>(g_events[1].event));
  g_buttons->SimulateReleaseForTest(Button::kNext);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(2, g_events.size());
}

// Test 5: Prev button does NOT emit a long-press event (no
// kLongPressPrev in the spec).
void test_buttons_prev_no_long_press() {
  g_buttons->SimulatePressForTest(Button::kPrev);
  g_buttons->Tick(25);
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  g_buttons->Tick(5000); // way past any threshold
  // Only the kPress event should be present.
  TEST_ASSERT_EQUAL_size_t(1, g_events.size());
  TEST_ASSERT_EQUAL(static_cast<int>(Event::kPress),
                    static_cast<int>(g_events[0].event));
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_buttons_press_release);
  RUN_TEST(test_buttons_debounce);
  RUN_TEST(test_buttons_long_press_ptt);
  RUN_TEST(test_buttons_long_press_next);
  RUN_TEST(test_buttons_prev_no_long_press);
  (void)0;
  UNITY_END();
}
