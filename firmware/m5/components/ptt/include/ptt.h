// ptt.h — Tether M5 PTT state machine (plan.md §4.8.1).
//
// The PTT state machine tracks the user-visible state of the M5: idle,
// recording, queued (audio flushed to SD), transmitting, acked, and
// the error / cancel paths. The states form a small DAG:
//
//   IDLE -> RECORDING -> QUEUED -> TRANSMITTING -> ACKED -> IDLE
//                            |          |
//                            v          v
//                          IDLE      CANCELED
//                       (3s hold)   (3s hold during TX)
//
// The state machine is driven by:
//   * button events (PTT press / release / long-press)
//   * signals from the radio task (transmission accepted, all acked,
//     retry budget exceeded, TTS playback active).
//
// On real hardware the PTT task is its own FreeRTOS task that owns
// the state and pumps a queue of events. On host the state is exposed
// for direct manipulation; tests call OnXxx() methods and assert on
// State().

#pragma once

#include <cstdint>
#include <functional>
#include <optional>

#include "buttons.h"

namespace tether::m5 {

enum class PttState : uint8_t {
  kIdle = 0,
  kRecording = 1,
  kQueued = 2,       // audio flushed to SD, awaiting radio
  kTransmitting = 3, // radio task is sending
  kAcked = 4,        // all chunks acked
  kCanceled = 5,     // user canceled mid-TX
  kFailed = 6,       // retry budget exceeded
};

inline const char *PttStateName(PttState s) {
  switch (s) {
    case PttState::kIdle: return "Idle";
    case PttState::kRecording: return "Recording";
    case PttState::kQueued: return "Queued";
    case PttState::kTransmitting: return "Transmitting";
    case PttState::kAcked: return "Acked";
    case PttState::kCanceled: return "Canceled";
    case PttState::kFailed: return "Failed";
  }
  return "?";
}

class Ptt {
 public:
  using StateChangeHandler = std::function<void(PttState old_s, PttState new_s)>;

  Ptt() = default;

  // Install a state-change observer. The handler is invoked from
  // OnXxx() before the method returns. Returns the previous handler
  // (or nullptr if none).
  StateChangeHandler OnStateChange(StateChangeHandler h) {
    StateChangeHandler prev = std::move(state_change_);
    state_change_ = std::move(h);
    return prev;
  }

  PttState State() const { return state_; }
  const char *StateName() const { return PttStateName(state_); }

  // Drive the state machine from button events.
  void OnButton(ButtonEvent ev);

  // Drive from radio task signals.
  void OnRadioAccepted();
  void OnRadioAllAcked();
  void OnRadioFailed();
  void OnTtsStarted();
  void OnTtsFinished();

  // Test seam: force a state transition. Bypasses validation; used
  // by tests that need to set up a starting state directly.
  void SetStateForTest(PttState s);

  // Counter of how many times OnRadioAccepted() succeeded.
  int AcceptedCount() const { return accepted_count_; }
  int AllAckedCount() const { return acked_count_; }
  int FailedCount() const { return failed_count_; }

 private:
  void Transition(PttState s);

  PttState state_ = PttState::kIdle;
  bool tts_active_ = false;
  int accepted_count_ = 0;
  int acked_count_ = 0;
  int failed_count_ = 0;
  StateChangeHandler state_change_;
};

}  // namespace tether::m5
