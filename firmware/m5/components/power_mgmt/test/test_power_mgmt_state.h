// test_power_mgmt_state.h — shared state for the power_mgmt
// unit tests. The original Phase 3 tests live in
// test_power_mgmt.cpp; the Phase 8 tests live in
// test_power_mgmt_phase8.cpp. Both share the singleton under
// test via this header.

#pragma once

#include "power_mgmt.h"

extern tether::m5::PowerMgmt *g_pm;
void ResetPowerMgmt();
