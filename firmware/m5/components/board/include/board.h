// board.h — Tether M5 (ThinkNode M5 / ELECROW) board pin map.
//
// Source of truth: meshtastic/firmware variants/esp32s3/ELECROW-ThinkNode-M5/
// variant.h (github.com/meshtastic/firmware, branch develop). When
// this disagrees with the variant.h, the variant.h wins on the
// ELECROW hardware side — the schematic is the schematic — but the
// *allocation* (which pin drives which Tether function) is a
// Tether design decision and is documented below.
//
// ESP-IDF version: v5.2.2 (CI is pinned to this exact tag in
// .github/workflows/{ci.yml,firmware-build.yml}). GPIO 40–48 are
// only defined in soc/esp32s3/include/soc/gpio_num.h starting in
// v5.2.2; earlier v5.2.x patches (v5.2.0, v5.2.1) only define up to
// GPIO_NUM_39.

// ────────────────────────────────────────────────────────────────────
// HARDWARE-MOD REQUIRED
// ────────────────────────────────────────────────────────────────────
// The Tether audio path requires 4 GPIOs for a single full-duplex
// I2S0 bus. The stock M5 has only ONE natively free pin (GPIO 18).
// To free 3 more, two hardware modifications are required before
// the firmware is flashed. See docs/HARDWARE-MODS.md for the full
// execution plan with photos. The summary:
//
//   1. GPS module removal — desolder the Quectel L76K GPS module
//      (LCC, 9.7×10.1 mm, 18 pins) from the M5 board. With the
//      module gone, GPIOs 10, 11, 13, 19, and 20 are freed (they
//      were the GPS slider sense, standby, reinit, RX, and TX
//      respectively). GPIOs 19 and 20 are claimed for I2S0 below;
//      the rest stay unused. Compared to the v0.1.3 GPS "Always-On"
//      hack, this removes the ~25 mA continuous drain of the GPS
//      being powered forever, and it gives us enough free pins
//      that we do NOT need to cut the VBUS-detect trace.
//
//   2. Buzzer removal — desolder the physical buzzer from the M5
//      board. GPIO 9 (the buzzer PWM pin) is freed for I2S0 DOUT
//      (amp DIN).
//
// No trace cuts are required. In particular, GPIO 12 (EXT_PWR_DETECT,
// the USB VBUS sense line) is left intact — the firmware can read
// USB plug state normally.
//
// GPIOs 33 and 34 are part of the OCTAL PSRAM bus and MUST NOT be
// used. Touching them crashes the PSRAM controller. The mic/amp
// pin map avoids them by design.
//
// After the two mods the I2S0 bus is wired full-duplex:
//
//   WS (LRC)  : GPIO 19  (shared between mic WS and amp LRC;
//                         freed by GPS removal — was GPS L76K RX)
//   BCLK (SCK): GPIO 20  (shared between mic SCK and amp BCLK;
//                         freed by GPS removal — was GPS L76K TX)
//   Mic SD    : GPIO 18  (I2S0 DIN — data from mic; natively free)
//   Amp DIN   : GPIO 9   (I2S0 DOUT — data to amp; freed by
//                         buzzer removal)
//
// Both devices run on the same SCK/WS pair, so the mic and amp
// share bit-clock and word-select timing exactly. This is the
// standard full-duplex I2S topology used by codecs with separate
// ADC and DAC channels.

#pragma once

