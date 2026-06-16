// ui_state.h — Tether M5 UI state machine (plan.md §4.8.5 + §5.4).
//
// The UI state owns everything the M5 displays: the current
// screen, the active conversation, the scroll position in the
// conv tab strip, the volume, the partial-refresh counter, and
// the EPD watchdog flag. It is the only component that calls
// the EPD renderer.
//
// Two interfaces drive the state machine:
//   * `SetPtt(Ptt*)` — PTT state-change observer. PTT → screen
//     transitions (recording, queued, transmitting, acked, ...).
//   * `OnButtonEvent(ButtonEvent)` — B / C presses cycle the
//     active conv or navigate the settings screen.
//
// Phase 3 only declared a minimal stub. Phase 4 adds:
//   * Conv-switcher state (current index, scroll position)
//   * EPD::PartialRefresh rate limiter (full refresh every
//     kEpdFullRefreshEvery partials)
//   * Settings mode (B held 2 s)
//   * EPD controller watchdog (block partials if the driver is
//     unresponsive; the EPD class exposes this as a flag)

#pragma once

#include <cstdint>
#include <vector>

#include "buttons.h"
#include "epd.h"
#include "ptt.h"

namespace tether::m5 {

// Every visible screen on the M5. Kept in sync with epd.h's
// renderer entry points.
enum class UiScreen : uint8_t {
  kIdle = 0,
  kRecording = 1,
  kQueued = 2,
  kTransmitting = 3,
  kAcked = 4,
  kSettings = 5,
  kLowBattery = 6,
  kTtsPlayback = 7,
};

// Battery threshold below which the low-battery warning shows
// (research.md §9.3). 3.40 V is the cut-off for the M5's 18650.
inline constexpr int kLowBatteryMv = 3400;

class ConvDb; // forward decl — the UI reads from the DB on demand

class UiState {
public:
  UiState();

  // Wire to a Ptt state-change observer. Replaces any prior
  // observer; returns the previous one.
  Ptt::StateChangeHandler SetPtt(Ptt *ptt);

  // Conv list. The UI does not own this; the conv_manager
  // task feeds it. The pointer must outlive the UiState.
  void SetConversations(const std::vector<ConvInfo> *convs) { convs_ = convs; }

  // EPD controller. Default-constructed one is used if none is
  // wired; tests inject a controller to inspect what was
  // rendered.
  void SetEpd(EPD *epd) { epd_ = epd; }

  // Drive a button event. This is the only way the UI reacts to
  // physical button presses.
  void OnButtonEvent(ButtonEvent ev);

  // Pump the state machine. The FreeRTOS task calls this on
  // every wakeup. Returns the current screen.
  UiScreen Tick();

  // Accessors.
  UiScreen Screen() const { return screen_; }
  const char *ScreenName() const;
  size_t CurrentConvIndex() const { return current_index_; }
  size_t ScrollPos() const { return scroll_pos_; }
  uint32_t PartialRefreshCount() const { return partial_count_; }
  uint8_t Volume() const { return volume_; }
  bool SettingsActive() const { return in_settings_; }
  bool LowBattery() const { return low_battery_; }
  int VbatMv() const { return vbat_mv_; }
  uint8_t Channel() const { return channel_; }

  // Test seam: force a screen directly. Bypasses the PTT
  // observer; used by tests to set up a starting state.
  void SetScreenForTest(UiScreen s) { screen_ = s; }

  // Test seam: simulate a battery voltage change.
  void SetVbatMvForTest(int mv) { vbat_mv_ = mv; }

  // History of (PttState, UiScreen) transitions, for tests.
  struct LogEntry {
    PttState ptt;
    UiScreen ui;
  };
  const std::vector<LogEntry> &Log() const { return log_; }

  // Render the current screen to the EPD. The task calls this
  // after every state change. Internally rate-limits partial
  // refreshes (full refresh every kEpdFullRefreshEvery).
  void Render();

private:
  void OnPttChange(PttState old_s, PttState new_s);
  void OnSettingsEntry();
  void OnSettingsExit();
  void AdvanceConv(int delta);
  void ChangeVolume(int delta);
  void UpdateBattery();
  void RenderIdle();
  void RenderRecording();
  void RenderQueued();
  void RenderTransmitting();
  void RenderTts();
  void RenderSettings();
  void RenderLowBattery();
  // Issue a partial or full refresh depending on the
  // partial_count_ counter and the EPD watchdog.
  void IssuePartial(const uint8_t *buf);
  void IssueFull(const uint8_t *buf);

  Ptt *ptt_ = nullptr;
  Ptt::StateChangeHandler prev_handler_;
  EPD *epd_ = nullptr;
  const std::vector<ConvInfo> *convs_ = nullptr;
  UiScreen screen_ = UiScreen::kIdle;
  std::vector<LogEntry> log_;
  // Conv switcher state.
  size_t current_index_ = 0;
  size_t scroll_pos_ = 0;
  // EPD rate limiter.
  uint32_t partial_count_ = 0;
  // Settings state.
  bool in_settings_ = false;
  uint8_t settings_cursor_ = 0;
  // Common UI state.
  uint8_t volume_ = 60;
  uint8_t channel_ = 0;
  int vbat_mv_ = 3900;
  bool low_battery_ = false;
  // Scratch buffer (kEpdBufSize bytes). Allocated once on
  // first use to avoid a global.
  uint8_t *scratch_ = nullptr;
};

} // namespace tether::m5
