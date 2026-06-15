// test_ptt_state.cpp — unit tests for tether::m5::Ptt (plan.md §4.8.1).
//
// The tests below exercise the state machine transitions listed in
// the plan: idle ↔ recording, queued, transmitting, acked, failed,
// canceled, TTS suppression, and the rejection of illegal transitions.

#include <vector>

#include <unity.h>

#include "ptt.h"
#include "buttons.h"

using tether::m5::Button;
using tether::m5::ButtonEvent;
using tether::m5::Event;
using tether::m5::Ptt;
using tether::m5::PttState;

namespace {
std::vector<std::pair<PttState, PttState>> g_changes;
Ptt *g_ptt = nullptr;

void Capture(PttState old_s, PttState new_s) {
  g_changes.emplace_back(old_s, new_s);
}

ButtonEvent Press(Button b) { return ButtonEvent{b, Event::kPress}; }
ButtonEvent Release(Button b) { return ButtonEvent{b, Event::kRelease}; }
ButtonEvent LongPtt() { return ButtonEvent{Button::kPtt, Event::kLongPressPtt}; }
} // namespace

void setUp() {
  g_changes.clear();
  g_ptt = new Ptt();
  g_ptt->OnStateChange(Capture);
}

void tearDown() {
  delete g_ptt;
  g_ptt = nullptr;
  g_changes.clear();
}

// Test 1: PTT press in IDLE → RECORDING.
void test_ptt_idle_to_recording_on_press() {
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_ptt->State()));
}

// Test 2: PTT release in RECORDING → QUEUED.
void test_ptt_recording_to_queued_on_release() {
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_ptt->State()));
  g_ptt->OnButton(Release(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kQueued),
                    static_cast<int>(g_ptt->State()));
}

// Test 3: PTT long-press in RECORDING → IDLE (cancel).
void test_ptt_recording_to_idle_on_long_press() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(LongPtt());
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
}

// Test 4: QUEUED + radio accepted → TRANSMITTING.
void test_ptt_queued_to_transmitting() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kQueued),
                    static_cast<int>(g_ptt->State()));
  g_ptt->OnRadioAccepted();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kTransmitting),
                    static_cast<int>(g_ptt->State()));
  TEST_ASSERT_EQUAL(1, g_ptt->AcceptedCount());
}

// Test 5: TRANSMITTING + all-chunks-acked → ACKED.
void test_ptt_transmitting_to_acked() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kAcked),
                    static_cast<int>(g_ptt->State()));
  TEST_ASSERT_EQUAL(1, g_ptt->AllAckedCount());
}

// Test 6: ACKED auto-clears to IDLE after a state-change observer
// would normally schedule the transition. We simulate by directly
// calling SetStateForTest then verifying the next PTT press moves to
// RECORDING.
void test_ptt_acked_to_idle_after_2s() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kAcked),
                    static_cast<int>(g_ptt->State()));
  // Simulate the 2 s auto-clear by transitioning manually.
  g_ptt->SetStateForTest(PttState::kIdle);
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_ptt->State()));
}

// Test 7: TRANSMITTING + retry budget exceeded → FAILED.
void test_ptt_transmitting_to_failed() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioFailed();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kFailed),
                    static_cast<int>(g_ptt->State()));
  TEST_ASSERT_EQUAL(1, g_ptt->FailedCount());
}

// Test 8: PTT long-press in TRANSMITTING → CANCELED.
void test_ptt_cancel_during_transmitting() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kTransmitting),
                    static_cast<int>(g_ptt->State()));
  g_ptt->OnButton(LongPtt());
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kCanceled),
                    static_cast<int>(g_ptt->State()));
}

// Test 9: TTS active suppresses PTT press (no state change).
void test_ptt_no_press_during_tts_playback() {
  g_ptt->OnTtsStarted();
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
  g_ptt->OnTtsFinished();
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_ptt->State()));
}

// Test 10: illegal transitions are rejected (no state change).
void test_ptt_illegal_transitions_rejected() {
  // IDLE + radio accepted: should NOT move to TRANSMITTING.
  g_ptt->OnRadioAccepted();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
  // IDLE + all-acked: no-op.
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
  // IDLE + radio failed: no-op.
  g_ptt->OnRadioFailed();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_ptt->State()));
  // RECORDING + all-acked: no-op.
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_ptt->State()));
}

// Test 11: state-change handler is invoked.
void test_ptt_state_change_handler() {
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_GREATER_THAN(0, g_changes.size());
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kIdle),
                    static_cast<int>(g_changes[0].first));
  TEST_ASSERT_EQUAL(static_cast<int>(PttState::kRecording),
                    static_cast<int>(g_changes[0].second));
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_ptt_idle_to_recording_on_press);
  RUN_TEST(test_ptt_recording_to_queued_on_release);
  RUN_TEST(test_ptt_recording_to_idle_on_long_press);
  RUN_TEST(test_ptt_queued_to_transmitting);
  RUN_TEST(test_ptt_transmitting_to_acked);
  RUN_TEST(test_ptt_acked_to_idle_after_2s);
  RUN_TEST(test_ptt_transmitting_to_failed);
  RUN_TEST(test_ptt_cancel_during_transmitting);
  RUN_TEST(test_ptt_no_press_during_tts_playback);
  RUN_TEST(test_ptt_illegal_transitions_rejected);
  RUN_TEST(test_ptt_state_change_handler);
  (void)0;
  UNITY_END();
}
