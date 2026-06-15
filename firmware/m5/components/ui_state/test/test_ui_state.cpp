// test_ui_state.cpp — unit tests for tether::m5::UiState.
#include <unity.h>

#include "ptt.h"
#include "ui_state.h"

using tether::m5::Button;
using tether::m5::ButtonEvent;
using tether::m5::Event;
using tether::m5::Ptt;
using tether::m5::UiScreen;
using tether::m5::UiState;

namespace {
Ptt *g_ptt = nullptr;
UiState *g_ui = nullptr;
void Reset() {
  delete g_ui;
  g_ui = nullptr;
  delete g_ptt;
  g_ptt = nullptr;
  g_ptt = new Ptt();
  g_ui = new UiState();
  g_ui->SetPtt(g_ptt);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_ui;
  g_ui = nullptr;
  delete g_ptt;
  g_ptt = nullptr;
}

// Test 1: IDLE → idle screen.
void test_ui_idle_screen() {
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

// Test 2: PTT press → recording screen.
void test_ui_recording_screen() {
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kRecording),
                    static_cast<int>(g_ui->Screen()));
}

// Test 3: PTT release → queued screen.
void test_ui_queued_screen() {
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kRelease});
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kQueued),
                    static_cast<int>(g_ui->Screen()));
}

// Test 4: radio accepted → transmitting screen.
void test_ui_transmitting_screen() {
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kRelease});
  g_ptt->OnRadioAccepted();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kTransmitting),
                    static_cast<int>(g_ui->Screen()));
}

// Test 5: all-acked → acked screen.
void test_ui_acked_screen() {
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kRelease});
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kAcked),
                    static_cast<int>(g_ui->Screen()));
}

// Test 6: state-change log is populated.
void test_ui_log_populated() {
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kRelease});
  TEST_ASSERT_GREATER_THAN(0, g_ui->Log().size());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_ui_idle_screen);
  RUN_TEST(test_ui_recording_screen);
  RUN_TEST(test_ui_queued_screen);
  RUN_TEST(test_ui_transmitting_screen);
  RUN_TEST(test_ui_acked_screen);
  RUN_TEST(test_ui_log_populated);
  (void)0;
  UNITY_END();
}
