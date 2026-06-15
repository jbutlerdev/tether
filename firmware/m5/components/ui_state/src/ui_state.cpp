// ui_state.cpp — minimal UI state for Phase 3.
#include "ui_state.h"

namespace tether::m5 {

const char *UiState::ScreenName() const {
  switch (screen_) {
    case UiScreen::kIdle: return "Idle";
    case UiScreen::kRecording: return "Recording";
    case UiScreen::kQueued: return "Queued";
    case UiScreen::kTransmitting: return "Transmitting";
    case UiScreen::kAcked: return "Acked";
    case UiScreen::kSettings: return "Settings";
    case UiScreen::kLowBattery: return "LowBattery";
  }
  return "?";
}

void UiState::SetPtt(Ptt *ptt) {
  ptt_ = ptt;
  if (ptt_) {
    ptt_->OnStateChange([this](PttState old_s, PttState new_s) {
      OnPttChange(old_s, new_s);
    });
  }
}

void UiState::OnPttChange(PttState /*old_s*/, PttState new_s) {
  switch (new_s) {
    case PttState::kIdle: screen_ = UiScreen::kIdle; break;
    case PttState::kRecording: screen_ = UiScreen::kRecording; break;
    case PttState::kQueued: screen_ = UiScreen::kQueued; break;
    case PttState::kTransmitting: screen_ = UiScreen::kTransmitting; break;
    case PttState::kAcked: screen_ = UiScreen::kAcked; break;
    case PttState::kCanceled: screen_ = UiScreen::kIdle; break;
    case PttState::kFailed: screen_ = UiScreen::kIdle; break;
  }
  log_.push_back(LogEntry{new_s, screen_});
}

UiScreen UiState::Tick() {
  // Phase 3: nothing to pump. Phase 4 will drive partial / full
  // refresh cycles here.
  return screen_;
}

}  // namespace tether::m5
