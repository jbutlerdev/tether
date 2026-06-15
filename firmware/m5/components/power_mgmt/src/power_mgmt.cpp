// power_mgmt.cpp — implementation of tether::m5::PowerMgmt.
#include "power_mgmt.h"

#include <cstdio>

namespace tether::m5 {

void PowerMgmt::Init(uint32_t light_sleep_ms, uint32_t deep_sleep_ms) {
  light_threshold_ms_ = light_sleep_ms;
  deep_threshold_ms_ = deep_sleep_ms;
}

void PowerMgmt::NotifyActivity() { idle_ms_ = 0; }

PowerState PowerMgmt::Tick(uint32_t elapsed_ms) {
  idle_ms_ += elapsed_ms;
  if (idle_ms_ >= deep_threshold_ms_ && AllGatedOff()) {
    // Deep sleep is only reachable when every peripheral is
    // gated off. If a peripheral is still powered up (e.g.
    // the EPD is mid-refresh) we cap at light sleep and
    // try again on the next Tick.
    state_ = PowerState::kDeepSleep;
  } else if (idle_ms_ >= light_threshold_ms_) {
    state_ = PowerState::kLightSleep;
  } else {
    state_ = PowerState::kActive;
  }
  return state_;
}

// ── Phase 8: peripheral gating ──────────────────────────────────────

void PowerMgmt::GateOff(Peripheral p) {
  peripherals_on_ &= ~static_cast<uint32_t>(p);
}

void PowerMgmt::GateOn(Peripheral p) {
  peripherals_on_ |= static_cast<uint32_t>(p);
}

bool PowerMgmt::IsPeripheralOn(Peripheral p) const {
  return (peripherals_on_ & static_cast<uint32_t>(p)) != 0;
}

bool PowerMgmt::AllGatedOff() const {
  return (peripherals_on_ & static_cast<uint32_t>(Peripheral::kAllMask)) == 0;
}

// ── Phase 8: wake sources ────────────────────────────────────────────

void PowerMgmt::EnableWakeSource(WakeSource s) {
  switch (s) {
  case WakeSource::kPtt:
    wake_ptt_ = true;
    break;
  case WakeSource::kRxPacket:
    wake_rx_ = true;
    break;
  case WakeSource::kRtcTimer:
    wake_timer_ = true;
    break;
  }
}

void PowerMgmt::DisableWakeSource(WakeSource s) {
  switch (s) {
  case WakeSource::kPtt:
    wake_ptt_ = false;
    break;
  case WakeSource::kRxPacket:
    wake_rx_ = false;
    break;
  case WakeSource::kRtcTimer:
    wake_timer_ = false;
    break;
  }
}

bool PowerMgmt::IsWakeSourceEnabled(WakeSource s) const {
  switch (s) {
  case WakeSource::kPtt:
    return wake_ptt_;
  case WakeSource::kRxPacket:
    return wake_rx_;
  case WakeSource::kRtcTimer:
    return wake_timer_;
  }
  return false;
}

void PowerMgmt::WakeOnPtt() {
  EnableWakeSource(WakeSource::kPtt);
  // On real hardware: esp_sleep_enable_gpio_wakeup();
  // + gpio_wakeup_enable(GPIO_NUM_xx, GPIO_INTR_LOW_LEVEL);
  // The host build just records the state.
}

// ── Phase 8: battery life model ──────────────────────────────────────

BatteryEstimate PowerMgmt::EstimateBatteryLifeHours(float duty_cycle,
                                                   float capacity_mah,
                                                   float active_current_ma,
                                                   float sleep_current_ma) const {
  BatteryEstimate est;
  if (duty_cycle < 0.0f) duty_cycle = 0.0f;
  if (duty_cycle > 1.0f) duty_cycle = 1.0f;
  float avg = active_current_ma * duty_cycle +
              sleep_current_ma * (1.0f - duty_cycle);
  if (avg <= 0.0f) {
    est.hours = 0.0f;
    est.is_feasible_6h_target = false;
    return est;
  }
  est.hours = capacity_mah / avg;
  est.is_feasible_6h_target = (est.hours >= 6.0f);
  return est;
}

} // namespace tether::m5
