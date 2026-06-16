// buttons.h — Tether M5 button handling with debounce and long-press
// (plan.md §4.7).
//
// The ThinkNode M5 has **two** physical buttons (not three — see
// AGENTS.md §3.4 and the meshtastic variant.h at
// variants/esp32s3/ELECROW-ThinkNode-M5/variant.h, which defines
// PIN_BUTTON1=21 and PIN_BUTTON2=14). The third "control" on the M5
// is a *switch* (slider) for the GPS module, not a button. See
// board.h.
//
//   A (front, large, GPIO 21) — **PTT**: push to record, release to
//     enqueue + transmit. Long-press (≥ 3 s) is a v0.2.0 feature
//     ("settings entry" in earlier designs is now B's long-press).
//
//   B (side, GPIO 14) — **Menu / cycle**: short press cycles to the
//     next conversation. Long-press (≥ 2 s) enters the settings menu;
//     inside the settings menu, a long-press exits.
//
//   GPS_SWITCH (GPIO 10, slider) — not a button; the system architect
//     chose GPIO 10 for SD CS on the M5 because the GPS switch is
//     wired to a *different physical pad* (a header pin, not a GPIO).
//     The switch's state is exposed via kPinGpsSwitch in board.h
//     if future code needs to know.
//
// Each button is a GPIO input that fires an edge interrupt on
// press/release. A debounce task (FreeRTOS, low priority) consumes
// the events from a queue and emits semantic Button events:
//
//   kPress                — clean press, debounced
//   kRelease              — clean release
//   kLongPressPtt         — PTT held ≥ 3 s
//   kLongPressMenu        — Menu/Cycle held ≥ 2 s
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
  kMenu = 1,
};

// Backwards-compat alias. The original design used kNext for the
// second button; some call sites still use that name. kNext is
// semantically identical to kMenu.
constexpr Button kNext = Button::kMenu;

enum class Event : uint8_t {
  kPress = 0,
  kRelease = 1,
  kLongPressPtt = 2,
  kLongPressMenu = 3,
};

constexpr Event kLongPressNext = Event::kLongPressMenu;

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

  // Internal state accessor used by the debounce task. Not part of
  // the public API; do not call from user code.
  bool IsDebounceTaskRunningForTask() const { return task_running_; }

private:
  // Per-button state. The M5 has 2 physical buttons.
  struct PinState {
    bool raw_pressed = false;
    bool debounced_pressed = false;
    uint32_t last_raw_change_at_ms = 0;
    uint32_t press_started_at_ms = 0;
    bool long_press_fired = false;
    bool pending_change = false;
  };

  void TickOneButton(size_t i, uint32_t now_ms_);
  bool StartDebounceTask(uint32_t period_ms);
  void StopDebounceTask();

  static constexpr size_t kButtonCount = 2;
  std::array<PinState, kButtonCount> state_{};
  EventHandler handler_;

  // Test-only timing thresholds. Production: 10 ms debounce,
  // 3000 ms PTT long-press, 2000 ms Menu long-press.
  uint32_t debounce_ms_ = 10;
  uint32_t long_ptt_ms_ = 3000;
  uint32_t long_next_ms_ = 2000; // alias for "long menu"

  uint32_t now_ms_ = 0;
  bool task_running_ = false;
  TaskHandle_t task_handle_ = nullptr;
};

} // namespace tether::m5
