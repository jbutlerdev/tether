// power_mgmt.h — Tether M5 power management task (plan.md §4.8.7).
//
// The power_mgmt task tracks idle time and decides when to enter
// light or deep sleep. On real hardware it calls esp_sleep_enable_*
// functions; on host tests the state is exposed via a small state
// machine so tests can verify the timing.

#pragma once

#include <cstdint>

namespace tether::m5 {

enum class PowerState : uint8_t {
  kActive = 0,
  kLightSleep = 1,
  kDeepSleep = 2,
};

class PowerMgmt {
 public:
  PowerMgmt() = default;

  // Initialize with thresholds. Defaults: light sleep after 5 s idle,
  // deep sleep after 30 s idle.
  void Init(uint32_t light_sleep_ms = 5000, uint32_t deep_sleep_ms = 30000);

  // Notify the power task of activity (button press, RX packet, etc.).
  // Resets the idle timer.
  void NotifyActivity();

  // Pump the state machine. Returns the new power state.
  PowerState Tick(uint32_t elapsed_ms);

  PowerState State() const { return state_; }
  uint32_t IdleMs() const { return idle_ms_; }

  // Wake sources (real hardware): PTT, RTC timer, RX packet.
  // On host these are flags that tests can set.
  void SetWakeSourcePttForTest(bool enabled) { wake_ptt_ = enabled; }
  void SetWakeSourceTimerForTest(bool enabled) { wake_timer_ = enabled; }
  void SetWakeSourceRxForTest(bool enabled) { wake_rx_ = enabled; }

 private:
  PowerState state_ = PowerState::kActive;
  uint32_t idle_ms_ = 0;
  uint32_t light_threshold_ms_ = 5000;
  uint32_t deep_threshold_ms_ = 30000;
  bool wake_ptt_ = true;
  bool wake_timer_ = true;
  bool wake_rx_ = true;
};

}  // namespace tether::m5
