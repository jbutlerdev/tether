# Hardware

This is the bill of materials and the pin map for Tether v0.1.3.

The pin map is **authoritative**: the same numbers are encoded in
`firmware/m5/components/board/include/board.h` and were derived
from the Meshtastic variant.h for the ELECROW ThinkNode M5
(variants/esp32s3/ELECROW-ThinkNode-M5/variant.h, branch develop).
If you change a pin in the firmware, change it here too — and vice
versa.

> **v0.1.3 — READ THIS FIRST.** The Tether audio path requires
> four GPIOs for a single full-duplex I²S0 bus. The stock M5 has
> only one natively free pin (GPIO 18). To free the other three,
> **three hardware modifications are required** before flashing
> the firmware. They are documented in detail in
> [`docs/HARDWARE-MODS.md`](docs/HARDWARE-MODS.md) with photos
> and step-by-step instructions. The mods are:
> 1. **GPS "Always-On" hack** — bypass the L76K load switch, sever
>    the trace back to GPIO 10.
> 2. **Buzzer removal** — desolder the SMD buzzer (frees GPIO 9).
> 3. **Power-Detect trace cut** — sever the trace from the USB
>    voltage divider to GPIO 12.
>
> **Do not flash the firmware onto an unmodified M5.** The audio
> path will not work.

## 1. Handheld node

* **Core Microcontroller & Transceiver:** Elecrow **ThinkNode M5**
  LoRa Meshtastic transceiver (ESP32-S3 + onboard Semtech **SX1262**
  LoRa + 1.54″ EPD + 3 user controls — 2 buttons + 1 GPS slider).
