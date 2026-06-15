// power_mgmt.h — Tether M5 power management task (plan.md §4.8.7,
// §9.5).
//
// The power_mgmt task tracks idle time and decides when to enter
// light or deep sleep. On real hardware it calls esp_sleep_enable_*
// functions; on host tests the state is exposed via a small state
// machine so tests can verify the timing.
//
// Phase 8 (plan §9.5) adds:
//   * Peripheral gating — a bitmask of which subsystems are
//     powered up. Deep sleep is blocked until every peripheral is
//     gated off. This is the structural guarantee that the bench
//     < 50 µA number is a design property, not a hopeful estimate.
//   * Wake source configuration — three independent enables
//     (PTT / RX packet / RTC timer) plus a WakeOnPtt() helper
//     that configures the ESP32-S3 ext0 wakeup on the PTT pin.
//   * Battery life model — a closed-form estimate given the duty
//     cycle, battery capacity, and active/sleep currents. We use
//     it to verify the 6-hour target without bench testing.

#pragma once

#include <cstdint>

namespace tether::m5 {

enum class PowerState : uint8_t {
  kActive = 0,
  kLightSleep = 1,
  kDeepSleep = 2,
};

// Peripheral identifies a power-gated subsystem on the M5. The
// values are bit positions in a uint32_t mask so callers can
// OR-merge them when more than one peripheral is on.
enum class Peripheral : uint32_t {
  kLora = 0x01,
  kI2SAmp = 0x02,
  kI2SMic = 0x04,
  kEpd = 0x08,
  kSd = 0x10,
  kAllMask = 0x1F,
};

// WakeSource enumerates the possible causes of waking from deep
// sleep. The M5 has exactly three (PTT GPIO, LoRa RX packet via
// the SX1262 DIO pin, and the RTC timer for periodic beacons).
enum class WakeSource : uint8_t {
  kPtt = 0,
  kRxPacket = 1,
  kRtcTimer = 2,
};

// BatteryEstimate is the result of EstimateBatteryLifeHours. The
// `is_feasible_6h_target` flag is a convenience: the operator
// TUI (plan §10.1) renders it as a green check or red X.
struct BatteryEstimate {
  float hours = 0;
  bool is_feasible_6h_target = false;
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

  // ── Phase 8: peripheral gating ────────────────────────────────────
  //
  // GateOff removes a peripheral from the powered-up set. The
  // production implementation calls the appropriate low-power
  // function (esp_wifi_stop, sd_card_unmount, etc.). The host
  // build just records the state.
  void GateOff(Peripheral p);
  void GateOn(Peripheral p);
  bool IsPeripheralOn(Peripheral p) const;

  // AllGatedOff is true when every peripheral in the
  // Peripheral::kAllMask set is gated off. The deep-sleep path
  // checks this before transitioning; a powered-up peripheral
  // would draw far more than the 50 µA target, so the check
  // is structural (not advisory).
  bool AllGatedOff() const;

  // ── Phase 8: wake sources ─────────────────────────────────────────
  //
  // Three independent enables. The default is "all enabled";
  // the production firmware may disable kRtcTimer if no
  // periodic beacon is configured (v2 feature, plan §4.8.7).
  void EnableWakeSource(WakeSource s);
  void DisableWakeSource(WakeSource s);
  bool IsWakeSourceEnabled(WakeSource s) const;

  // WakeOnPtt is the high-level helper for "configure the
  // ESP32-S3 ext0 wakeup on the PTT pin". On real hardware
  // this calls esp_sleep_enable_gpio_wakeup; on host it just
  // enables the WakeSource::kPtt flag.
  void WakeOnPtt();

  // ── Phase 8: battery life model ───────────────────────────────────
  //
  // EstimateBatteryLifeHours returns a closed-form estimate of
  // battery life given the duty cycle and the battery /
  // current parameters. The math is:
  //
  //   avg_current_mA = active_mA * duty + sleep_mA * (1 - duty)
  //   hours = capacity_mAh / avg_current_mA
  //
  // The 6-hour target is checked: is_feasible_6h_target is true
  // iff hours >= 6.0.
  BatteryEstimate EstimateBatteryLifeHours(float duty_cycle,
                                          float capacity_mah,
                                          float active_current_ma,
                                          float sleep_current_ma) const;

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

  // Phase 8 state.
  uint32_t peripherals_on_ = static_cast<uint32_t>(Peripheral::kAllMask);
};

} // namespace tether::m5
