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
//
// This file is integration glue (per plan §3.2 — "Most are integration
// tests on real hardware"). It is not unit-tested; the production logic
// is in lora.h (header-only) and exercised by src/lora_test.cpp against
// MockRadioBackend. We make a best-effort to keep this file compiling
// against the current RadioLib API; the bench rig on real hardware is
// the integration test for these code paths (plan §3.5).

#include "lora.h"

#if !defined(TETHER_BRIDGE_HOST_TEST)

#include <RadioLib.h>

#include <cstdint>
#include <span>
#include <vector>

namespace tether::bridge {

namespace {

// Convert tether::bridge::SpreadFactor → RadioLib's spreading factor.
uint8_t ToRadioLibSf(SpreadFactor sf) { return static_cast<uint8_t>(sf); }

// Convert tether::bridge::BandwidthHz → RadioLib bandwidth (kHz).
float ToRadioLibBwKHz(BandwidthHz bw) {
  return static_cast<float>(bw) / 1000.0f;
}

// Convert tether::bridge::CodingRate → RadioLib CR denominator.
uint8_t ToRadioLibCr(CodingRate cr) { return static_cast<uint8_t>(cr); }

// Fill a ConfigLoRa_t from a Preset + current frequency.
ConfigLoRa_t MakeConfig(const Preset &preset, float freq_mhz) {
  ConfigLoRa_t cfg{};
  cfg.frequency = freq_mhz;
  cfg.bandwidth = ToRadioLibBwKHz(preset.bandwidth_hz);
  cfg.spreadingFactor = ToRadioLibSf(preset.spread_factor);
  cfg.codingRate = ToRadioLibCr(preset.coding_rate);
  cfg.syncWord = preset.sync_word;
  cfg.power = static_cast<int8_t>(preset.tx_power_dbm);
  cfg.preambleLength = 8; // standard LoRa preamble; matches the bench
  return cfg;
}

} // namespace

class RadioLibBackend : public RadioBackend {
public:
  RadioLibBackend(uint32_t pin_nss, uint32_t pin_reset, uint32_t pin_dio1,
                  uint32_t pin_busy, SPIClass &spi)
      : module_(pin_nss, pin_dio1, pin_reset, pin_busy, spi), radio_(&module_),
        pin_busy_(static_cast<int8_t>(pin_busy)) {}

  void Configure(const Preset &preset) override {
    preset_ = preset;
    const ConfigLoRa_t cfg = MakeConfig(preset_, freq_mhz_);
    radio_.begin(cfg);
  }

  void SetFrequency(uint64_t frequency_hz) override {
    // Modern RadioLib does not expose a public setFrequency on SX126x.
    // Re-apply the entire config with the new frequency.
    freq_mhz_ = static_cast<float>(frequency_hz) / 1000000.0f;
    const ConfigLoRa_t cfg = MakeConfig(preset_, freq_mhz_);
    radio_.begin(cfg);
  }

  void WaitWhileBusy() override {
    // SX1262 BUSY pin: must be LOW before issuing the next SPI access.
    // RadioLib's Module.beginTransaction polls BUSY internally; we
    // additionally poll here so the call order is observable from
    // the bench test (plan §3.2: BUSY pin must be polled before xfer).
    const auto deadline = millis() + 100;
    while (digitalRead(pin_busy_) == HIGH) {
      if (millis() > deadline) {
        break; // give up; the radio is wedged
      }
    }
  }

  void Send(std::span<const uint8_t> packet) override {
    // RadioLib's transmit takes (data, len, addr=0). Copy the span
    // into a contiguous buffer because the API takes uint8_t* (non-
    // const) and span<const uint8_t> is read-only.
    std::vector<uint8_t> buf(packet.begin(), packet.end());
    radio_.transmit(buf.data(), buf.size());
  }

  std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) override {
    // RadioLib receive uses a fixed-size buffer; 256 bytes matches
    // our kMaxFrameSize from frame.h. The current API takes
    // (data, len, timeout); the previous overload with an irq flag
    // is gone.
    uint8_t buf[256];
    int state = radio_.receive(buf, sizeof(buf), timeout_ms);
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
  Module module_;
  SX1262 radio_;
  int8_t pin_busy_;
  Preset preset_{};
  float freq_mhz_ = 902.3f; // US915 ch 0; matches Channel::FromIndex(0)
};

} // namespace tether::bridge

#endif // !TETHER_BRIDGE_HOST_TEST
