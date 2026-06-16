# Hardware Modifications — ThinkNode M5 for Tether v0.1.3

This document describes the **physical modifications** that must be
performed on the Elecrow ThinkNode M5 board before flashing the
Tether firmware. These mods are required because the stock M5 has
**only one natively free GPIO** (GPIO 18); the rest of the
right-edge and bottom-edge pads are wired to peripherals
(SX1262 LoRa, EPD, SD, LEDs, GPS, buzzer, etc.) that we cannot
remap without desoldering.

The Tether firmware assumes the mods below have been performed. If
you flash the firmware onto an unmodified M5, the I²S0 audio bus
will not work (GPIOs 9/10/12 are still owned by the buzzer, GPS
switch, and VBUS detect respectively) and the audio path will
record silence / output nothing.

---

## TL;DR — what you need to do

Three mods, in this order:

1. **GPS "Always-On" hack** — bypass the L76K load switch, sever
   the trace back to GPIO 10. *Frees GPIO 10 for I²S0 BCLK.*
2. **Buzzer removal** — desolder the SMD buzzer. *Frees GPIO 9
   for I²S0 DOUT (amp DIN).*
3. **Power-Detect trace cut** — sever the trace from the USB
   voltage divider to GPIO 12. *Frees GPIO 12 for I²S0 WS (LRC).*

The shared I²S0 bus is then wired full-duplex:

| Signal | GPIO | Source |
|---|---|---|
| WS (LRC) | 12 | shared by mic and amp |
| BCLK | 10 | shared by mic and amp |
| Mic SD (DIN) | 18 | mic → ESP32 |
| Amp DIN (DOUT) | 9 | ESP32 → amp |

Do the mods in the order above. Each one frees a pin that the next
step needs to use as a soldering pad.

**Tools required:** temperature-controlled soldering iron (350 °C
with a fine tip), fine-gauge wire (30 AWG silicone-jacketed is
good), flux pen, hot-air station or hot plate (for the buzzer
removal), fine-tip tweezers, loupe or USB microscope, multimeter
for continuity checks.

**Time required:** 60–90 minutes for a first attempt. The buzzer
removal is the hardest step; the GPS hack is the easiest.

**Skill level:** experienced SMD rework. If you have only done
through-hole soldering before, practice on a scrap board first.

---

## 1. The GPS "Always-On" hack

**Goal:** bypass the load switch that powers the Quectel L76K GPS
module, and sever the trace from that switch's control pin back to
the ESP32-S3's GPIO 10. After this, the GPS module is permanently
powered (3.3 V) regardless of the case slider's position, and
GPIO 10 is electrically isolated and safe to use for I²S.

### Why this works

The M5 schematic shows a small MOSFET (Q-something) between the
3.3 V rail and the GPS module's VCC. The MOSFET's gate is driven
by the ESP32 through a GPIO; the case slider is a separate
mechanical switch wired to a different GPIO. The slider *only*
reports position to the ESP32; the actual power-gating is done by
the MOSFET. By shorting across the MOSFET (drain ↔ source) and
severing the gate trace, we hard-wire the GPS to 3.3 V and
isolate the slider GPIO.

> **Tether does not use the GPS.** This mod is purely to free
> GPIO 10. After the mod, the GPS is always on, drawing ~25 mA
> continuously. This is acceptable for a tethered bench setup; if
> you need longer battery life, use a hardware switch on the
> GPS's VCC trace instead.

### Steps

1. **Discharge yourself.** Touch a grounded metal surface.
   ESP32-S3 and the GPS module are both ESD-sensitive.
2. **Locate the load switch.** It is a small SOT-23 (3-pin)
   MOSFET immediately adjacent to the Quectel GPS module's VCC
   pad, on the underside of the PCB. Reference: see the M5
   schematic in the Elecrow GitHub repo, page 4 (power tree).
3. **Apply flux to the MOSFET's drain and source pins.** A flux
   pen works well.
4. **Solder a jumper** (a short piece of 30 AWG wire) from the
   drain pad to the source pad. This bypasses the MOSFET.
5. **Verify continuity** with a multimeter. You should see ~0 Ω
   between the GPS VCC and the 3.3 V rail. If you do, the
   bypass is good.
6. **Power the board on USB** (without the firmware flashed —
   just USB power). The GPS module's LED should illuminate within
   a few seconds, indicating it has power. (The original
   firmware is still on the ESP32 at this point; we're only
   checking the power rail.)
7. **Locate the trace** from the MOSFET's gate pad back to the
   ESP32. This is a thin trace, ~0.2 mm wide, on the top side of
   the PCB near the GPS module.
8. **Sever the trace** with a sharp X-Acto knife or a fine
   scalpel. Cut twice (about 1 mm apart) and lift the
   intervening trace segment off the board. Verify with a
   multimeter that there is now infinite resistance between
   the MOSFET gate and GPIO 10 (the ESP32-S3's GPIO 10 pad is
   on the top side, near the edge of the castellated module
   outline).
9. **Re-test.** Power the board. The GPS LED should still be on
   (bypass still works) and the case slider should have no
   effect on the GPS power (because we've severed the gate
   trace).

**After this mod, GPIO 10 is yours.** It will read as a digital
input with a floating value; pull-up or pull-down externally
when you wire it to I²S BCLK.

---

## 2. Buzzer removal

