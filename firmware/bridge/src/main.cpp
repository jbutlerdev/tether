// Empty Tether RAK4631 bridge sketch. Phase 0 skeleton.
// See plan.md §1.1.
#include <Arduino.h>

void setup() {
  Serial.begin(921600);
  delay(1000);
}

void loop() {
  // Phase 0: idle. Subsequent phases add LoRa RX + USB-Serial framing.
}
