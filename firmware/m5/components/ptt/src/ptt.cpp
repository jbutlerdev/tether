// ptt.cpp — implementation of tether::m5::Ptt.

#include "ptt.h"

#include <cstring>

namespace tether::m5 {

void Ptt::Transition(PttState s) {
  if (s == state_)
    return;
  PttState old = state_;
  state_ = s;
  if (state_change_) {
    state_change_(old, s);
  }
}

void Ptt::OnButton(ButtonEvent ev) {
  if (tts_active_) {
    // TTS playback suppresses PTT. Other buttons pass through.
    if (ev.button == Button::kPtt &&
        (ev.event == Event::kPress || ev.event == Event::kLongPressPtt)) {
      return;
    }
  }
  switch (state_) {
  case PttState::kIdle:
    if (ev.button == Button::kPtt && ev.event == Event::kPress) {
      Transition(PttState::kRecording);
    }
    break;
  case PttState::kRecording:
    if (ev.button == Button::kPtt) {
      if (ev.event == Event::kRelease) {
        Transition(PttState::kQueued);
      } else if (ev.event == Event::kLongPressPtt) {
        Transition(PttState::kIdle); // cancel during recording
      }
    }
    break;
  case PttState::kQueued:
    // No button response yet; the radio task drives the next state.
    break;
  case PttState::kTransmitting:
    if (ev.button == Button::kPtt && ev.event == Event::kLongPressPtt) {
      Transition(PttState::kCanceled);
    }
    break;
  case PttState::kAcked:
  case PttState::kCanceled:
  case PttState::kFailed:
    // No button response in terminal states; the UI task
    // transitions to IDLE after the user dismisses the screen.
    break;
  }
}

void Ptt::OnRadioAccepted() {
  if (state_ != PttState::kQueued)
    return;
  accepted_count_++;
  Transition(PttState::kTransmitting);
}

void Ptt::OnRadioAllAcked() {
  if (state_ != PttState::kTransmitting)
    return;
  acked_count_++;
  Transition(PttState::kAcked);
}

void Ptt::OnRadioFailed() {
  if (state_ != PttState::kTransmitting)
    return;
  failed_count_++;
  Transition(PttState::kFailed);
}

void Ptt::OnTtsStarted() { tts_active_ = true; }
void Ptt::OnTtsFinished() { tts_active_ = false; }

void Ptt::SetStateForTest(PttState s) { state_ = s; }

} // namespace tether::m5