* **Audio Input:** INMP441 I2S MEMS Microphone Module / Adafruit I2S
  MEMS microphone breakout. Wired to **I2S0** at GPIO 35/36/37
  (WS/BCLK/DIN, all on the M5's right edge).
* **Audio Output:** Adafruit Class D I2S Amplifier (MAX98357A) paired
  with a low-profile speaker. Wired to **I2S1** in a split
  configuration: LRC=47, BCLK=48 (right edge), DIN=18 (left edge).
  No contiguous run of three free pins exists for the amp; the split
  config is mandatory.
* **Storage:** MicroSD Card (operating over SPI; CS=GPIO 10).
* **Power:** 1200 mAh Li-Po (battery pin GPIO 8, ADC channel 7).
  Charge via USB-C (`EXT_PWR_DETECT` on GPIO 12).

### 1.1 User controls (the M5 has 2 buttons + 1 switch)

| Control | Pin | Function |
|---|---|---|
| **Button A** (front, large) | GPIO 21 | **PTT**: push to record, release to enqueue + transmit. Long-press is a v0.2.0 hook. |
| **Button B** (side) | GPIO 14 | **Menu / cycle**: short press = next conversation; long-press = settings entry / exit. |
| **GPS slider** (case) | GPIO 10 (digital input) | Toggles the L76K GPS module on/off. **Not a button.** Tether does not use the GPS, but the slider is sensed at boot so we can log a "GPS off" line in `tether-m5` serial output. |

The third "control" on the M5 is the GPS slider, not a third
button. The 3-button model that the v0.1.0 docs inherited from
earlier research is wrong: the Meshtastic variant.h only defines
`PIN_BUTTON1=21` and `PIN_BUTTON2=14`. UI code that needs a "back"
or "decrease" affordance (e.g. inside the settings menu) must use
**PTT as the back/decrease control**, not a hypothetical button C.

### 1.2 Pin map (firmware/m5/components/board/include/board.h)

All pin numbers are ESP32-S3 GPIO numbers.

| Subsystem | Pin | Function | Source |
|---|---|---|---|
| **LoRa SX1262** | 17 | CS | meshtastic `SX126X_CS` |
|  | 16 | SPI SCK (shared bus) | `LORA_SCK` |
|  | 15 | SPI MOSI (shared bus) | `LORA_MOSI` |
|  | 7 | SPI MISO (shared bus) | `LORA_MISO` |
|  | 6 | RESET | `SX126X_RESET` |
|  | 5 | BUSY | `SX126X_BUSY` |
|  | 4 | DIO1 (IRQ) | `SX126X_DIO1` |
|  | 46 | Power enable | `SX126X_POWER_EN` |
| **EPD (1.54″)** | 39 | CS | `PIN_EINK_CS` |
|  | 42 | BUSY | `PIN_EINK_BUSY` |
|  | 40 | DC | `PIN_EINK_DC` |
|  | 41 | RES | `PIN_EINK_RES` |
|  | 38 | SCLK | `PIN_EINK_SCLK` |
|  | 45 | MOSI / SDI | `PIN_EINK_MOSI` |
|  | (PCA9557 pin 5) | E-ink power enable (not GPIO; on the I2C expander) | `PCA_PIN_EINK_EN` |
| **SD card (SPI)** | 10 | CS | `SD_CS` |
| **I2S0 — INMP441** | 35 | WS (word select) | architect |
|  | 36 | BCLK (bit clock) | architect |
|  | 37 | DIN (data in, from mic) | architect |
| **I2S1 — MAX98357A** | 47 | WS / LRC (word select) | architect |
|  | 48 | BCLK (bit clock) | architect |
|  | 18 | DOUT (data out, to amp) | architect |
| **Buttons** | 21 | Button A (PTT) | meshtastic `PIN_BUTTON1` |
|  | 14 | Button B (Menu) | meshtastic `PIN_BUTTON2` |
| **GPS slider** | 10 | Slider position sense | meshtastic `GPS_SWITH` (note: same GPIO as SD_CS but a different physical pad per the M5 schematic) |
| **Battery** | 8 | VBAT ADC (channel 7) | `BATTERY_PIN` |
| **Power** | 12 | VBUS detect | `EXT_PWR_DETECT` |
| **Buzzer** | 9 | PWM (active high) | `PIN_BUZZER` |
| **UART1 (RAK4631 bridge)** | 43 | TX | meshtastic `UART_TX` |
|  | 44 | RX | meshtastic `UART_RX` |
| **I2C0 (RTC, future)** | 1 | SCL | `I2C_SCL` |
|  | 2 | SDA | `I2C_SDA` |
| **I2C1 (PCA9557, unused by Tether)** | 47 | SCL (collides with I2S1 WS; not used by Tether) | meshtastic `Wire1.begin(48, 47)` |
|  | 48 | SDA (collides with I2S1 BCLK; not used by Tether) | same |

The I2C1 / I2S1 collision is the reason `board.h` has a long
comment about the right-edge pads: the PCA9557 I2C expander is
unneeded in Tether (no Meshtastic-style LED notifications, no
power-rail control), so GPIO 47/48 are free for I2S1.

### 1.3 Source of truth

The Meshtastic variant.h for this exact board is at
<https://raw.githubusercontent.com/meshtastic/firmware/refs/heads/develop/variants/esp32s3/ELECROW-ThinkNode-M5/variant.h>.
When `board.h` disagrees with the variant.h, the variant.h wins —
they ship on real hardware. If you change a pin, change it in both
files (or update `board.h`'s header comment to cite the
discrepancy).

## 2. PC base-station bridge

* **Desktop PC:** Running the custom Go daemon (`tetherd`,
  eventually) for audio reassembly, STT/TTS, and Matrix/Forge
  routing.
* **USB LoRa Bridge:** RAKwireless Mini Meshtastic Starter Kit
  (nRF52840 + SX1262). Connected over USB-Serial at **921 600
  baud** to the PC. The M5 talks to the bridge over LoRa; the
  bridge talks to the PC over USB-Serial. The frame format is
  `0xAA 0x55 | type(1) | len(2 LE) | payload(len) | crc16(2 LE)`.
