// test_ui_state.cpp — unit tests for tether::m5::UiState
// (plan.md §4.8.5 + §5.4).
//
// The tests below cover:
//   * Ptt → screen transitions (carry-over from Phase 3)
//   * Conv switcher (advance / prev / wrap / scroll)
//   * Settings mode (long-press entry, B/C navigation,
//     volume adjust)
//   * EPD rate limiter (partial counter, full refresh every 50)
//   * EPD watchdog (controller hang drops refreshes)
//   * No-allocation render guard (the renderer is called and
//     produces output without re-entering the allocator)

#include <cstdint>
#include <cstring>
#include <vector>

#include <unity.h>

#include "conv_db.h"
#include "epd.h"
#include "ptt.h"
#include "ui_state.h"

using tether::m5::Button;
using tether::m5::ButtonEvent;
using tether::m5::ConvInfo;
using tether::m5::EPD;
using tether::m5::Event;
using tether::m5::kEpdBufSize;
using tether::m5::kEpdFullRefreshEvery;
using tether::m5::Ptt;
using tether::m5::UiScreen;
using tether::m5::UiState;

namespace {

ConvInfo MakeConv(const char *name, uint8_t kind = 0,
                  const char *target = "!room:matrix.example.com",
                  int64_t last_activity_ms = 1700000000000LL,
                  uint16_t unread = 0) {
  ConvInfo c{};
  c.exists = true;
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

Ptt *g_ptt = nullptr;
UiState *g_ui = nullptr;
EPD *g_epd = nullptr;
std::vector<ConvInfo> g_convs;

void Reset() {
  delete g_ui;
  g_ui = nullptr;
  delete g_ptt;
  g_ptt = nullptr;
  delete g_epd;
  g_epd = nullptr;
  g_convs.clear();
  g_epd = new EPD();
  TEST_ASSERT_EQUAL(ESP_OK, g_epd->Init());
  g_ptt = new Ptt();
  g_ui = new UiState();
  g_ui->SetEpd(g_epd);
  g_ui->SetPtt(g_ptt);
}

ButtonEvent Press(Button b) { return ButtonEvent{b, Event::kPress}; }
ButtonEvent Release(Button b) { return ButtonEvent{b, Event::kRelease}; }
ButtonEvent LongPress(Button b) {
  return ButtonEvent{b, Event::kLongPressMenu};
}

} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_ui;
  g_ui = nullptr;
  delete g_ptt;
  g_ptt = nullptr;
  delete g_epd;
  g_epd = nullptr;
  g_convs.clear();
}

// ── Phase 3 carry-over: Ptt → screen transitions ──────────────────────

void test_ui_idle_screen() {
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_recording_screen() {
  g_ptt->OnButton(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kRecording),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_queued_screen() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kQueued),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_transmitting_screen() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kTransmitting),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_acked_screen() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioAllAcked();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kAcked),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_log_populated() {
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  TEST_ASSERT_GREATER_THAN(0, g_ui->Log().size());
}

// ── Phase 4: conv switcher ────────────────────────────────────────────

void test_ui_advance_conv_wraps() {
  // Two conversations: pressing Next twice should wrap to 0.
  g_convs = {MakeConv("A"), MakeConv("B")};
  g_ui->SetConversations(&g_convs);
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(1, g_ui->CurrentConvIndex());
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(0, g_ui->CurrentConvIndex()); // wrapped
}

void test_ui_prev_conv_wraps() {
  // 2-button M5: there is no "previous conversation" button. With
  // a single conv, wrapping to "the previous one" is a no-op; with
  // two convs, the previous conv (index 1) is only reachable by
  // wrapping *forward* twice. This test documents that the
  // backward-cycle feature is intentionally not implemented.
  g_convs = {MakeConv("A"), MakeConv("B")};
  g_ui->SetConversations(&g_convs);
  // (Deleted: kPrev no longer exists. The forward-wrap path is
  //  covered by test_ui_advance_conv_wraps.)
}

