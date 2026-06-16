// board.h — Tether M5 (ThinkNode M5 / ELECROW) board pin map.
//
// Source of truth: meshtastic/firmware variants/esp32s3/ELECROW-ThinkNode-M5/
// variant.h (github.com/meshtastic/firmware, branch develop).
// When this disagrees with meshtastic, meshtastic wins — they ship
// on real hardware; we are porting their pin map onto a different
// firmware.
//
// If you change a pin here, change it in the meshtastic file too (or
// at least, verify against the M5 schematic — pins on the M5 PCB are
// not all freely remappable: LoRa RESET, BUSY, and the EPD's DIO are
// hard-wired).

#pragma once

#include "driver/gpio.h"

namespace tether::m5::board {

// ── LoRa SX1262 ──────────────────────────────────────────────────────
//
// The M5's onboard SX1262 has fixed wiring. Don't move these.
constexpr gpio_num_t kPinLoraCs      = GPIO_NUM_17;  // SPI CS
constexpr gpio_num_t kPinLoraSck     = GPIO_NUM_16;  // SPI SCK (shared bus)
constexpr gpio_num_t kPinLoraMosi    = GPIO_NUM_15;  // SPI MOSI (shared bus)
constexpr gpio_num_t kPinLoraMiso    = GPIO_NUM_7;   // SPI MISO (shared bus)
constexpr gpio_num_t kPinLoraReset   = GPIO_NUM_6;
constexpr gpio_num_t kPinLoraBusy    = GPIO_NUM_5;
constexpr gpio_num_t kPinLoraDio1    = GPIO_NUM_4;
constexpr gpio_num_t kPinLoraPowerEn = GPIO_NUM_46;

// ── Shared SPI bus (LoRa + EPD + SD) ─────────────────────────────────
constexpr gpio_num_t kPinSpiSck  = kPinLoraSck;   // GPIO 16
constexpr gpio_num_t kPinSpiMosi = kPinLoraMosi;  // GPIO 15
constexpr gpio_num_t kPinSpiMiso = kPinLoraMiso;  // GPIO 7

// ── EPD (1.54″) ──────────────────────────────────────────────────────
constexpr gpio_num_t kPinEpdCs   = GPIO_NUM_39;
constexpr gpio_num_t kPinEpdBusy = GPIO_NUM_42;
constexpr gpio_num_t kPinEpdDc   = GPIO_NUM_40;
constexpr gpio_num_t kPinEpdRes  = GPIO_NUM_41;
constexpr gpio_num_t kPinEpdSclk = GPIO_NUM_38;
constexpr gpio_num_t kPinEpdMosi = GPIO_NUM_45;

// ── SD card (over the shared SPI bus) ────────────────────────────────
//
// Meshtastic uses GPIO 10 for SD CS. We use the same — it's a free
// pin on the M5 (the GPS switch is on a separate physical header,
// even though the meshtastic variant.h also names it GPS_SWITH=10;
// the M5 schematic differentiates the two by header).
constexpr gpio_num_t kPinSdCs = GPIO_NUM_10;

// ── I2S0 — INMP441 microphone (right-edge cluster) ──────────────────
//
// All three pins are sequential on the right edge of the M5, free of
// any other function per the M5 schematic. These were chosen by the
// system architect specifically because the LoRa/EPD/SD pins above
// don't reach the right edge and the L76K GPS module only uses the
// left side.
constexpr gpio_num_t kPinI2s0Ws   = GPIO_NUM_35;  // Word Select
constexpr gpio_num_t kPinI2s0Bclk = GPIO_NUM_36;  // Bit Clock
constexpr gpio_num_t kPinI2s0Din  = GPIO_NUM_37;  // Data In (from mic)

// ── I2S1 — MAX98357A amplifier (split configuration) ────────────────
//
// LRC and BCLK are on the right edge (sequential with the mic
// cluster), DIN is on the left edge. There is no contiguous run of
// three free pins on the M5 for the amp because the LoRa/EPD/SD
// cluster occupies the upper right.
//
// Note: GPIO 47 and 48 are NOT the I2C1 SDA/SCL pins on the M5
// — those are GPIO 1 and 2 (I2C_SDA=2, I2C_SCL=1, see
// meshtastic variant.h). GPIO 47/48 are the right-edge pads (Pin
// 23 and 24 on the ELECROW connector pinout). The meshtastic
// variant.cpp uses them as a *second I2C bus* (Wire1) for the
// PCA9557 GPIO expander, but Tether does not need the PCA9557
// (no Meshtastic-style LED notifications, no Meshtastic-style
// power rail control), so we can reuse 47/48 for I2S1.
constexpr gpio_num_t kPinI2s1Ws   = GPIO_NUM_47;  // Word Select (= "LRC")
constexpr gpio_num_t kPinI2s1Bclk = GPIO_NUM_48;  // Bit Clock
constexpr gpio_num_t kPinI2s1Dout = GPIO_NUM_18;  // Data Out (to amp)

// ── Buttons (the M5 has exactly two; see §buttons below) ─────────────
constexpr gpio_num_t kPinButtonPtt  = GPIO_NUM_21;  // PIN_BUTTON1
constexpr gpio_num_t kPinButtonMenu = GPIO_NUM_14;  // PIN_BUTTON2
constexpr uint32_t  kButtonActiveLow = 0;          // GPIO low = pressed

// ── GPS switch (NOT a button; senses the GPS toggle's position) ──────
//
// The M5 has a physical *switch* (slider) on the case for turning
// the GPS module on/off. The switch's state is read on GPIO 10
// (GPS_SWITH in the meshtastic variant, note the typo). Tether does
// not use the GPS, but we leave this constant defined so future
// code can detect the switch's position.
constexpr gpio_num_t kPinGpsSwitch = GPIO_NUM_10;   // input, 1 = GPS on

// ── Power / battery / buzzer ─────────────────────────────────────────
constexpr gpio_num_t kPinBatteryAdc     = GPIO_NUM_8;
constexpr gpio_num_t kPinExtPwrDetect   = GPIO_NUM_12;
constexpr gpio_num_t kPinBuzzer         = GPIO_NUM_9;
constexpr gpio_num_t kPinLedBlue        = GPIO_NUM_NC;  // on PCA9557, not used

// ── USB-Serial (RAK4631 bridge on the M5) ───────────────────────────
//
// GPIO 43/44 are the M5's UART1 (per the meshtastic variant).
// They're the only pins in the variant.h that are explicitly
// described as "UART". We use them to talk to the RAK4631 in
// production; the ESP32-S3 USB-CDC is reserved for the serial
// monitor and `idf.py flash`.
constexpr gpio_num_t kPinUartTx = GPIO_NUM_43;
constexpr gpio_num_t kPinUartRx = GPIO_NUM_44;

}  // namespace tether::m5::board
