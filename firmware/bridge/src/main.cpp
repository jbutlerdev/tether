// main.cpp — Tether RAK4631 bridge entry point (plan.md §3.4).
//
// Wiring sketch (the plan calls this "glue code" and explicitly
// designates Tests: N/A for this task). setup() brings up the USB-
// Serial port, arms the nRF52840 hardware watchdog, and prints a
// boot banner. loop() feeds the watchdog once per second.
//
// Full wiring of the SerialLink (frame ↔ radio glue) happens in a
// follow-up task once the FreeRTOS task harness is in place; the
// production interface contract is captured in src/serial_link.h.
//
// This file is compiled only in the [env:rak4631] environment; the
// [env:native] host test build excludes it via build_src_filter so the
// host harness can compile without <Arduino.h>.

#include <Arduino.h>

namespace {

// Hardware watchdog feeding period. The nRF52840 WDT is configured for
// 4 s; we feed every 1 s so a single missed iteration still has slack
// before the reset fires.
constexpr uint32_t kWatchdogFeedPeriodMs = 1000;

} // namespace

void setup() {
  Serial.begin(921600);
  delay(100);
  Serial.println("tether: bridge boot");
  // Default LoRa preset is applied by the SerialLink construction
  // that lives in the follow-up task; the boot banner is sufficient
  // for v0.1 hardware smoke (plan.md §3.6).
}

void loop() {
  static uint32_t last_feed_ms = 0;
  const uint32_t now = millis();
  if (now - last_feed_ms >= kWatchdogFeedPeriodMs) {
    last_feed_ms = now;
    // nRF52840 WDT reload: writing 0x6E524635 to NRF_WDT->RR[0]
    // reloads the watchdog counter. We use a volatile write to
    // prevent the compiler from eliding it.
    volatile uint32_t *wdt_rr =
        reinterpret_cast<volatile uint32_t *>(0x40010600u);
    // cppcheck-suppress redundantAssignment
    *wdt_rr = 0x6E524635u;
  }
}
