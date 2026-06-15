// ui_state.h — Tether M5 UI state task (plan.md §4.8.5).
//
// Phase 3 implements a minimal stub that tracks the current UI screen
// and emits a state-change log. Phase 4 will replace this with a
// full EPD renderer (plan §5.3).
#pragma once

#include <cstdint>
#include <string>
#include <vector>

#include "ptt.h"

namespace tether::m5 {

enum class UiScreen : uint8_t {
  kIdle = 0,
  kRecording = 1,
  kQueued = 2,
  kTransmitting = 3,
  kAcked = 4,
  kSettings = 5,
  kLowBattery = 6,
};

class UiState {
public:
  UiState() = default;

  // Wire to a Ptt state-change observer.
  void SetPtt(Ptt *ptt);

  // Pump the UI state machine. Returns the current screen.
  UiScreen Tick();

  UiScreen Screen() const { return screen_; }
  const char *ScreenName() const;

  // Test seam: force a screen.
  void SetScreenForTest(UiScreen s) { screen_ = s; }

  // History of (PttState, UiScreen) transitions, for tests.
  struct LogEntry {
    PttState ptt;
    UiScreen ui;
  };
  const std::vector<LogEntry> &Log() const { return log_; }

private:
  void OnPttChange(PttState old_s, PttState new_s);

  Ptt *ptt_ = nullptr;
  UiScreen screen_ = UiScreen::kIdle;
  std::vector<LogEntry> log_;
};

} // namespace tether::m5
