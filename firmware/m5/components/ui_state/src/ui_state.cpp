// ui_state.cpp — Tether M5 UI state machine (plan.md §4.8.5 + §5.4).
//
// The UI state machine drives the EPD renderer. On every state
// change (Ptt observer, button event, periodic Tick) it picks
// the right screen and renders to the EPD. The render path is
// rate-limited: 50 partials trigger a full refresh to clear EPD
// ghosting (plan §5.4 / research.md §9.3).
//
// Conv switcher:
//   * Button B (short): next conv, wrapping
//   * Button C (short): prev conv, wrapping
//   * scroll_pos_ moves with current_index_ so 4 tabs are
//     always visible at the bottom of the idle screen.
//
// Settings mode:
//   * B held 2 s → enter settings (research.md §10 says "B held
//     2 s" enters settings).
//   * B (short) inside settings → next setting
//   * C (short) inside settings → previous / exit
//   * B/C inside the volume line adjust the volume
//
// Watchdog:
//   * If the EPD controller reports !controller_responsive_,
//     partials are dropped and a full refresh is forced on the
//     next render. The task never blocks waiting for a
//     response; it just skips.

#include "ui_state.h"

#include <cstdlib>
#include <cstring>
#include <new>

#include "conv_db.h"

namespace tether::m5 {

namespace {

// ── Settings mode tunables ───────────────────────────────────────────
constexpr uint32_t kSettingsLongPressMs = 2000;
constexpr uint8_t kVolumeStep = 10;
constexpr uint8_t kVolumeMin = 0;
constexpr uint8_t kVolumeMax = 100;
constexpr size_t kTabCount = 4;

// Idle state: how long since the last PTT-release + ACK before
// returning to the idle screen from kAcked.
constexpr uint32_t kAckedDwellMs = 2000;

// Settings cursor positions.
enum SettingsCursor : uint8_t {
  kSettingsChannel = 0,
  kSettingsModem = 1,
  kSettingsVolume = 2,
  kSettingsAddr = 3,
  kSettingsVbat = 4,
  kSettingsCount = 5,
};

} // namespace

const char *UiState::ScreenName() const {
  switch (screen_) {
  case UiScreen::kIdle:
    return "Idle";
  case UiScreen::kRecording:
    return "Recording";
  case UiScreen::kQueued:
    return "Queued";
  case UiScreen::kTransmitting:
    return "Transmitting";
  case UiScreen::kAcked:
    return "Acked";
  case UiScreen::kSettings:
    return "Settings";
  case UiScreen::kLowBattery:
    return "LowBattery";
  case UiScreen::kTtsPlayback:
    return "TtsPlayback";
  }
  return "?";
}

UiState::UiState() {
  scratch_ = static_cast<uint8_t *>(std::malloc(kEpdBufSize));
  if (scratch_) {
    std::memset(scratch_, 0, kEpdBufSize);
  }
}

Ptt::StateChangeHandler UiState::SetPtt(Ptt *ptt) {
  ptt_ = ptt;
  if (!ptt) {
    return nullptr;
  }
  return ptt_->OnStateChange(
      [this](PttState old_s, PttState new_s) { OnPttChange(old_s, new_s); });
}

void UiState::OnPttChange(PttState /*old_s*/, PttState new_s) {
  switch (new_s) {
  case PttState::kIdle:
    // The Ptt state machine does not transition to kIdle from
    // any other state in v1 (the user-initiated paths go
    // IDLE→RECORDING→QUEUED→...). This case is defensive: if
    // a future revision adds a transition, the UI will follow.
    screen_ = UiScreen::kIdle;
    break;
  case PttState::kRecording:
    screen_ = UiScreen::kRecording;
    break;
  case PttState::kQueued:
    screen_ = UiScreen::kQueued;
    break;
  case PttState::kTransmitting:
    screen_ = UiScreen::kTransmitting;
    break;
  case PttState::kAcked:
    screen_ = UiScreen::kAcked;
    break;
  case PttState::kCanceled:
  case PttState::kFailed:
    // Briefly show the idle screen; the conv_manager will
    // surface errors via the system-history channel.
    screen_ = UiScreen::kIdle;
    break;
  }
  log_.push_back(LogEntry{new_s, screen_});
  Render();
}

void UiState::OnButtonEvent(ButtonEvent ev) {
  // TTS playback suppresses all button input (research.md §9.4
  // — the user can't switch convs while a TTS is in flight).
  if (screen_ == UiScreen::kTtsPlayback) {
    return;
  }

  if (in_settings_) {
    if (ev.button == Button::kMenu) {
      if (ev.event == Event::kPress) {
        if (settings_cursor_ == kSettingsVolume) {
          // With 2 buttons, kPtt acts as the "decrease / go back"
          // inside the volume sub-screen. We still advance the
          // cursor on kMenu press.
          ChangeVolume(+static_cast<int>(kVolumeStep));
        } else {
          settings_cursor_ = (settings_cursor_ + 1) % kSettingsCount;
        }
        Render();
      } else if (ev.event == Event::kLongPressMenu) {
        OnSettingsExit();
        Render();
      }
    } else if (ev.button == Button::kPtt) {
      if (ev.event == Event::kPress || ev.event == Event::kRelease) {
        // PTT inside settings: PTT short-press acts as the
        // "previous" / "decrease" control on a 2-button M5
        // (the 3rd "Prev" button that the v0.1.0 design assumed
        // does not exist on the ELECROW hardware — see
        // AGENTS.md §3.4 and buttons.h). For the volume cell it
        // decreases; for other cells it moves the cursor back
        // (or exits at the top).
        if (settings_cursor_ == kSettingsVolume) {
          ChangeVolume(-static_cast<int>(kVolumeStep));
        } else if (settings_cursor_ == 0) {
          OnSettingsExit();
        } else {
          settings_cursor_--;
        }
        Render();
      }
    }
    return;
  }

  // Outside settings.
  switch (ev.button) {
  case Button::kMenu:
    if (ev.event == Event::kPress) {
      AdvanceConv(+1);
      Render();
    } else if (ev.event == Event::kLongPressMenu) {
      OnSettingsEntry();
      Render();
    }
    break;
  case Button::kPtt:
    // PTT is handled by the Ptt state machine, not the UI.
    break;
  }
}

void UiState::OnSettingsEntry() {
  in_settings_ = true;
  settings_cursor_ = kSettingsChannel;
  screen_ = UiScreen::kSettings;
}

void UiState::OnSettingsExit() {
  in_settings_ = false;
  settings_cursor_ = 0;
  screen_ = UiScreen::kIdle;
}

void UiState::AdvanceConv(int delta) {
  if (!convs_ || convs_->empty()) {
    current_index_ = 0;
    scroll_pos_ = 0;
    return;
  }
  size_t n = convs_->size();
  long long cur = static_cast<long long>(current_index_) + delta;
  cur = ((cur % static_cast<long long>(n)) + static_cast<long long>(n)) %
        static_cast<long long>(n);
  current_index_ = static_cast<size_t>(cur);

  // Keep the active conv inside the visible 4-tab window.
  if (n <= kTabCount) {
    scroll_pos_ = 0;
  } else if (current_index_ < scroll_pos_) {
    scroll_pos_ = current_index_;
  } else if (current_index_ >= scroll_pos_ + kTabCount) {
    scroll_pos_ = current_index_ - kTabCount + 1;
  }
}

void UiState::ChangeVolume(int delta) {
  int v = static_cast<int>(volume_) + delta;
  if (v < kVolumeMin)
    v = kVolumeMin;
  if (v > kVolumeMax)
    v = kVolumeMax;
  volume_ = static_cast<uint8_t>(v);
}

void UiState::UpdateBattery() {
  low_battery_ = vbat_mv_ <= kLowBatteryMv;
  if (low_battery_ && screen_ == UiScreen::kIdle) {
    screen_ = UiScreen::kLowBattery;
  }
}

UiScreen UiState::Tick() {
  // The settings / battery checks happen on every tick. The
  // Ptt state is event-driven; Tick does not poll it.
  UpdateBattery();
  return screen_;
}

void UiState::Render() {
  if (!epd_) {
    return;
  }
  if (!epd_->IsControllerResponsiveForTest()) {
    // EPD driver hung: do nothing. The watchdog will reset
    // the M5 if this persists.
    return;
  }
  switch (screen_) {
  case UiScreen::kIdle:
    RenderIdle();
    break;
  case UiScreen::kRecording:
    RenderRecording();
    break;
  case UiScreen::kQueued:
    RenderQueued();
    break;
  case UiScreen::kTransmitting:
    RenderTransmitting();
    break;
  case UiScreen::kTtsPlayback:
    RenderTts();
    break;
  case UiScreen::kSettings:
    RenderSettings();
    break;
  case UiScreen::kLowBattery:
    RenderLowBattery();
    break;
  case UiScreen::kAcked:
    RenderIdle();
    break;
  }
}

void UiState::IssuePartial(const uint8_t *buf) {
  if (!epd_)
    return;
  if (partial_count_ >= kEpdFullRefreshEvery) {
    IssueFull(buf);
    return;
  }
  esp_err_t rc = epd_->PartialRefresh(buf);
  if (rc == ESP_OK) {
    ++partial_count_;
  }
}

void UiState::IssueFull(const uint8_t *buf) {
  if (!epd_)
    return;
  if (epd_->FullRefresh(buf) == ESP_OK) {
    partial_count_ = 0;
  }
}

// ── Per-screen renderers ──────────────────────────────────────────────

void UiState::RenderIdle() {
  if (!scratch_)
    return;
  IdleState s;
  s.channel = channel_;
  s.vbat_mv = vbat_mv_;
  s.low_battery = low_battery_;
  s.volume = volume_;
  s.current_index = current_index_;
  s.scroll_pos = scroll_pos_;
  if (convs_) {
    s.convs = *convs_;
  }
  ::tether::m5::RenderIdle(s, scratch_);
  IssuePartial(scratch_);
}

void UiState::RenderRecording() {
  if (!scratch_ || !ptt_)
    return;
  RecordingState s;
  s.elapsed_ms = 0; // not tracked yet; Phase 7 wires the timer
  s.peak_amplitude = 0;
  if (convs_ && !convs_->empty()) {
    s.conv = (*convs_)[current_index_ % convs_->size()];
  }
  ::tether::m5::RenderRecording(s, scratch_);
  IssueFull(scratch_); // recording screen is a full-bleed event
}

void UiState::RenderQueued() {
  if (!scratch_)
    return;
  QueuedState s;
  s.file_bytes = 0;
  s.enqueued_at_ms = 0;
  if (convs_ && !convs_->empty()) {
    s.conv = (*convs_)[current_index_ % convs_->size()];
  }
  ::tether::m5::RenderQueued(s, scratch_);
  IssueFull(scratch_);
}

void UiState::RenderTransmitting() {
  if (!scratch_)
    return;
  TransmittingState s;
  s.sent_chunks = 0;
  s.total_chunks = 0;
  s.acked_chunks = 0;
  s.elapsed_ms = 0;
  s.estimated_total_ms = 0;
  if (convs_ && !convs_->empty()) {
    s.conv = (*convs_)[current_index_ % convs_->size()];
  }
  ::tether::m5::RenderTransmitting(s, scratch_);
  IssueFull(scratch_);
}

void UiState::RenderTts() {
  if (!scratch_)
    return;
  TtsState s;
  s.elapsed_ms = 0;
  s.total_ms = 0;
  if (convs_ && !convs_->empty()) {
    s.conv = (*convs_)[current_index_ % convs_->size()];
  }
  ::tether::m5::RenderTtsPlayback(s, scratch_);
  IssueFull(scratch_);
}

void UiState::RenderSettings() {
  if (!scratch_)
    return;
  SettingsState s;
  s.channel = channel_;
  s.volume = volume_;
  s.vbat_mv = vbat_mv_;
  s.node_addr = 0x4A1F; // populated in Phase 8 from NVS
  s.modem = "SF11/BW125";
  s.cursor = settings_cursor_;
  ::tether::m5::RenderSettings(s, scratch_);
  IssuePartial(scratch_);
}

void UiState::RenderLowBattery() {
  if (!scratch_)
    return;
  LowBatteryState s;
  s.vbat_mv = vbat_mv_;
  s.critical = vbat_mv_ < 3200;
  ::tether::m5::RenderLowBattery(s, scratch_);
  IssueFull(scratch_);
}

} // namespace tether::m5
