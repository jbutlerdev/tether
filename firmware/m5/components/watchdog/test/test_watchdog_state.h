// test_watchdog_state.h — shared state for the watchdog unit
// tests. The original Phase 3 tests live in test_watchdog.cpp;
// the Phase 8 reset-reason tests live in
// test_watchdog_reset.cpp. Both translation units need to
// share the singleton Watchdog under test, so the declaration
// lives in a small header that is included by both.

#pragma once

#include "watchdog.h"

extern tether::m5::Watchdog *g_wdt;
void ResetWatchdog();
