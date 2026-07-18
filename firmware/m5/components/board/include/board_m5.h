// board_m5.h — Tether variant pin map for the ThinkNode M5 (ELECROW).
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
//
// This header is selected by CONFIG_TETHER_BOARD_M5 (see board.h).
// See board_t3s3_mvsr.h for the LilyGO T3-S3 MVSR variant.

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

namespace tether::m5::board {

// ── Variant identity & capabilities ──────────────────────────────────
inline constexpr char kBoardName[] = "ThinkNode-M5";

// The M5 has a PCA9557 I/O expander driving LEDs, e-ink backlight,
// and the master peripheral power rail. Tether code goes through
// the pca9557 component rather than direct GPIO for those.
inline constexpr bool kHasPca9557 = true;

// The M5 has a 1.54" 200×200 e-paper display driven over SPI.
// The MVSR has a 0.96" 128×64 SSD1306 OLED over I2C instead.
enum class DisplayKind : uint8_t { kEpd, kOled };
inline constexpr DisplayKind kDisplayKind = DisplayKind::kEpd;

// The M5 has two physical buttons (PTT + Menu). The MVSR has only
// BOOT (GPIO 0) and needs an external PTT button wired to a free GPIO.
inline constexpr int kNumButtons = 2;

// The M5 needs the two hardware mods described above before the
// audio path works. The MVSR's audio pins are all natively free.
inline constexpr bool kNeedsHardwareMod = true;

// ── I2S peripherals ──────────────────────────────────────────────────
//
// The M5 runs the mic and amp on a SHARED I2S0 bus (full-duplex,
// same BCLK/WS, separate DIN/DOUT). The MVSR runs the mic on I2S0
// and the amp on I2S1 (two independent peripherals). Both variants
// reference these constants from i2s_mic.cpp / i2s_amp.cpp so the
// driver code is shared and the port assignment is data-driven.
inline constexpr int kI2sMicPort = 0; // I2S_NUM_0
inline constexpr int kI2sAmpPort = 0; // I2S_NUM_0 (shared)

// Mic interface: the M5's INMP441 and the MVSR V1.0's MSM261 are
// standard I2S; the MVSR V1.1's MP34DT05 is PDM. The driver picks
// the init path on this flag.
enum class MicInterface : uint8_t { kI2sStd, kPdm };
inline constexpr MicInterface kMicInterface = MicInterface::kI2sStd;

// ── SPI hosts ────────────────────────────────────────────────────────
//
// The M5 shares one SPI2 (HSPI) bus between LoRa, SD, and EPD. The
// MVSR has LoRa on SPI2 (HSPI) and SD on SPI3 (FSPI) — two separate
// buses. main.cpp / spi_bus use these to wire the right host per
// device. Values are spi_host_device_t enum ints (SPI2_HOST=1,
// SPI3_HOST=2) so the header stays host-test-buildable.
inline constexpr int kLoraSpiHost = 1; // SPI2_HOST
inline constexpr int kSdSpiHost = 1;   // SPI2_HOST (shared with LoRa)
inline constexpr int kEpdSpiHost = 1;  // SPI2_HOST (shared)

// ── LoRa SX1262 ──────────────────────────────────────────────────────
constexpr gpio_num_t kPinLoraCs = GPIO_NUM_17;
constexpr gpio_num_t kPinLoraSck = GPIO_NUM_16;
constexpr gpio_num_t kPinLoraMosi = GPIO_NUM_15;
constexpr gpio_num_t kPinLoraMiso = GPIO_NUM_7;
constexpr gpio_num_t kPinLoraReset = GPIO_NUM_6;
constexpr gpio_num_t kPinLoraBusy = GPIO_NUM_5;
constexpr gpio_num_t kPinLoraDio1 = GPIO_NUM_4;
constexpr gpio_num_t kPinLoraPowerEn = GPIO_NUM_46;

// ── Shared SPI bus (LoRa + EPD + SD) ─────────────────────────────────
constexpr gpio_num_t kPinSpiSck = kPinLoraSck;
constexpr gpio_num_t kPinSpiMosi = kPinLoraMosi;
constexpr gpio_num_t kPinSpiMiso = kPinLoraMiso;

// ── EPD (1.54″) ──────────────────────────────────────────────────────
constexpr gpio_num_t kPinEpdCs = GPIO_NUM_39;
constexpr gpio_num_t kPinEpdBusy = GPIO_NUM_42;
constexpr gpio_num_t kPinEpdDc = GPIO_NUM_40;
constexpr gpio_num_t kPinEpdRes = GPIO_NUM_41;
constexpr gpio_num_t kPinEpdSclk = GPIO_NUM_38;
constexpr gpio_num_t kPinEpdMosi = GPIO_NUM_45;
// The E-ink's backlight power is gated through the PCA9557
// (PCA_PIN_EINK_EN, expander pin 5). See pca9557.h.

// ── SD card (over the shared SPI bus) ────────────────────────────────
constexpr gpio_num_t kPinSdCs = GPIO_NUM_10;
constexpr gpio_num_t kPinSdSck = kPinLoraSck;
constexpr gpio_num_t kPinSdMosi = kPinLoraMosi;
constexpr gpio_num_t kPinSdMiso = kPinLoraMiso;

// ── I2S0 — shared full-duplex audio bus (mic + amp) ─────────────────
constexpr gpio_num_t kPinI2sWs = GPIO_NUM_19;
constexpr gpio_num_t kPinI2sBclk = GPIO_NUM_20;
constexpr gpio_num_t kPinI2sDin = GPIO_NUM_18; // Data In: from mic
constexpr gpio_num_t kPinI2sDout = GPIO_NUM_9; // Data Out: to amp

// ── Amp pin aliases (shared bus — same as I2S0 above) ──────────────
// The M5 amp shares the I2S0 bus, so these alias to the shared pins.
// Defined for source compatibility with i2s_amp.cpp's separate-bus
// branch (which is discarded by `if constexpr` on the M5 but still
// must compile). The MVSR has separate amp pins on I2S1; see
// board_t3s3_mvsr.h. kPinAmpSdMode, kPinMicEn, kPinVibrationMotor
// are NC — the M5 hardware doesn't have those signals.
constexpr gpio_num_t kPinAmpBclk = kPinI2sBclk;
constexpr gpio_num_t kPinAmpWs = kPinI2sWs;
constexpr gpio_num_t kPinAmpDout = kPinI2sDout;
constexpr gpio_num_t kPinAmpSdMode = GPIO_NUM_NC;      // M5 amp has no SD_MODE
constexpr gpio_num_t kPinMicEn = GPIO_NUM_NC;          // INMP441 has no EN pin
constexpr gpio_num_t kPinVibrationMotor = GPIO_NUM_NC; // no vibration motor

// ── Buttons ──────────────────────────────────────────────────────────
constexpr gpio_num_t kPinButtonPtt = GPIO_NUM_21;  // PIN_BUTTON1
constexpr gpio_num_t kPinButtonMenu = GPIO_NUM_14; // PIN_BUTTON2
constexpr uint32_t kButtonActiveLow = 0;           // GPIO low = pressed

// ── Power / battery / buzzer ─────────────────────────────────────────
constexpr gpio_num_t kPinBuzzer = GPIO_NUM_9;        // now = I2S DOUT
constexpr gpio_num_t kPinExtPwrDetect = GPIO_NUM_12; // still VBUS detect
constexpr gpio_num_t kPinBatteryAdc = GPIO_NUM_8;    // ADC channel 7

// The M5's notification LED is behind the PCA9557 (not a direct
// GPIO). kPinLed is NC; use the pca9557 component.
constexpr gpio_num_t kPinLed = GPIO_NUM_NC;

// ── USB-Serial (RAK4631 bridge on the M5) ───────────────────────────
constexpr gpio_num_t kPinUartTx = GPIO_NUM_43;
constexpr gpio_num_t kPinUartRx = GPIO_NUM_44;

// ── I2C buses ────────────────────────────────────────────────────────
constexpr gpio_num_t kPinI2c0Scl = GPIO_NUM_1;
constexpr gpio_num_t kPinI2c0Sda = GPIO_NUM_2;
constexpr gpio_num_t kPinI2c1Scl = GPIO_NUM_48; // Wire1 (PCA9557)
constexpr gpio_num_t kPinI2c1Sda = GPIO_NUM_47; // Wire1 (PCA9557)

constexpr uint8_t kPca9557I2cAddr = 0x18; // 7-bit address (no R/W bit)

// Unused on the M5; defined for source compatibility with code that
// references them under `if constexpr (kDisplayKind == kOled)` (the
// SSD1306 OLED / PCF85063 RTC are MVSR-only). The values are inert.
constexpr uint8_t kSsd1306I2cAddr = 0x00;
constexpr uint8_t kPcf85063I2cAddr = 0x00;

// ── Pins explicitly reserved / do-not-touch ─────────────────────────
//
// GPIO 33 and GPIO 34 are part of the OCTAL PSRAM data bus. The
// M5 uses an ESP32-S3-WROOM-1-N16R8 module with octal PSRAM; those
// pins are wired to the PSRAM chip. Driving them as general-purpose
// GPIO will crash the PSRAM controller and brick the firmware.
constexpr gpio_num_t kPinReservedPsramIo = GPIO_NUM_33;
constexpr gpio_num_t kPinReservedPsramClk = GPIO_NUM_34;

} // namespace tether::m5::board
