// buttons.cpp — implementation of tether::m5::Buttons.
//
// The state machine debounces a raw GPIO input and fires semantic
// events. On real hardware the GPIO ISR (or the GPIO task that polls
// the pins) writes to `raw_pressed`; on host tests the
// SimulatePressForTest / SimulateReleaseForTest hooks do the same.
//
// Long-press fires only once per press, while the button is still
// held. The matching kRelease event is suppressed when a long-press
// has fired.

#include "buttons.h"

#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/gpio.h"
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.buttons";

// FreeRTOS task that calls Tick(period_ms) repeatedly.
void DebounceTaskEntry(void *arg) {
  auto *self = static_cast<Buttons *>(arg);
  constexpr uint32_t kPeriod = 10;
  while (self->IsDebounceTaskRunningForTask()) {
    self->Tick(kPeriod);
    vTaskDelay(pdMS_TO_TICKS(kPeriod));
  }
  vTaskDelete(nullptr);
}
} // namespace

Buttons::~Buttons() { StopDebounceTask(); }

bool Buttons::Init(EventHandler handler) {
  if (!handler)
    return false;
  handler_ = std::move(handler);
  for (auto &p : state_) {
    p = PinState{};
  }
  now_ms_ = 0;
  return true;
}

void Buttons::SimulatePressForTest(Button b) {
  size_t i = static_cast<size_t>(b);
  if (i >= 3)
    return;
  if (state_[i].raw_pressed != true) {
    state_[i].raw_pressed = true;
    state_[i].last_raw_change_at_ms = now_ms_;
    state_[i].pending_change = true;
  }
}

void Buttons::SimulateReleaseForTest(Button b) {
  size_t i = static_cast<size_t>(b);
  if (i >= 3)
    return;
  if (state_[i].raw_pressed != false) {
    state_[i].raw_pressed = false;
    state_[i].last_raw_change_at_ms = now_ms_;
    state_[i].pending_change = true;
  }
}

void Buttons::Tick(uint32_t elapsed_ms) {
  now_ms_ += elapsed_ms;
  for (size_t i = 0; i < 3; ++i) {
    auto &s = state_[i];
    if (s.raw_pressed != s.debounced_pressed) {
      // Pending change; check if it's settled.
      if (!s.pending_change) {
        // Internal mismatch (shouldn't happen) — settle immediately.
        s.debounced_pressed = s.raw_pressed;
      } else if ((now_ms_ - s.last_raw_change_at_ms) >= debounce_ms_) {
        // Settled.
        s.debounced_pressed = s.raw_pressed;
        s.pending_change = false;
        if (s.debounced_pressed) {
          s.press_started_at_ms = now_ms_;
          s.long_press_fired = false;
          if (handler_) {
            ButtonEvent ev{static_cast<Button>(i), Event::kPress};
            handler_(ev);
          }
        } else if (!s.long_press_fired) {
          if (handler_) {
            ButtonEvent ev{static_cast<Button>(i), Event::kRelease};
            handler_(ev);
          }
        }
      }
    } else if (s.debounced_pressed && !s.long_press_fired) {
      // Still held; check for long-press.
      uint32_t held_ms = now_ms_ - s.press_started_at_ms;
      bool should_fire_long = false;
      Event long_event = Event::kPress;
      if (i == static_cast<size_t>(Button::kPtt) && held_ms >= long_ptt_ms_) {
        should_fire_long = true;
        long_event = Event::kLongPressPtt;
      } else if (i == static_cast<size_t>(Button::kNext) &&
                 held_ms >= long_next_ms_) {
        should_fire_long = true;
        long_event = Event::kLongPressNext;
      }
      if (should_fire_long) {
        s.long_press_fired = true;
        if (handler_) {
          ButtonEvent ev{static_cast<Button>(i), long_event};
          handler_(ev);
        }
      }
    }
  }
}

bool Buttons::StartDebounceTask(uint32_t period_ms) {
  (void)period_ms;
  if (task_running_)
    return true;
  task_running_ = true;
  BaseType_t rc = xTaskCreate(DebounceTaskEntry, "buttons_debounce", 2048, this,
                              5, &task_handle_);
  if (rc != pdPASS) {
    task_running_ = false;
    return false;
  }
  return true;
}

void Buttons::StopDebounceTask() {
  if (!task_running_)
    return;
  task_running_ = false;
  // The task self-deletes when it sees task_running_ = false.
  task_handle_ = nullptr;
}

} // namespace tether::m5
