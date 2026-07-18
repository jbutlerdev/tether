# Hardware Modifications — ThinkNode M5 for Tether v0.1.4

This document describes the **physical modifications** that must be
performed on the Elecrow ThinkNode M5 board before flashing the
Tether firmware. These mods are required because the stock M5 has
**only one natively free GPIO** (GPIO 18); the rest of the
right-edge and bottom-edge pads are wired to peripherals
(SX1262 LoRa, EPD, SD, LEDs, GPS, buzzer, etc.) that we cannot
remap without desoldering.

The Tether firmware assumes the mods below have been performed. If
you flash the firmware onto an unmodified M5, the I²S0 audio bus
will not work (GPIO 9 is still owned by the buzzer, GPIO 19/20 are
still wired to the GPS module's UART) and the audio path will
record silence / output nothing.

> **v0.1.4 simplification.** Earlier drafts of this document
> described **three** mods: a "GPS Always-On hack" (bypass the GPS
> load switch and sever the gate trace), buzzer removal, and a
> VBUS-detect trace cut. The v0.1.4 design replaces the Always-On
> hack with **complete GPS module removal** — desoldering the L76K
> module entirely. This has two benefits:
> 1. **No more ~25 mA continuous drain** from the GPS being
>    permanently powered.
> 2. **Five GPIOs are freed** by the GPS removal (10, 11, 13, 19,
>    20 — the slider sense, standby, reinit, RX, and TX lines).
>    Two of those (GPIO 19 / GPIO 20, the GPS UART) absorb the
>    I²S0 WS / BCLK requirement, which means we **no longer need
>    to cut the VBUS-detect trace**. GPIO 12 stays wired to the
>    USB voltage divider and the firmware can read VBUS state
>    normally.
>
> The buzzer is still removed (it's still tied to GPIO 9, which we
> need for I²S0 DOUT).

---

## TL;DR — what you need to do

**Two mods, in this order:**

1. **GPS module removal** — desolder the Quectel L76K GPS module
   from the M5 board. *Frees GPIO 19 and GPIO 20 for I²S0 WS /
   BCLK (and GPIO 10, 11, 13 as a side effect).*
2. **Buzzer removal** — desolder the SMD buzzer. *Frees GPIO 9
   for I²S0 DOUT (amp DIN).*

After both mods, the shared I²S0 bus is wired full-duplex:

| Signal | GPIO | Source |
|---|---|---|
| WS (LRC) | 19 | freed by GPS removal (was GPS L76K RX) |
| BCLK | 20 | freed by GPS removal (was GPS L76K TX) |
| Mic SD (DIN) | 18 | mic → ESP32 (natively free) |
| Amp DIN (DOUT) | 9 | ESP32 → amp (freed by buzzer removal) |

GPIO 12 (USB VBUS detect) is **untouched**. The firmware can read
USB plug events normally.

Do the mods in the order above. The GPS removal is the easiest
single step (the L76K is a small LCC module designed for reflow);
the buzzer removal is the hardest (it's a heavier component with
larger thermal mass).

**Tools required:** temperature-controlled soldering iron (350 °C
with a fine tip), fine-gauge wire (30 AWG silicone-jacketed is
good), flux pen, hot-air station (required for the GPS removal,
recommended for the buzzer), fine-tip tweezers, loupe or USB
microscope, multimeter for continuity checks.

**Time required:** 30–60 minutes for a first attempt. The hot-air
GPS removal is faster than the buzzer removal.

**Skill level:** experienced SMD rework. The GPS removal in
particular requires comfort with hot-air on a multi-layer board
with nearby plastic parts — the GPS antenna and case are
adjacent to the module. If you have only done through-hole
soldering before, practice on a scrap board first.

---

## 1. GPS module removal

**Goal:** desolder the Quectel L76K GPS module from the M5 board.
After this, the GPS is gone (not just powered down), and five
GPIOs that were dedicated to it (10, 11, 13, 19, 20) are
electrically floating on the ESP32 side. We will reuse two of
them (19 and 20) for the I²S0 audio bus.

### Why this works

The M5 schematic (per the Meshtastic variant.h at
<https://raw.githubusercontent.com/meshtastic/firmware/refs/heads/develop/variants/esp32s3/ELECROW-ThinkNode-M5/variant.h>)
shows the L76K module tied to the ESP32-S3 over five GPIOs:

| ESP32-S3 GPIO | Variant.h name | L76K signal |
|---|---|---|
| 10 | `GPS_SWITH` | Slider switch (mechanical sense; not the GPS itself) |
| 11 | `PIN_GPS_STANDBY` | Standby control (low = sleep, high = wake) |
| 13 | `PIN_GPS_REINIT` | Reset (low ≥ 100 ms = reset) |
| 19 | `GPS_RX_PIN` | UART RX (data from GPS into CPU) |
| 20 | `GPS_TX_PIN` | UART TX (data from CPU to GPS) |

The L76K is a 9.7 × 10.1 × 2.4 mm LCC package with 18
land-grid-array pins on its underside (per the Quectel L76K
Hardware Design datasheet, V1.0). It is intended for reflow
soldering but desolders cleanly with hot air at the right
temperature.

> **Tether does not use the GPS.** The slider on the M5 case
> becomes a vestigial mechanical control; it no longer has any
> effect on the firmware, because the GPS module it would have
> switched is gone. The slider's physical pad is on GPIO 10,
> which is also the SD card's CS line via a different physical
> pad (see AGENTS.md §3.4 and `board.h::kPinSdCs`). After this
> mod, toggling the slider will mechanically toggle the SD_CS
> pad, but the SD card is in SPI mode and ignores the slider
> state.

### Steps

1. **Discharge yourself.** Touch a grounded metal surface. The
   ESP32-S3 and the L76K module are both ESD-sensitive.
2. **Remove the back cover of the M5.** Four Phillips #00 screws
   on the back. Set the cover and screws aside in a magnetic
   tray. **Be careful with the GPS antenna cable** — it's a
   small U.FL / IPEX connector on the GPS module's PCB edge,
   and yanking it will tear the connector off.
3. **Disconnect the GPS antenna.** Gently lift the U.FL
   connector's retaining tab with tweezers (it pops straight
   up, ~1 mm of travel) and slide the antenna cable out. Set
   the antenna aside — you will not reuse it. If you want to
   be tidy, tape the loose antenna cable to the inside of the
   case so it doesn't rattle.
4. **Locate the L76K module.** It is the small (~10 × 10 mm)
   shielded module on the underside of the main PCB, near
   one of the short edges. Reference: see the M5 schematic
   page 4 (RF / GPS section) and the Elecrow product photo in
   the user manual.
5. **Apply flux** around all four edges of the L76K module.
   A flux pen works well; cover the LCC pads on the PCB
   underneath as thoroughly as you can without flooding the
   surrounding area.
6. **Preheat the board.** Set your hot-air station to 150 °C
   with low airflow and warm the whole board for 60–90
   seconds. This drives off moisture and reduces thermal
   shock to the L76K (which has an internal ceramic patch
   antenna that can crack if heated too quickly).
7. **Reflow the L76K module.** Increase the hot-air station
   to **320 °C** with **medium airflow**, keep the nozzle
   ~2–3 cm above the module, and move in a slow circular
   pattern. After 45–60 seconds the solder under the LCC pads
   will be fully molten (you can tell when the module shifts
   slightly under the airflow or when a gentle nudge with
   tweezers slides it).
8. **Lift the module off.** Use fine-tip tweezers to lift the
   module straight up off the board. Do **not** pry — the LCC
   pads on the PCB are robust, but prying can lift them. If
   the module doesn't release easily, keep heating; do not
   force it.
9. **Clean the pads.** Use solder wick and a clean soldering
   iron tip (350 °C) to remove residual solder from the 18
   LCC pads. Apply fresh flux, lay the wick over the pads,
   and press the iron gently. The pads should end up flat and
   shiny.
10. **Verify.** With a multimeter in continuity mode, confirm
    that **none** of the freed GPIOs (10, 11, 13, 19, 20) is
    shorted to any other GPIO or to GND / 3V3. They should
    all read infinite resistance to each other and to the
    power rails.
11. **Optional: protect the freed pads.** If you intend to
    wire GPIO 19 / GPIO 20 to the I²S bus as documented in
    §4, tin the castellated edge pads near GPIO 19 / GPIO 20
    (the right edge of the ESP32-S3 module) with a small
    amount of fresh solder. The castellated edges are easy to
    solder to — they're just exposed metal.
12. **Re-test.** Power the board on USB. The EPD will show
    the stock Meshtastic firmware (we haven't flashed
    Tether yet). The red power LED should illuminate
    (hardware-OR'd with VBUS). The blue notification LED
    should not illuminate (the GPS module is no longer
    present to drive it).

**After this mod, GPIO 19 and GPIO 20 are yours.** They will
read as digital inputs with floating values; pull-up or
pull-down externally when you wire them to the I²S bus in §4.
GPIOs 10, 11, and 13 are also freed but we don't need them
for Tether.

---

## 2. Buzzer removal

**Goal:** desolder the SMD buzzer from the M5 PCB. This frees
GPIO 9 (the buzzer's PWM drive pin) for I²S0 DOUT.

### Why this works

The M5's buzzer is a tiny SMD component, ~6 mm × 6 mm, mounted
on the top side of the PCB. Its PWM drive pin is GPIO 9. Once
the buzzer is gone, GPIO 9 is electrically isolated except for
the ESP32's pad.

> **Tether does not use the buzzer in v0.1.4.** The blue
> notification LED (driven by the PCA9557) provides user
> feedback. If you want a buzzer, see the v0.2.0 hook (a future
> revision will add an external piezo on a different GPIO).

### Steps

1. **Locate the buzzer.** It is the small square component
   near the top-left of the M5, on the same side as the EPD.
   Reference: see the M5 schematic, page 2.
2. **Apply hot air** at 350 °C, low airflow, for 30–60 seconds
   to melt the solder under the buzzer. If you don't have a
   hot-air station, use two soldering irons at 380 °C to heat
   both pads simultaneously while prying with tweezers.
3. **Lift the buzzer off the board.** Tweezers are essential
   here. Work the tweezers under the buzzer gently; don't pry
   against the PCB or you may lift a pad.
4. **Clean the pads.** Use solder wick to remove residual
   solder from the two pads where the buzzer was.
5. **Verify.** With a multimeter, confirm that GPIO 9 is no
   longer connected to anything on the board (infinite
   resistance between the GPIO 9 pad on the ESP32 and any
   other point on the PCB).

**After this mod, GPIO 9 is yours.** It will read as a digital
input with a floating value.

---

## 3. (Removed in v0.1.4) VBUS-detect trace cut

This mod was required by the v0.1.3 design (which used GPIO 12
for I²S0 WS) but is **no longer necessary**. With the GPS
module removed in §1, GPIO 19 and GPIO 20 are free to take
over the I²S0 WS and BCLK roles, and GPIO 12 stays wired to
the USB voltage divider. The firmware reads VBUS state through
GPIO 12 normally — there is no "USB plugged in" UI gap.

If you have already performed this mod on a v0.1.3 build, you
can leave the trace cut in place; GPIO 12 will simply read as
floating-low when USB is unplugged (the resistor divider's
default state). The only practical effect is that the
firmware's USB-detect logic will misreport. Re-soldering a
jumper across the cut (see §8) restores stock behaviour.

---

## 4. Wiring the shared I²S0 bus

**Goal:** connect the INMP441 microphone and the MAX98357A
amplifier to the ESP32-S3's I²S0 peripheral in full-duplex
mode, sharing BCLK and WS.

### Pin map (recap)

| Signal | GPIO | Wires from |
|---|---|---|
| BCLK | 20 | ESP32 GPIO 20 (castellated) → splice to mic SCK and amp BCLK |
| WS | 19 | ESP32 GPIO 19 (castellated) → splice to mic WS and amp LRC |
| Mic SD (DIN) | 18 | mic SD pad → ESP32 GPIO 18 (castellated) |
| Amp DIN (DOUT) | 9 | amp DIN pad → ESP32 GPIO 9 (castellated) |

> **Why splice?** The mic and amp each expect their own SCK/WS
> pair. Since both devices run on the same I²S0 bus at the same
> sample rate, splicing their SCK and WS wires together at the
> ESP32 is electrically equivalent to running two separate
> clocks that happen to be bit-identical. The full-duplex mode
> drives both data lines simultaneously.

### Steps

1. **Identify the four ESP32-S3 castellated edge pads** you need:
   - GPIO 9 (top side, near the corner where the buzzer was)
   - GPIO 18 (top side, near the right edge)
   - GPIO 19 (top side, near the right edge)
   - GPIO 20 (top side, near the right edge)
2. **Tin each pad** with a small amount of fresh solder. The
   castellated edges are easy to solder to — they're just
   exposed metal.
3. **Cut four 5 cm pieces of 30 AWG silicone-jacketed wire.**
   Strip 1 mm on each end.
4. **Solder the mic-side wires** first:
   - Mic SCK → join to BCLK (GPIO 20) wire
   - Mic WS → join to WS (GPIO 19) wire
   - Mic SD → GPIO 18 wire
5. **Solder the amp-side wires:**
   - Amp BCLK → join to BCLK (GPIO 20) wire (same junction as
     mic SCK)
   - Amp LRC → join to WS (GPIO 19) wire (same junction as
     mic WS)
   - Amp DIN → GPIO 9 wire
6. **Power and ground.** The mic and amp each need 3.3 V and
   GND. Tap into the M5's existing 3.3 V rail (the same one
   that powers the PCA9557 and the LoRa). Use a GPIO from the
   right edge that you haven't used yet (e.g., GPIO 11 / 13,
   now free thanks to the GPS removal) as a 3.3 V tap; or
   solder directly to a nearby 3.3 V test point.
7. **Verify continuity** with a multimeter. Each of the four
   signal wires should show < 1 Ω from the ESP32 pad to the
   device pad.
8. **Verify no shorts.** Confirm infinite resistance between
   any two of the four signal wires (other than the
   BCLK–SCK and WS–LRC splices, which should be ~0 Ω).

---

## 5. Powering the LEDs and peripherals via PCA9557

After the I²S mods, you can optionally wire the LEDs and the
master peripheral power rail through the PCA9557. Tether's
firmware expects the PCA9557 to be present at I²C address 0x18
on Wire1 (GPIO 47 SDA, GPIO 48 SCL) — the I²C1 bus.

The M5 already has the PCA9557 chip on the board (it's how
Meshtastic drives the LEDs). No additional mods are needed to
use it from Tether. Just make sure the I²C1 bus on GPIOs 47/48
isn't being used by anything else — and after the GPS removal,
GPIO 19 / GPIO 20 are free (the I²S0 WS / BCLK lines that
previously conflicted are now safely on GPIO 19 / 20), so the
I²C1 pads are uncontested.

---

## 6. Verification

After both mods and the I²S wiring, do a final check
**before** flashing the firmware:

1. **Power the board on USB.** The red LED should illuminate
   (hardware-OR'd with VBUS).
2. **Check the 3.3 V rail.** Use a multimeter to confirm 3.3 V
   is present on the LoRa's VCC, the EPD's VCC, the
   mic's VCC, the amp's VCC, and the PCA9557's VCC.
3. **Check the mic and amp.** The mic's L/R pin should be tied
   to GND (left channel). The amp's SD pin should be tied to
   GND through a ~100 kΩ resistor (or directly — see the
   MAX98357A datasheet for the gain selection options).
4. **Flash the firmware.** `idf.py -p /dev/ttyUSB0 flash`.
5. **Check the serial monitor.** You should see the firmware
   print `i2s_mic init OK`, `i2s_amp init OK`, and `PCA9557
   ready`. The blue LED should illuminate briefly during
   init (the firmware doesn't drive it; this is the I²C bus
   pulling the line high during the initial register read).
6. **Test PTT.** Press button A. Speak. Release. You should
   see the audio frame counter increment in the serial
   output. Listen on the amp side — you should hear the
   audio loopback at low volume (a software-side check is
   available in `tether-loopback`).
7. **Test USB-detect.** With GPIO 12 still connected to the
   VBUS divider (because we did not cut its trace), plug and
   unplug USB and confirm the firmware reports the change.
   On the serial monitor you should see a `usb: plugged` /
   `usb: unplugged` line within ~100 ms of the physical
   event.

If any of the steps fail, double-check the wiring against the
schematic. Common pitfalls:

- **Mic SCK and amp BCLK not spliced** — they must be on the
  same wire, not two separate wires to the same GPIO. (Two
  separate wires to the same GPIO *would* work, but the
  splice approach is more reliable for hand-soldered builds.)
- **Mic L/R pin not tied to GND** — the INMP441 defaults to
  right channel if L/R is floating. Without a hard GND tie,
  the mic records silence.
- **Amp SD pin left floating** — the MAX98357A's SD pin
  selects the I²S format. If it's floating, the amp may
  default to a non-I²S mode and produce no output.
- **GPS module not fully removed** — if any of the LCC pads
  are still shorted, GPIO 19 / GPIO 20 will read as
  driven-by-GPS rather than floating. Re-touch the pads with
  solder wick.

---

## 7. What's sacrificed, in summary

| Hardware feature | Sacrificed for | Notes |
|---|---|---|
| GPS module (Quectel L76K) | GPIO 19 (WS), GPIO 20 (BCLK), and GPIO 10/11/13 as side-effects | GPS module is completely removed. No more ~25 mA drain. No position data (Tether never used it). |
| Buzzer (PWM audio) | GPIO 9 (DOUT) | No beep tones. Replaced by the blue LED. |

The M5's other features — SX1262 LoRa, EPD, SD, battery, USB-C
charging, buttons, USB VBUS detect (GPIO 12) — are **untouched**.

Compared to the v0.1.3 design, v0.1.4 has these trade-offs:

| | v0.1.3 (3 mods) | v0.1.4 (2 mods) |
|---|---|---|
| Sacrificed hardware | GPS, buzzer, VBUS detect | GPS, buzzer |
| GPS power drain | ~25 mA continuous (always-on hack) | 0 mA (module removed) |
| USB plug detection in firmware | no | **yes** (GPIO 12 intact) |
| Slider state observable | no (trace cut) | no (slider is vestigial with GPS gone) |
| Rollback difficulty | medium | harder (GPS module must be replaced to restore) |

---

## 8. Rollback

If you need to revert the M5 to stock:

1. **Solder a new buzzer** to the two pads (or a compatible
   SMD buzzer, e.g., Murata PKLCS1212E40A1-B0).
2. **Solder a replacement GPS module** to the 18 LCC pads.
   You will need a fresh Quectel L76K (or pin-compatible AT6558R
   module). Reflow at 320 °C with hot air, exactly as in
   reverse of step 1.7–1.8. **Reattach the U.FL antenna
   cable** before powering on, otherwise the GPS will not
   acquire a fix.
3. **Remove the I²S wiring** — desolder the four wires from
   the ESP32 castellated pads.

After rollback, flash the stock Meshtastic firmware and the
M5 will function as before, modulo any pad damage from the
desoldering (which is rare with a clean hot-air technique).
