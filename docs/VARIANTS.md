# Tether hardware variants

Tether's M5-family firmware builds for multiple ESP32-S3 + SX1262
boards. The active variant is a **build-time Kconfig choice**
(`CONFIG_TETHER_BOARD_M5` / `CONFIG_TETHER_BOARD_T3S3_MVSR`) selected
in `firmware/m5/components/board/Kconfig`; the selected variant's pin
map lives in `firmware/m5/components/board/include/board_<name>.h`
and is included by `board.h`. Every component references the `kPin…`
constants and capability flags from `board.h`, so the same source
builds for either board and the pin assignment is data-driven.

| Variant | Kconfig | Board header | Display | I/O expander | Audio bus | Hardware mod | Buttons |
|---|---|---|---|---|---|---|---|
| ThinkNode M5 (ELECROW) | `CONFIG_TETHER_BOARD_M5` | `board_m5.h` | 1.54″ EPD (SPI) | PCA9557 (I²C) | shared I²S0 full-duplex | **required** (GPS + buzzer removal) | 2 (GPIO 21 + 14) |
| LilyGO T3-S3 MVSR | `CONFIG_TETHER_BOARD_T3S3_MVSR` | `board_t3s3_mvsr.h` | 0.96″ SSD1306 OLED (I²C) | none (direct GPIO LED) | 2 separate I²S peripherals | **none** | 1 (BOOT) + external PTT |

## Selecting a variant at build time

```bash
# M5 (default) — from a clean checkout:
cd firmware/m5
idf.py set-target esp32s3
idf.py build

# LilyGO T3-S3 MVSR — use the sdkconfig defaults overlay:
cd firmware/m5
idf.py set-target esp32s3
idf.py -DSDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.t3s3_mvsr" reconfigure
idf.py build
```

