// main.cpp — Tether RAK4631 bridge entry point.
//
// The bridge is a pass-through between the Go base station (USB-
// Serial, 921 600 baud, frame protocol from frame.h) and the LoRa
// radio (SX1262 via RadioLib). setup() brings up the serial port,
// initializes the SX1262 with the default preset (SF11/BW125/CR4/8,
// 20 dBm, sync 0xF3 — research.md §6.1), and constructs the
// SerialLink. loop() drives SerialLink::Step() in a tight loop and
// feeds the nRF52840 hardware watchdog every second.
//
// This file is compiled only in the [env:rak4631] environment; the
// [env:native] host test build excludes it via build_src_filter so the
// host harness can compile without <Arduino.h>.
//
// The RAK4631 wiring (SX1262 on the WisBlock): NSS=SS, DIO1=DIO1,
// RESET=NRST, BUSY=BUSY. These are the default RadioLib pins for the
// RAK4631; see RadioLib's src/modules/SX126x for the pin map.

#include <Arduino.h>
#include <RadioLib.h>
#include <SPI.h>

#include <cstdint>
#include <memory>
#include <span>

#include "frame.h"
#include "lora.h"
#include "lora_radiolib.h"
#include "serial_link.h"

namespace {

// Hardware watchdog feeding period. The nRF52840 WDT is configured
// for 4 s; we feed every 1 s so a single missed iteration still has
// slack before the reset fires.
constexpr uint32_t kWatchdogFeedPeriodMs = 1000;

// RAK4631 SX1262 pin map (WisBlock standard). These match the
// RadioLib default for the RAK4631 module.
constexpr uint32_t kPinSx1262Nss = SS;
constexpr uint32_t kPinSx1262Reset = RADIOLIB_NRST;
constexpr uint32_t kPinSx1262Dio1 = RADIOLIB_DIO1;
constexpr uint32_t kPinSx1262Busy = RADIOLIB_BUSY;

// File-scope pointer to the SerialLink, set in setup() and read in
// loop(). This avoids a global constructor-order problem (the link
// depends on Serial + SPI, which are initialized by the Arduino
// runtime before setup()).
std::shared_ptr<tether::bridge::SerialLink> g_link;

// ArduinoHardwareSerial wraps Arduino's Serial (the USB CDC port)
// as a SerialPort for the SerialLink. The RAK4631's native USB port
// appears as /dev/ttyACM0 on the host.
class ArduinoHardwareSerial : public tether::bridge::SerialPort {
public:
  explicit ArduinoHardwareSerial(HardwareSerial &serial) : serial_(serial) {}

  size_t Available() const override { return serial_.available(); }

  size_t Read(uint8_t *out, size_t len) override {
    return serial_.readBytes(out, len);
  }

  void Write(std::span<const uint8_t> bytes) override {
    serial_.write(bytes.data(), bytes.size());
  }

private:
  HardwareSerial &serial_;
};

} // namespace

void setup() {
  // USB-Serial at 921 600 baud (research.md §13.5 / frame.h).
  Serial.begin(921600);
  delay(100);
  Serial.println("tether: bridge boot");

  // Construct the LoRa radio: RadioLib backend on the RAK4631's
  // hardware SPI bus, wrapped in LoRaRadio.
  auto backend = tether::bridge::MakeRadioLibBackend(
      kPinSx1262Nss, kPinSx1262Reset, kPinSx1262Dio1, kPinSx1262Busy, SPI);
  auto radio = std::make_shared<tether::bridge::LoRaRadio>(backend);

  // Initialize with the default preset (research.md §6.1).
  radio->Init(tether::bridge::Preset::Default());
  radio->SetChannel(0); // US915 ch 0 = 902.3 MHz

  // Construct the serial link: ArduinoHardwareSerial + LoRaRadio.
  auto serial_port = std::make_shared<ArduinoHardwareSerial>(Serial);
  g_link = std::make_shared<tether::bridge::SerialLink>(serial_port, radio);

  Serial.println("tether: bridge ready");
}

void loop() {
  static uint32_t last_feed_ms = 0;

  // Drive the serial link: drain serial → configure/TX, attempt
  // LoRa RX, flush pending CAD/TX-done frames. This is the bridge's
  // entire job — it's a pass-through.
  if (g_link) {
    g_link->Step();
  }

  // Feed the nRF52840 hardware watchdog. Writing 0x6E524635 to
  // NRF_WDT->RR[0] reloads the counter.
  const uint32_t now = millis();
  if (now - last_feed_ms >= kWatchdogFeedPeriodMs) {
    last_feed_ms = now;
    volatile uint32_t *wdt_rr =
        reinterpret_cast<volatile uint32_t *>(0x40010600u);
    // cppcheck-suppress redundantAssignment
    *wdt_rr = 0x6E524635u;
  }
}
