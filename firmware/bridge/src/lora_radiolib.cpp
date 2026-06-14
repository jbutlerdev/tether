// lora_radiolib.cpp — RadioLib SX1262 backend (rak4631 env only).
//
// Compiled only when the PlatformIO build is targeting the real nRF52840
// (i.e. NOT defined(TETHER_BRIDGE_HOST_TEST)). The host-test build uses
// MockRadioBackend from lora.h directly.
//
// The backend implements RadioBackend against a RadioLib::SX1262 instance
// and a HardwareSPI bus. The SX1262's BUSY pin is polled before every
// radio command; RadioLib handles this internally for begin/end SPI
// transactions, but we expose WaitWhileBusy() at the RadioBackend level
// so callers (and tests) can assert the ordering.

#include "lora.h"

#if !defined(TETHER_BRIDGE_HOST_TEST)

#include <RadioLib.h>

#include <cstdint>
#include <span>
#include <vector>

namespace tether::bridge {

namespace {

// Convert tether::bridge::SpreadFactor → RadioLib's spreading factor.
int ToRadioLibSf(SpreadFactor sf) { return static_cast<int>(sf); }

// Convert tether::bridge::BandwidthHz → RadioLib bandwidth enum.
float ToRadioLibBwHz(BandwidthHz bw) { return static_cast<float>(bw); }

// Convert tether::bridge::CodingRate → RadioLib CR denominator.
int ToRadioLibCr(CodingRate cr) { return static_cast<int>(cr); }

} // namespace

class RadioLibBackend : public RadioBackend {
public:
  RadioLibBackend(int8_t pin_nss, int8_t pin_reset, int8_t pin_dio1,
                  int8_t pin_busy, SPIClass &spi)
      : radio_(new Module(spi, pin_nss, pin_reset, pin_dio1, pin_busy)),
        pin_busy_(pin_busy) {}

  void Configure(const Preset &preset) override {
    radio_.setSpreadingFactor(ToRadioLibSf(preset.spread_factor));
    radio_.setBandwidth(ToRadioLibBwHz(preset.bandwidth_hz));
    radio_.setCodingRate(ToRadioLibCr(preset.coding_rate));
    radio_.setSyncWord(preset.sync_word);
    radio_.setOutputPower(preset.tx_power_dbm);
  }

  void SetFrequency(uint64_t frequency_hz) override {
    radio_.setFrequency(static_cast<float>(frequency_hz) / 1'000'000.0f);
  }

  void WaitWhileBusy() override {
    // SX1262 BUSY pin: must be LOW before issuing the next SPI access.
    // RadioLib's Module.beginTransaction polls BUSY internally; we
    // additionally poll here so the call order is observable.
    const auto deadline = millis() + 100;
    while (digitalRead(pin_busy_) == HIGH) {
      if (millis() > deadline) {
        break; // give up; the radio is wedged
      }
    }
  }

  void Send(std::span<const uint8_t> packet) override {
    // RadioLib's transmit takes a (data, len) pair; copy into a
    // heap-backed vector because the API takes uint8_t* (non-const).
    std::vector<uint8_t> buf(packet.begin(), packet.end());
    radio_.transmit(buf.data(), static_cast<size_t>(buf.size()));
  }

  std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) override {
    // RadioLib receive uses a fixed-size buffer; 256 bytes matches
    // our kMaxFrameSize from frame.h.
    uint8_t buf[256];
    int state = radio_.receive(buf, sizeof(buf), 0, timeout_ms, true);
    if (state != RADIOLIB_ERR_NONE) {
      return std::nullopt;
    }
    const size_t len = radio_.getPacketLength(false);
    if (len == 0 || len > sizeof(buf)) {
      return std::nullopt;
    }
    return std::vector<uint8_t>(buf, buf + len);
  }

  bool StartCAD() override {
    return radio_.startChannelScan() == RADIOLIB_ERR_NONE;
  }

  void Sleep() override { radio_.sleep(); }
  void Standby() override { radio_.standby(); }

private:
  SX1262 radio_;
  int8_t pin_busy_;
};

} // namespace tether::bridge

#endif // !TETHER_BRIDGE_HOST_TEST