#ifdef TETHER_M5_HOST_TEST
// On the host build we don't have ESP-IDF's driver/gpio.h. Provide
// a minimal shim that defines the GPIO_NUM_N constants used by
// board.h so the host build can compile.
#include <cstdint>
using gpio_num_t = int; // host shim
#define GPIO_NUM_NC (-1)
#define GPIO_NUM_0 0
#define GPIO_NUM_1 1
#define GPIO_NUM_2 2
#define GPIO_NUM_3 3
#define GPIO_NUM_4 4
#define GPIO_NUM_5 5
#define GPIO_NUM_6 6
#define GPIO_NUM_7 7
#define GPIO_NUM_8 8
#define GPIO_NUM_9 9
#define GPIO_NUM_10 10
#define GPIO_NUM_11 11
#define GPIO_NUM_12 12
#define GPIO_NUM_13 13
#define GPIO_NUM_14 14
#define GPIO_NUM_15 15
#define GPIO_NUM_16 16
#define GPIO_NUM_17 17
#define GPIO_NUM_18 18
#define GPIO_NUM_19 19
#define GPIO_NUM_20 20
#define GPIO_NUM_21 21
#define GPIO_NUM_33 33
#define GPIO_NUM_34 34
#define GPIO_NUM_35 35
#define GPIO_NUM_36 36
#define GPIO_NUM_37 37
#define GPIO_NUM_38 38
#define GPIO_NUM_39 39
#define GPIO_NUM_40 40
#define GPIO_NUM_41 41
#define GPIO_NUM_42 42
#define GPIO_NUM_43 43
#define GPIO_NUM_44 44
#define GPIO_NUM_45 45
#define GPIO_NUM_46 46
#define GPIO_NUM_47 47
#define GPIO_NUM_48 48
#else
#include "driver/gpio.h"
#endif

