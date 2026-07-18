// board_t3s3_mvsr.h — Tether variant pin map for the LilyGO
// T3-S3 MVSR board (T3-S3 V1.2 mainboard + MVSR backplate).
//
// Source of truth: Xinyuan-LilyGO/T3-S3-MVSRBoard (GitHub),
// libraries/private_library/pin_config.h. When this disagrees with
// the upstream pin_config.h, the upstream wins on the hardware
// side — the schematic is the schematic — but the *allocation*
// (which pin drives which Tether function) is a Tether design
// decision and is documented below.
//
// Board variants: the MVSR backplate comes in V1.0 (MSM261 I2S mic)
// and V1.1 (MP34DT05 PDM mic). This pin map targets V1.1 (the
// current production revision, March 2025+); V1.0 shares the same
// BCLK/WS/DATA pads so the I2S-standard path also works on V1.0
// with kMicInterface = kI2sStd. See docs/VARIANTS.md.
//
// NO HARDWARE MOD REQUIRED. Unlike the M5, every audio GPIO is
// natively free on the MVSR — the mic and amp are on two SEPARATE
// I2S peripherals (I2S0 mic, I2S1 amp), so there is no shared-bus
// conflict and nothing to desolder.
//
// This header is selected by CONFIG_TETHER_BOARD_T3S3_MVSR (see
// board.h).

#pragma once

