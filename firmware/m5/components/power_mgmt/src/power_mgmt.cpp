// power_mgmt.cpp — implementation of tether::m5::PowerMgmt.
#include "power_mgmt.h"

namespace tether::m5 {

void PowerMgmt::Init(uint32_t light_sleep_ms, uint32_t deep_sleep_ms) {
  light_threshold_ms_ = light_sleep_ms;
  deep_threshold_ms_ = deep_sleep_ms;
}

void PowerMgmt::NotifyActivity() { idle_ms_ = 0; }

PowerState PowerMgmt::Tick(uint32_t elapsed_ms) {
  idle_ms_ += elapsed_ms;
  if (idle_ms_ >= deep_threshold_ms_) {
    state_ = PowerState::kDeepSleep;
  } else if (idle_ms_ >= light_threshold_ms_) {
    state_ = PowerState::kLightSleep;
  } else {
    state_ = PowerState::kActive;
  }
  return state_;
}

} // namespace tether::m5