**Goal:** desolder the SMD buzzer from the M5 PCB. This frees
GPIO 9 (the buzzer's PWM drive pin) for I²S0 DOUT.

### Why this works

The M5's buzzer is a tiny SMD component, ~6 mm × 6 mm, mounted
on the top side of the PCB. Its PWM drive pin is GPIO 9. Once
the buzzer is gone, GPIO 9 is electrically isolated except for
the ESP32's pad.

> **Tether does not use the buzzer in v0.1.3.** The blue
> notification LED (driven by the PCA9557) provides user feedback.
> If you want a buzzer, see the v0.2.0 hook (a future
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

## 3. Power-Detect trace cut

**Goal:** sever the trace from the USB voltage divider (the
VBUS-sensing resistor ladder) to the ESP32-S3's GPIO 12. This
frees GPIO 12 for I²S0 WS.

### Why this works

The M5's USB-C VBUS line is divided down to ~3.3 V logic level
by a resistor pair and fed to GPIO 12 so the firmware can
detect when the user has plugged in USB. The charging IC works
independently — it just monitors VBUS directly and doesn't need
the ESP32 to know. Severing the trace cuts the firmware's
knowledge of USB state but leaves the charger fully functional.

> **Tether does not have a "USB plugged in" UI in v0.1.3.** The
> red power LED (driven by the PCA9557) is OR'd with VBUS by
> hardware, so it will still illuminate when USB is connected.
> The firmware just won't be able to programmatically detect
> USB state through GPIO 12. v0.2.0 will use the ESP32-S3's
> built-in USB-OTG VBUS detection instead.

### Steps

1. **Locate the trace.** It runs from the VBUS resistor ladder
   (a pair of 100 kΩ resistors near the USB-C connector) to
   the GPIO 12 pad on the ESP32. The trace is on the top side
   of the PCB and is ~0.2 mm wide.
2. **Apply flux** to a 2 mm section of the trace, near the
   midpoint (away from the resistor and away from the ESP32
   pad).
3. **Sever the trace** with a scalpel. Cut twice about 1 mm
   apart and lift the segment. Alternatively, use a fine
   soldering iron tip to scrape away the trace between the two
   cuts.
4. **Verify** with a multimeter. You should see infinite
   resistance between the resistor-ladder output and the GPIO
   12 pad on the ESP32.
5. **Verify the charger still works.** Plug in USB. The red
   LED on the M5 should illuminate (it's OR'd with VBUS
   hardware-side, so it's lit by the charger regardless of
   firmware). The battery should charge normally.

**After this mod, GPIO 12 is yours.** It will read as a digital
input with a floating value; pull-up externally when wiring.

---

## 4. Wiring the shared I²S0 bus

**Goal:** connect the INMP441 microphone and the MAX98357A
amplifier to the ESP32-S3's I²S0 peripheral in full-duplex mode,
sharing BCLK and WS.

### Pin map (recap)

| Signal | GPIO | Wires from |
|---|---|---|
| BCLK | 10 | ESP32 GPIO 10 (castellated) → splice to mic SCK and amp BCLK |
| WS | 12 | ESP32 GPIO 12 (castellated) → splice to mic WS and amp LRC |
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
   - GPIO 10 (top side, near the GPS switch's severed trace)
   - GPIO 12 (top side, near the VBUS resistor ladder)
   - GPIO 18 (top side, near the right edge)
2. **Tin each pad** with a small amount of fresh solder. The
   castellated edges are easy to solder to — they're just
   exposed metal.
3. **Cut four 5 cm pieces of 30 AWG silicone-jacketed wire.**
   Strip 1 mm on each end.
4. **Solder the mic-side wires** first:
   - Mic SCK → join to BCLK (GPIO 10) wire
   - Mic WS → join to WS (GPIO 12) wire
   - Mic SD → GPIO 18 wire
5. **Solder the amp-side wires:**
   - Amp BCLK → join to BCLK (GPIO 10) wire (same junction as
     mic SCK)
   - Amp LRC → join to WS (GPIO 12) wire (same junction as mic
     WS)
   - Amp DIN → GPIO 9 wire
6. **Power and ground.** The mic and amp each need 3.3 V and
   GND. Tap into the M5's existing 3.3 V rail (the same one
   that powers the PCA9557 and the LoRa). Use a GPIO from the
   right edge that you haven't used yet (e.g., GPIO 13 / 14,
   if free) as a 3.3 V tap; or solder directly to a nearby
   3.3 V test point.
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
isn't being used by anything else — and after the buzzer
removal, GPIO 9 is free (was the buzzer PWM) and after the
trace cut, GPIO 12 is free (was VBUS detect), so the I²C1
pads are uncontested.

---

## 6. Verification

After all three mods and the I²S wiring, do a final check
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

---

## 7. What's sacrificed, in summary

| Hardware feature | Sacrificed for | Notes |
|---|---|---|
| GPS switch (slider) | GPIO 10 | GPS module is now always on. ~25 mA continuous drain. |
| Buzzer (PWM audio) | GPIO 9 | No beep tones. Replaced by the blue LED. |
| VBUS detect (USB sense) | GPIO 12 | No "USB plugged in" UI. v0.2.0 will use the ESP32-S3's built-in VBUS detection. |

The M5's other features — SX1262 LoRa, EPD, SD, battery, USB-C
charging, buttons, GPS module (still functional, just always
powered) — are untouched.

---

## 8. Rollback

If you need to revert the M5 to stock:

1. **Restore the trace cuts** with 30 AWG wire jumpers:
   - GPS gate trace: solder a jumper across the cut to
     re-connect the gate to GPIO 10.
   - VBUS detect trace: solder a jumper across the cut to
     re-connect the resistor ladder to GPIO 12.
2. **Remove the GPS bypass jumper** (the drain-source wire
   you soldered in step 1.4 of the GPS hack). The MOSFET will
   resume its power-gating function.
3. **Solder a new buzzer** to the two pads (or a compatible
   SMD buzzer, e.g., Murata PKLCS1212E40A1-B0).
4. **Remove the I²S wiring** — desolder the four wires from
   the ESP32 castellated pads.

After rollback, flash the stock Meshtastic firmware and the
M5 will function as before.
