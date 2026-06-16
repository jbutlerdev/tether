// buttons.h — Tether M5 button handling with debounce and long-press
// (plan.md §4.7).
//
// The M5 has three physical buttons (A=PTT, B=Next, C=Prev). Each is
// a GPIO input that fires an edge interrupt on press/release. A
// debounce task (FreeRTOS, low priority) consumes the events from a
// queue and emits semantic Button events:
//
//   kPress                — clean press, debounced
//   kRelease              — clean release
//   kLongPressPtt         — PTT held ≥ 3 s
//   kLongPressNext        — Next held ≥ 2 s (settings)
//
// Long-press fires while the button is still held; the matching
// kRelease event is suppressed.

#pragma once

#include <cstdint>
#include <functional>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#endif

namespace tether::m5 {

enum class Button : uint8_t {
  kPtt = 0,
  kNext = 1,
  kPrev = 2,
};

enum class Event : uint8_t {
  kPress = 0,
  kRelease = 1,
  kLongPressPtt = 2,
  kLongPressNext = 3,
};

struct ButtonEvent {
  Button button;
  Event event;
};

// Callbacks receive the ButtonEvent directly. We use std::function so
// tests can capture state; on real hardware a small adapter could
// forward to a C-style callback if needed.
using EventHandler = std::function<void(ButtonEvent)>;

class Buttons {
public:
  Buttons() = default;
  ~Buttons();

  Buttons(const Buttons &) = delete;
  Buttons &operator=(const Buttons &) = delete;

  // Install `handler` to receive debounced events. Replaces any prior
  // handler. Returns true on success.
  bool Init(EventHandler handler);

  // Test seams: simulate a press/release on a button. Host tests use
  // these to drive the state machine without a real GPIO line.
  void SimulatePressForTest(Button b);
  void SimulateReleaseForTest(Button b);

  // Pump the debounce + long-press state machine. Tests call this
  // after a sequence of SimulateXxx() calls to advance time. On real
  // hardware the debounce task runs in its own FreeRTOS loop and
  // calls Tick() internally every debounce_ticks_.
  void Tick(uint32_t elapsed_ms);

  // Configure debounce / long-press thresholds. Tests use these to
  // shrink the timings so they run fast.
  void SetDebounceMsForTest(uint32_t ms) { debounce_ms_ = ms; }
  void SetLongPressPttMsForTest(uint32_t ms) { long_ptt_ms_ = ms; }
  void SetLongPressNextMsForTest(uint32_t ms) { long_next_ms_ = ms; }

  // Start the debounce FreeRTOS task. The task wakes every
  // `period_ms` (default 10 ms) and calls Tick().
  bool StartDebounceTask(uint32_t period_ms = 10);

  // Stop the debounce task. Idempotent.
  void StopDebounceTask();

  // Internal access for the debounce task entry point.
  bool IsDebounceTaskRunningForTask() const { return task_running_; }

private:
  EventHandler handler_;
  // For each button: last raw state, last debounce time.
  struct PinState {
    bool raw_pressed = false;
    bool debounced_pressed = false;
    bool long_press_fired = false;
    bool pending_change = false; // true if raw_pressed != debounced_pressed
    uint32_t press_started_at_ms = 0;
    uint32_t last_raw_change_at_ms = 0;
  };
  PinState state_[3] = {};
  uint32_t debounce_ms_ = 30;
  uint32_t long_ptt_ms_ = 3000;
  uint32_t long_next_ms_ = 2000;
  uint32_t now_ms_ = 0;
  TaskHandle_t task_handle_ = nullptr;
  bool task_running_ = false;
};

} // namespace tether::m5