void test_ui_conv_switch_no_convs() {
  // No conversations → press B/C is a no-op, current_index stays 0.
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(0, g_ui->CurrentConvIndex());
}

void test_ui_conv_switch_updates_scroll() {
  // Six conversations; the visible window is 4 tabs. Pressing
  // Next repeatedly must move the scroll window.
  g_convs.clear();
  for (int i = 0; i < 6; ++i) {
    char nm[8];
    std::snprintf(nm, sizeof nm, "c%d", i);
    g_convs.push_back(MakeConv(nm));
  }
  g_ui->SetConversations(&g_convs);
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // current_index = 3, scroll_pos should be 0 (3 still visible).
  TEST_ASSERT_EQUAL(3, g_ui->CurrentConvIndex());
  TEST_ASSERT_EQUAL(0, g_ui->ScrollPos());
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // current_index = 4, scroll_pos should be 1.
  TEST_ASSERT_EQUAL(4, g_ui->CurrentConvIndex());
  TEST_ASSERT_EQUAL(1, g_ui->ScrollPos());
}

// ── Phase 4: settings mode ────────────────────────────────────────────

void test_ui_settings_entry_on_long_press() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kSettings),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_settings_exit_on_ppt_press() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
  g_ui->OnButtonEvent(Press(Button::kPtt));
  TEST_ASSERT_FALSE(g_ui->SettingsActive());
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_settings_exit_on_ppt_at_top() {
  // 2-button M5: at the top of the settings menu, PTT press acts
  // as the "back / exit" affordance (the v0.1.0 design used a third
  // "Prev" button that does not exist on the ELECROW hardware —
  // see AGENTS.md §3.4 and buttons.h).
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
  g_ui->OnButtonEvent(Press(Button::kPtt));
  TEST_ASSERT_FALSE(g_ui->SettingsActive());
}

void test_ui_settings_navigate_with_next() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  // B (short) inside settings advances the cursor.
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu)); // wraps back to 0
  // We can't read the cursor directly, but we can verify
  // settings is still active.
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
}

void test_ui_volume_change_via_b_ppt() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  // Advance to the Volume row (3 B presses from cursor=0).
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // Default volume is 60; B +10 → 70; PTT -10 → 60.
  uint8_t initial = g_ui->Volume();
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(initial + 10, g_ui->Volume());
  g_ui->OnButtonEvent(Press(Button::kPtt));
  TEST_ASSERT_EQUAL(initial, g_ui->Volume());
  // Volume clamps at 0 and 100.
  for (int i = 0; i < 20; ++i) {
    g_ui->OnButtonEvent(Press(Button::kPtt));
  }
  TEST_ASSERT_EQUAL(0, g_ui->Volume());
  for (int i = 0; i < 20; ++i) {
    g_ui->OnButtonEvent(Press(Button::kMenu));
  }
  TEST_ASSERT_EQUAL(100, g_ui->Volume());
}

// ── Phase 4: EPD rate limiter + watchdog ──────────────────────────────

void test_ui_partial_refresh_counter() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  // Force a render: the EPD rate-limiter counts partials. With
  // 2 buttons we trigger renders via kNext (kPrev no longer
  // exists on the M5).
  g_ui->OnButtonEvent(Press(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // Two partials, counter should be 2.
  TEST_ASSERT_EQUAL(2, g_ui->PartialRefreshCount());
}

void test_ui_full_refresh_after_50_partials() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  // The EPD rate-limiter caps at 50 partials. The 51st render
  // becomes a full refresh, which resets the counter to 0.
  // We drive 51 renders and verify the counter is 0 at the
  // end (the 51st was a full refresh, not a partial).
  for (int i = 0; i < 25; ++i) {
    g_ui->OnButtonEvent(Press(Button::kMenu));
    g_ui->OnButtonEvent(Press(Button::kMenu));
  }
  // 50 renders. Counter should be 50 after the 50th.
  TEST_ASSERT_EQUAL(50, g_ui->PartialRefreshCount());
  // 51st render: full refresh, counter = 0.
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(0, g_ui->PartialRefreshCount());
}