namespace tether::m5::board {

// ── LoRa SX1262 ──────────────────────────────────────────────────────
//
// The M5's onboard SX1262 has fixed wiring. Don't move these.
constexpr gpio_num_t kPinLoraCs = GPIO_NUM_17;   // SPI CS
constexpr gpio_num_t kPinLoraSck = GPIO_NUM_16;  // SPI SCK (shared bus)
constexpr gpio_num_t kPinLoraMosi = GPIO_NUM_15; // SPI MOSI (shared bus)
constexpr gpio_num_t kPinLoraMiso = GPIO_NUM_7;  // SPI MISO (shared bus)
constexpr gpio_num_t kPinLoraReset = GPIO_NUM_6;
constexpr gpio_num_t kPinLoraBusy = GPIO_NUM_5;
constexpr gpio_num_t kPinLoraDio1 = GPIO_NUM_4;
constexpr gpio_num_t kPinLoraPowerEn = GPIO_NUM_46;

// ── Shared SPI bus (LoRa + EPD + SD) ─────────────────────────────────
constexpr gpio_num_t kPinSpiSck = kPinLoraSck;   // GPIO 16
constexpr gpio_num_t kPinSpiMosi = kPinLoraMosi; // GPIO 15
constexpr gpio_num_t kPinSpiMiso = kPinLoraMiso; // GPIO 7

// ── EPD (1.54″) ──────────────────────────────────────────────────────
constexpr gpio_num_t kPinEpdCs = GPIO_NUM_39;
constexpr gpio_num_t kPinEpdBusy = GPIO_NUM_42;
constexpr gpio_num_t kPinEpdDc = GPIO_NUM_40;
constexpr gpio_num_t kPinEpdRes = GPIO_NUM_41;
constexpr gpio_num_t kPinEpdSclk = GPIO_NUM_38;
constexpr gpio_num_t kPinEpdMosi = GPIO_NUM_45;
// The E-ink's backlight power is gated through the PCA9557
// (PCA_PIN_EINK_EN, expander pin 5). See firmware/m5/components/
// pca9557/include/pca9557.h.

// ── SD card (over the shared SPI bus) ────────────────────────────────
constexpr gpio_num_t kPinSdCs = GPIO_NUM_10;

// ── I2S0 — shared full-duplex audio bus (mic + amp) ─────────────────
//
// REQUIRES THE 2 HARDWARE MODS DESCRIBED AT THE TOP OF THIS FILE.
// Without them, GPIO 9 (buzzer) is not available and GPIOs 19/20
// are still wired to the (now-removed) GPS module's UART, and
// this bus cannot be wired.
//
// The mic (INMP441) and the amp (MAX98357A) share the BCLK and WS
// signals; the mic drives its SD line into the ESP32's DIN, and the
// amp reads the ESP32's DOUT. This is the standard full-duplex I2S
// topology; the ESP32-S3's I2S0 peripheral can drive both
// directions simultaneously.
constexpr gpio_num_t kPinI2sWs = GPIO_NUM_19;   // Word Select (LRC; was GPS L76K RX)
constexpr gpio_num_t kPinI2sBclk = GPIO_NUM_20; // Bit Clock (SCK; was GPS L76K TX)
constexpr gpio_num_t kPinI2sDin = GPIO_NUM_18;  // Data In: from mic
constexpr gpio_num_t kPinI2sDout = GPIO_NUM_9;  // Data Out: to amp

// ── Buttons (the M5 has exactly two; see §buttons below) ─────────────
constexpr gpio_num_t kPinButtonPtt = GPIO_NUM_21;  // PIN_BUTTON1
constexpr gpio_num_t kPinButtonMenu = GPIO_NUM_14; // PIN_BUTTON2
constexpr uint32_t kButtonActiveLow = 0;           // GPIO low = pressed

// ── Power / battery / buzzer (mostly sacrificed) ─────────────────────
//
// GPIO 9 (kPinI2sDout) was the buzzer. Desoldered for I2S0 DOUT.
// GPIO 19 (kPinI2sWs) and GPIO 20 (kPinI2sBclk) were the GPS
// L76K's UART. Freed by removing the GPS module entirely.
// GPIO 12 (kPinExtPwrDetect) is INTACT — we keep the USB VBUS
// sense line because GPIO 19/20 absorb the WS/BCLK requirement.
constexpr gpio_num_t kPinBuzzer = GPIO_NUM_9;        // now = I2S DOUT
constexpr gpio_num_t kPinExtPwrDetect = GPIO_NUM_12; // still VBUS detect
// Battery ADC is still on GPIO 8 (no conflict).
constexpr gpio_num_t kPinBatteryAdc = GPIO_NUM_8; // ADC channel 7

// ── USB-Serial (RAK4631 bridge on the M5) ───────────────────────────
constexpr gpio_num_t kPinUartTx = GPIO_NUM_43;
constexpr gpio_num_t kPinUartRx = GPIO_NUM_44;

// ── I2C buses ────────────────────────────────────────────────────────
//
// I2C0 (Wire) is the bus for the PCF8563 RTC. We don't use the RTC
// in v0.1.0, so Wire is uninitialized; the pads are physically
// available.
//
// I2C1 (Wire1) is the bus to the PCA9557 GPIO expander that drives
// the LEDs, the E-ink backlight power, and the master peripheral
// power rail. We use this in v0.1.0 — see firmware/m5/components/
// pca9557/. GPIO 47/48 are NOT "free for I2S1" as the v0.1.2
// comment said: they are claimed by Wire1.
constexpr gpio_num_t kPinI2c0Scl = GPIO_NUM_1;
constexpr gpio_num_t kPinI2c0Sda = GPIO_NUM_2;
constexpr gpio_num_t kPinI2c1Scl = GPIO_NUM_48; // Wire1
constexpr gpio_num_t kPinI2c1Sda = GPIO_NUM_47; // Wire1

constexpr uint8_t kPca9557I2cAddr = 0x18; // 7-bit address (no R/W bit)

// ── Pins explicitly reserved / do-not-touch ─────────────────────────
//
// GPIO 33 and GPIO 34 are part of the OCTAL PSRAM data bus. The
// M5 uses an ESP32-S3-WROOM-1-N16R8 (or N8R8) module with octal
// PSRAM; those pins are wired to the PSRAM chip. Driving them as
// general-purpose GPIO will crash the PSRAM controller and
// brick the firmware. The I2S / button / SPI / I2C pin map
// avoids them by design.
constexpr gpio_num_t kPinReservedPsramIo = GPIO_NUM_33;
constexpr gpio_num_t kPinReservedPsramClk = GPIO_NUM_34;

} // namespace tether::m5::board
