// pca9557.h — Tether M5 PCA9557 I/O expander driver.
//
// The M5 has a TI PCA9557PW I2C GPIO expander on Wire1
// (GPIO 47/48 on the ESP32-S3, I2C address 0x18). It drives:
//
//   pin 0 — (unused / not connected on the M5 schematic)
//   pin 1 — Blue LED (notification: TX, RX, recording)
//   pin 2 — LED power supply (OR'd with VBUS; default ON)
//   pin 3 — Red LED (charging / power; hardware-OR'd with USB)
//   pin 4 — Master peripheral power enable (eink + GPS + LoRa +
//            sensor). When LOW, all peripherals are unpowered.
//   pin 5 — E-ink backlight power enable
//   pin 6 — (unused)
//   pin 7 — (unused)
//
// Reference: meshtastic/firmware variants/esp32s3/ELECROW-ThinkNode-M5/
// variant.h and variant.cpp.
//
// All Tether code that needs to drive the LEDs, the E-ink
// backlight, or the master peripheral power rail goes through
// this component.

#pragma once

#include <cstdint>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/gpio.h"
#include "esp_err.h"
#endif

namespace tether::m5 {

class Pca9557 {
public:
  Pca9557() = default;
  ~Pca9557() = default;

  Pca9557(const Pca9557 &) = delete;
  Pca9557 &operator=(const Pca9557 &) = delete;

  // Initialize the I2C1 bus and the PCA9557. All output pins are
  // configured as outputs; all input pins as inputs. Returns true
  // on success.
  bool Init();

  // LED controls. The "notification" LED is blue; the "power" LED
  // is red. The LED power rail (pin 2) is OR'd with VBUS by
  // hardware, so the red LED illuminates when USB is plugged in
  // regardless of what we drive on pin 3 — the firmware can
  // additionally drive pin 3 high to force the red LED on.
  void SetLedNotification(bool on);
  void SetLedPower(bool on);
  void SetLedPowerRail(bool on);

  // E-ink backlight power (pin 5). When LOW (the default after
  // Init), the backlight is off. Setting it HIGH enables the
  // backlight — this also pulls significant current (~30 mA) so
  // it's typically only on while the screen is being read.
  void SetEinkBacklight(bool on);

  // Master peripheral power rail (pin 4). When LOW, all the
  // downstream peripherals (eink, GPS, LoRa, sensor) are
  // unpowered. This is the master power-gate used by power_mgmt
  // to enter deep sleep. The default after Init() is HIGH
  // (peripherals powered).
  //
  // WARNING: setting this LOW while the firmware is running will
  // cut power to the LoRa radio and the EPD. The power_mgmt and
  // watchdog components coordinate this with deep-sleep entry and
  // fault recovery; do not call this from arbitrary code.
  void SetPeripheralPower(bool on);

  // Test seam: reset all output pins to a known state.
  void ResetForTest();

private:
  // Single-shot write of a pin's output state. Returns true on
  // success.
  bool WritePin(uint8_t pin, bool high);

  // Cached output register. We re-read it from the chip on Init
  // and update it on every WritePin so the cache stays consistent
  // if some other code on the I2C bus mutates the chip.
  uint8_t output_cache_ = 0xFF; // all pins high-Z (PCA9557 default)
  bool initialized_ = false;
};

} // namespace tether::m5