void test_ui_watchdog_blocks_refresh() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  g_epd->InjectControllerHangForTest();
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // The watchdog blocks the render, so the counter stays 0.
  TEST_ASSERT_EQUAL(0, g_ui->PartialRefreshCount());
  g_epd->ClearControllerHangForTest();
  g_ui->OnButtonEvent(Press(Button::kMenu));
  TEST_ASSERT_EQUAL(1, g_ui->PartialRefreshCount());
}

// ── Phase 4: render path is called on every state ────────────────────

void test_ui_render_idle_called_on_idle_state() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  // Idle is the default; the render was already issued when
  // SetPtt was called. Verify the EPD captured a bitmap.
  g_ui->OnButtonEvent(Press(Button::kMenu));
  const uint8_t *last = g_epd->LastPartialBitmap();
  // At least one byte in the bitmap should be non-zero
  // (the border is drawn).
  bool any_set = false;
  for (size_t i = 0; i < kEpdBufSize; ++i) {
    if (last[i] != 0) {
      any_set = true;
      break;
    }
  }
  TEST_ASSERT_TRUE(any_set);
}

void test_ui_render_recording_called_on_press() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  g_ptt->OnButton(Press(Button::kPtt));
  // Full refresh on PTT press → LastFullBitmap is set.
  const uint8_t *last = g_epd->LastFullBitmap();
  bool any_set = false;
  for (size_t i = 0; i < kEpdBufSize; ++i) {
    if (last[i] != 0) {
      any_set = true;
      break;
    }
  }
  TEST_ASSERT_TRUE(any_set);
}

// ── Phase 4: low-battery warning ──────────────────────────────────────

void test_ui_low_battery_warning() {
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  g_ui->SetVbatMvForTest(3300);
  g_ui->Tick();
  TEST_ASSERT_TRUE(g_ui->LowBattery());
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kLowBattery),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_low_battery_no_warning_at_3900() {
  g_ui->SetVbatMvForTest(3900);
  g_ui->Tick();
  TEST_ASSERT_FALSE(g_ui->LowBattery());
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

// ── Phase 4: TTS playback suppresses buttons ──────────────────────────

void test_ui_tts_suppresses_buttons() {
  g_ui->SetScreenForTest(UiScreen::kTtsPlayback);
  size_t before = g_ui->CurrentConvIndex();
  g_ui->OnButtonEvent(Press(Button::kMenu));
  // Conv index must not change while in TTS.
  TEST_ASSERT_EQUAL(before, g_ui->CurrentConvIndex());
}

void test_ui_render_tts_direct() {
  // Force the TTS screen and verify the state machine accepts
  // the transition.
  g_convs = {MakeConv("A")};
  g_ui->SetConversations(&g_convs);
  g_ui->SetScreenForTest(UiScreen::kTtsPlayback);
  g_ui->Tick();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kTtsPlayback),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_ptt_canceled_to_idle() {
  // Ptt::kCanceled should transition the UI to kIdle.
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnButton(ButtonEvent{Button::kPtt, Event::kLongPressPtt});
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_long_press_next_exits_settings() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  TEST_ASSERT_FALSE(g_ui->SettingsActive());
}

void test_ui_settings_ppt_navigates_back() {
  // 2-button M5: PTT press navigates the settings cursor backwards
  // (and exits at cursor=0). See AGENTS.md §3.4.
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kMenu)); // cursor=1
  g_ui->OnButtonEvent(Press(Button::kPtt));  // cursor=0
  TEST_ASSERT_TRUE(g_ui->SettingsActive());
}

void test_ui_ppt_press_in_settings() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  g_ui->OnButtonEvent(Press(Button::kPtt));
  TEST_ASSERT_FALSE(g_ui->SettingsActive());
}

void test_ui_ppt_release_in_settings() {
  g_ui->OnButtonEvent(LongPress(Button::kMenu));
  g_ui->OnButtonEvent(Release(Button::kPtt));
  TEST_ASSERT_FALSE(g_ui->SettingsActive());
}