namespace tether::m5::board {

// ── Variant identity & capabilities ──────────────────────────────────
inline constexpr char kBoardName[] = "LilyGO-T3S3-MVSR";

// The MVSR has NO PCA9557 I/O expander. The notification LED is a
// direct GPIO (GPIO 37); there is no master peripheral power rail
// (peripherals are always powered). Tether code that drives the
// LED uses kPinLed directly; the pca9557 component is NOT compiled
// into the MVSR build (see main.cpp's kHasPca9557 gate).
inline constexpr bool kHasPca9557 = false;

// The MVSR has a 0.96" 128×64 SSD1306 OLED on I2C (GPIO 17/18),
// not the M5's 1.54" 200×200 SPI e-paper. The ssd1306 component
// drives it; the epd component is not compiled in.
enum class DisplayKind : uint8_t { kEpd, kOled };
inline constexpr DisplayKind kDisplayKind = DisplayKind::kOled;

// The MVSR backplate has only the BOOT button (GPIO 0). Tether
// needs PTT + Menu. We wire an EXTERNAL PTT button to a free GPIO
// (GPIO 4 — see kPinButtonPtt below) and use BOOT as Menu/cycle.
// The external PTT wiring is documented in docs/VARIANTS.md.
inline constexpr int kNumButtons = 2;

// All audio GPIOs are natively free on the MVSR — no mod needed.
inline constexpr bool kNeedsHardwareMod = false;

// ── I2S peripherals ──────────────────────────────────────────────────
//
// The MVSR runs the mic on I2S0 and the amp on I2S1 — two
// independent peripherals, NOT a shared full-duplex bus like the
// M5. This is simpler (no shared-clock constraints) and is the
// reason no hardware mod is required.
inline constexpr int kI2sMicPort = 0; // I2S_NUM_0 (mic)
inline constexpr int kI2sAmpPort = 1; // I2S_NUM_1 (amp)

// The V1.1 mic (MP34DT05-A) is PDM; the V1.0 mic (MSM261) is I2S
// standard. The ESP32-S3 I2S peripheral drives both; i2s_mic.cpp
// picks the init path on this flag. For a V1.0 board change this
// to kI2sStd and use the kPinI2s* pins (BCLK=47, WS=15, DIN=48).
enum class MicInterface : uint8_t { kI2sStd, kPdm };
inline constexpr MicInterface kMicInterface = MicInterface::kPdm;

// ── SPI hosts ────────────────────────────────────────────────────────
//
// The MVSR has LoRa on SPI2 (HSPI: SCK=5, MOSI=6, MISO=3, CS=7)
// and SD on SPI3 (FSPI: SCK=14, MOSI=11, MISO=2, CS=13) — TWO
// separate buses. Unlike the M5 there is no shared bus and no
// LoRa-IRQ-mid-SD-write hazard, so the spi_bus_mutex is per-bus.
inline constexpr int kLoraSpiHost = 1; // SPI2_HOST (HSPI)
inline constexpr int kSdSpiHost = 2;   // SPI3_HOST (FSPI)
inline constexpr int kEpdSpiHost = 0;  // unused (no EPD)

// ── LoRa SX1262 (SPI2 / HSPI) ────────────────────────────────────────
constexpr gpio_num_t kPinLoraCs = GPIO_NUM_7;
constexpr gpio_num_t kPinLoraSck = GPIO_NUM_5;
constexpr gpio_num_t kPinLoraMosi = GPIO_NUM_6;
constexpr gpio_num_t kPinLoraMiso = GPIO_NUM_3;
constexpr gpio_num_t kPinLoraReset = GPIO_NUM_8;
constexpr gpio_num_t kPinLoraBusy = GPIO_NUM_34;
constexpr gpio_num_t kPinLoraDio1 = GPIO_NUM_33;
constexpr gpio_num_t kPinLoraPowerEn = GPIO_NUM_NC; // no power-en GPIO

// ── LoRa SPI bus pins (alias to the LoRa SCK/MOSI/MISO above) ────────
constexpr gpio_num_t kPinSpiSck = kPinLoraSck;
constexpr gpio_num_t kPinSpiMosi = kPinLoraMosi;
constexpr gpio_num_t kPinSpiMiso = kPinLoraMiso;

// ── SD card (SPI3 / FSPI — separate bus from LoRa) ───────────────────
constexpr gpio_num_t kPinSdCs = GPIO_NUM_13;
constexpr gpio_num_t kPinSdSck = GPIO_NUM_14;
constexpr gpio_num_t kPinSdMosi = GPIO_NUM_11;
constexpr gpio_num_t kPinSdMiso = GPIO_NUM_2;

// ── EPD — not present on the MVSR ────────────────────────────────────
// All EPD pins are NC; the epd component is not compiled in.
constexpr gpio_num_t kPinEpdCs = GPIO_NUM_NC;
constexpr gpio_num_t kPinEpdBusy = GPIO_NUM_NC;
constexpr gpio_num_t kPinEpdDc = GPIO_NUM_NC;
constexpr gpio_num_t kPinEpdRes = GPIO_NUM_NC;
constexpr gpio_num_t kPinEpdSclk = GPIO_NUM_NC;
constexpr gpio_num_t kPinEpdMosi = GPIO_NUM_NC;

// ── I2S0 — mic (PDM on V1.1 / I2S-standard on V1.0) ──────────────────
//
// V1.1 PDM mic (MP34DT05-A): the ESP32-S3 PDM RX peripheral uses a
// single CLK + DATA pair (no WS). kPinI2sWs is the PDM clock, and
// kPinI2sDin is the PDM data line. kPinI2sBclk/kPinI2sDout are NC
// (the mic is not on a shared bus). i2s_mic.cpp uses kMicInterface
// to pick the PDM vs standard-I2S init path.
//
// V1.0 I2S mic (MSM261): BCLK=47, WS=15, DATA=48. For a V1.0 board,
// set kMicInterface = kI2sStd and wire kPinI2sBclk=47, kPinI2sWs=15,
// kPinI2sDin=48 (matching the MSM261_BCLK/WS/DATA in pin_config.h).
constexpr gpio_num_t kPinI2sWs = GPIO_NUM_15;   // PDM CLK (V1.1) / WS
constexpr gpio_num_t kPinI2sBclk = GPIO_NUM_NC; // PDM mic: unused
constexpr gpio_num_t kPinI2sDin = GPIO_NUM_48;  // PDM DATA (V1.1) / DIN
constexpr gpio_num_t kPinI2sDout = GPIO_NUM_NC; // mic bus: no DOUT
constexpr gpio_num_t kPinMicEn = GPIO_NUM_35;   // mic enable (both revs)

// ── I2S1 — amp (MAX98357A) ───────────────────────────────────────────
constexpr gpio_num_t kPinAmpBclk = GPIO_NUM_40;
constexpr gpio_num_t kPinAmpWs = GPIO_NUM_41;     // LRCLK
constexpr gpio_num_t kPinAmpDout = GPIO_NUM_39;   // DATA to amp
constexpr gpio_num_t kPinAmpSdMode = GPIO_NUM_38; // shutdown mode

// ── Buttons ──────────────────────────────────────────────────────────
//
// The MVSR backplate has only BOOT (GPIO 0). Tether needs PTT +
// Menu. PTT is an EXTERNAL button wired to GPIO 4 (a free GPIO with
// no conflicts); BOOT is Menu/cycle. Both are active-low with the
// internal pull-up enabled (buttons.Init enables the pull-up).
//
// External PTT wiring: one side of the button to GPIO 4, the other
// to GND. See docs/VARIANTS.md for the full wiring diagram.
constexpr gpio_num_t kPinButtonPtt = GPIO_NUM_4;  // EXTERNAL button
constexpr gpio_num_t kPinButtonMenu = GPIO_NUM_0; // BOOT button
constexpr uint32_t kButtonActiveLow = 0;          // GPIO low = pressed

// ── Power / battery / vibration motor ────────────────────────────────
constexpr gpio_num_t kPinBuzzer = GPIO_NUM_NC;         // no buzzer
constexpr gpio_num_t kPinExtPwrDetect = GPIO_NUM_NC;   // no VBUS detect
constexpr gpio_num_t kPinBatteryAdc = GPIO_NUM_1;      // ADC1_CH0 (T3-S3)
constexpr gpio_num_t kPinLed = GPIO_NUM_37;            // direct GPIO LED
constexpr gpio_num_t kPinVibrationMotor = GPIO_NUM_46; // PWM (PTT feedback)

// ── USB-Serial (T3-S3 native USB CDC; UART0 on 43/44 for bridge) ────
constexpr gpio_num_t kPinUartTx = GPIO_NUM_43;
constexpr gpio_num_t kPinUartRx = GPIO_NUM_44;

// ── I2C buses ────────────────────────────────────────────────────────
//
// I2C0 (Wire) is the bus for the PCF85063 RTC (GPIO 42/45). We
// don't use the RTC in v1, so Wire is initialized only by the
// display. The OLED (SSD1306) is on a SEPARATE I2C bus (GPIO 17/18)
// — the T3-S3 mainboard's display bus.
constexpr gpio_num_t kPinI2c0Scl = GPIO_NUM_45; // RTC bus (PCF85063)
constexpr gpio_num_t kPinI2c0Sda = GPIO_NUM_42;
constexpr gpio_num_t kPinI2c1Scl = GPIO_NUM_17; // OLED bus (SSD1306)
constexpr gpio_num_t kPinI2c1Sda = GPIO_NUM_18;

constexpr uint8_t kSsd1306I2cAddr = 0x3C;  // 7-bit OLED address
constexpr uint8_t kPcf85063I2cAddr = 0x51; // 7-bit RTC address

// The MVSR has no PCA9557; this constant is unused but defined for
// source compatibility with any code that references it under
// `if constexpr (kHasPca9557)`.
constexpr uint8_t kPca9557I2cAddr = 0x00;

// ── Pins explicitly reserved / do-not-touch ─────────────────────────
//
// The T3-S3 module uses QUAD (QSPI) flash + PSRAM, NOT octal. The
// QSPI bus uses GPIO 27–32 (SPICLK_N/P, SPIQ, SPID, SPIHD, SPIWP);
// GPIO 33–37 are FREE for general-purpose I/O (which is why the
// MVSR can put LORA_DIO1=33, LORA_BUSY=34, MIC_EN=35, LED=37 on
// them — impossible on the M5's octal-PSRAM module). Driving
// GPIO 27–32 as GPIO crashes the flash/PSRAM controller.
constexpr gpio_num_t kPinReservedPsramIo = GPIO_NUM_27;
constexpr gpio_num_t kPinReservedPsramClk = GPIO_NUM_28;

} // namespace tether::m5::board