`idf.py menuconfig` → "Tether board variant" also lets you pick
interactively. CI builds both variants (the `firmware-build.yml`
workflow's `m5` and `m5-t3s3-mvsr` jobs).

## Adding a new variant

1. Add `firmware/m5/components/board/include/board_<name>.h` defining
   the **full symbol set** that `board_m5.h` / `board_t3s3_mvsr.h`
   define (every `kPin…`, the capability flags `kHasPca9557`,
   `kDisplayKind`, `kI2sMicPort`, `kI2sAmpPort`, `kMicInterface`, the
   SPI hosts, `kNumButtons`, `kNeedsHardwareMod`, the do-not-touch
   PSRAM pins, `kBoardName`). The host test
   `test_host/test_board_t3s3_mvsr.cpp` is the template for a
   compile-time + pin-conflict validation test.
2. Add a `config TETHER_BOARD_<NAME>` entry to
   `firmware/m5/components/board/Kconfig`.
3. Add a `sdkconfig.defaults.<name>` overlay if the module's PSRAM
   type or flash layout differs (the T3-S3 is QSPI; the M5 is OPI).
4. Add a `firmware-build.yml` job that builds with
   `-DSDKCONFIG_DEFAULTS=...`.
5. Update this table.

## LilyGO T3-S3 MVSR wiring

**Source of truth:**
[`Xinyuan-LilyGO/T3-S3-MVSRBoard`](https://github.com/Xinyuan-LilyGO/T3-S3-MVSRBoard),
`libraries/private_library/pin_config.h`. When this disagrees with
the upstream `pin_config.h`, the upstream wins on the hardware side.

### No hardware modification required

Unlike the M5, every audio GPIO is natively free on the MVSR. The mic
(I²S0) and amp (I²S1) are on **two separate I²S peripherals**, so
there is no shared-bus conflict and nothing to desolder. This is the
biggest practical advantage of the MVSR for Tether.

### Board revisions (mic model)

The MVSR backplate has two revisions:

- **V1.0** — MSM261S4030H0R, I²S mic (BCLK=47, WS=15, DATA=48).
- **V1.1** — MP34DT05-A, **PDM** mic (CLK=15, DATA=48). Current
  production (March 2025+).

The pin map (`board_t3s3_mvsr.h`) targets **V1.1** with
`kMicInterface = kPdm`. For a V1.0 board, set `kMicInterface =
kI2sStd` and wire `kPinI2sBclk = GPIO_NUM_47` (the MSM261 BCLK pad);
the I²S-standard init path in `i2s_mic.cpp` handles the rest.

### External PTT button (required)

The MVSR backplate has only the **BOOT** button (GPIO 0). Tether
needs PTT + Menu. Wire an **external PTT button to GPIO 4**:

```
   GPIO 4  ─────┐
                ├── [ PTT button ] ──── GND
                │
   (internal pull-up enabled by buttons.Init)
```

- GPIO 4 → `kPinButtonPtt` (PTT: push to record, release to send)
- GPIO 0 → `kPinButtonMenu` (BOOT: short press = cycle conversation,
  long press = settings)

Both are active-low; `buttons.Init` enables the internal pull-up, so
no external resistor is needed. A panel-mount PTT button on a cable
to a 2-pin header (GPIO 4 + GND) is the recommended assembly.

### Peripherals the MVSR exposes (and Tether uses)

| Function | GPIO | Notes |
|---|---|---|
| LoRa SX1262 CS / SCK / MOSI / MISO | 7 / 5 / 6 / 3 | SPI2 (HSPI) |
| LoRa RST / BUSY / DIO1 | 8 / 34 / 33 | |
| SD card CS / SCK / MOSI / MISO | 13 / 14 / 11 / 2 | SPI3 (FSPI) — **separate bus** from LoRa |
| Mic PDM CLK / DATA / EN | 15 / 48 / 35 | I²S0, PDM (V1.1) |
| Amp MAX98357A BCLK / WS / DATA / SD_MODE | 40 / 41 / 39 / 38 | I²S1 |
| OLED SSD1306 SDA / SCL | 18 / 17 | I²C1, address 0x3C |
| RTC PCF85063 SDA / SCL / INT | 42 / 45 / 16 | I²C0, address 0x51 (unused in v1) |
| LED | 37 | direct GPIO (no PCA9557) |
| Vibration motor | 46 | PWM (PTT feedback) |
| Battery ADC | 1 | ADC1_CH0 |
| UART TX / RX | 43 / 44 | USB-serial bridge |

### Do-not-touch pins (QSPI flash/PSRAM)

The T3-S3 module uses **quad (QSPI)** flash + PSRAM, not octal. The
QSPI bus occupies **GPIO 27–32** (SPICLK_N/P, SPIQ, SPID, SPIHD,
SPIWP). Driving any of them as GPIO crashes the flash controller.

This is the key difference from the M5 (which uses **octal** PSRAM and
reserves GPIO 33–37). The MVSR's `pin_config.h` puts LoRa DIO1/BUSY on
33/34, the mic EN on 35, and the LED on 37 — all free on QSPI, all
impossible on the M5's OPI module. The `sdkconfig.defaults.t3s3_mvsr`
overlay sets `CONFIG_SPIRAM_MODE_QUAD=y` to match.

### What is NOT yet ported (follow-ups)

- **Full OLED screen set.** The M5's `epd/screens.cpp` renders seven
  screens (idle / recording / queued / TX / TTS / settings /
  low-battery) at 200×200. The MVSR's `ssd1306` component currently
  renders a boot screen + text; the full 128×64 screen set is a
  follow-up that mirrors the M5 screens at OLED resolution.
- **PDM mic calibration.** The PDM init path is wired
  (`i2s_mic.cpp`, `kMicInterface == kPdm`); bench validation of the
  gain/noise profile against the M5's INMP441 is a hardware task.
- **Vibration motor as PTT feedback.** `kPinVibrationMotor` (GPIO 46)
  is defined; wiring it into the PTT state machine's beep path is a
  small follow-up (replaces the M5's buzzer beep with a haptic buzz).