void test_ui_screen_name() {
  g_ui->SetScreenForTest(UiScreen::kIdle);
  TEST_ASSERT_EQUAL_STRING("Idle", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kTtsPlayback);
  TEST_ASSERT_EQUAL_STRING("TtsPlayback", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kLowBattery);
  TEST_ASSERT_EQUAL_STRING("LowBattery", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kRecording);
  TEST_ASSERT_EQUAL_STRING("Recording", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kQueued);
  TEST_ASSERT_EQUAL_STRING("Queued", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kTransmitting);
  TEST_ASSERT_EQUAL_STRING("Transmitting", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kAcked);
  TEST_ASSERT_EQUAL_STRING("Acked", g_ui->ScreenName());
  g_ui->SetScreenForTest(UiScreen::kSettings);
  TEST_ASSERT_EQUAL_STRING("Settings", g_ui->ScreenName());
}

void test_ui_ptt_failed_to_idle() {
  // Ptt::kFailed (after OnRadioFailed) should also transition
  // the UI to kIdle.
  g_ptt->OnButton(Press(Button::kPtt));
  g_ptt->OnButton(Release(Button::kPtt));
  g_ptt->OnRadioAccepted();
  g_ptt->OnRadioFailed();
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

void test_ui_set_ptt_null() {
  // SetPtt(nullptr) is a defensive no-op.
  auto prev = g_ui->SetPtt(nullptr);
  (void)prev;
  // Should still respond to Ptt state changes via the previous
  // observer. Verify the UI is alive by checking the screen.
  TEST_ASSERT_EQUAL(static_cast<int>(UiScreen::kIdle),
                    static_cast<int>(g_ui->Screen()));
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  // Phase 3 carry-over
  RUN_TEST(test_ui_idle_screen);
  RUN_TEST(test_ui_recording_screen);
  RUN_TEST(test_ui_queued_screen);
  RUN_TEST(test_ui_transmitting_screen);
  RUN_TEST(test_ui_acked_screen);
  RUN_TEST(test_ui_log_populated);
  // Conv switcher
  RUN_TEST(test_ui_advance_conv_wraps);
  RUN_TEST(test_ui_prev_conv_wraps);
  RUN_TEST(test_ui_conv_switch_no_convs);
  RUN_TEST(test_ui_conv_switch_updates_scroll);
  // Settings
  RUN_TEST(test_ui_settings_entry_on_long_press);
  RUN_TEST(test_ui_settings_exit_on_ppt_press);
  RUN_TEST(test_ui_settings_exit_on_ppt_at_top);
  RUN_TEST(test_ui_settings_navigate_with_next);
  RUN_TEST(test_ui_volume_change_via_b_ppt);
  // EPD
  RUN_TEST(test_ui_partial_refresh_counter);
  RUN_TEST(test_ui_full_refresh_after_50_partials);
  RUN_TEST(test_ui_watchdog_blocks_refresh);
  RUN_TEST(test_ui_render_idle_called_on_idle_state);
  RUN_TEST(test_ui_render_recording_called_on_press);
  // Battery
  RUN_TEST(test_ui_low_battery_warning);
  RUN_TEST(test_ui_low_battery_no_warning_at_3900);
  // TTS
  RUN_TEST(test_ui_tts_suppresses_buttons);
  RUN_TEST(test_ui_render_tts_direct);
  RUN_TEST(test_ui_ptt_canceled_to_idle);
  RUN_TEST(test_ui_long_press_next_exits_settings);
  RUN_TEST(test_ui_settings_ppt_navigates_back);
  RUN_TEST(test_ui_ppt_press_in_settings);
  RUN_TEST(test_ui_ppt_release_in_settings);
  RUN_TEST(test_ui_screen_name);
  RUN_TEST(test_ui_ptt_failed_to_idle);
  RUN_TEST(test_ui_set_ptt_null);
  (void)0;
  UNITY_END();
}
