// lora_sx1262.cpp — production RadioLib-backed implementation of
// tether::m5::LoraRadio (plan.md §4.2).
//
// On real hardware this file wraps the RadioLib SX1262 module. The
// M5 hardware design uses:
//   * SPI bus shared with SD and EPD (Bus() singleton).
//   * SX1262 BUSY pin (input) — must be polled before every SPI
//     transaction (research.md §7.4).
//   * SX1262 IRQ pin (input edge) — flag-setter only; radio task does
//     heavy SPI work after taking the mutex.
//   * SX1262 CS pin (output high), RST pin (output high).
//
// The PinMap comes from hardware.md and the ThinkNode M5 schematic.
// The radio task (firmware/m5/components/radio_task/) is the only
// caller of this class.

#include "lora_sx1262.h"

#include <RadioLib.h>

#include "esp_log.h"
#include "spi_bus.h"

namespace tether::m5 {

namespace {

// Pin map for the ThinkNode M5 (hardware.md). RST, BUSY, IRQ, and CS
// are hard-coded; the SPI bus is shared.
constexpr int8_t kPinSx1262Cs = 8;
constexpr int8_t kPinSx1262Reset = 12;
constexpr int8_t kPinSx1262Busy = 13;
constexpr int8_t kPinSx1262Irq = 14;

class RadioLibBackend : public RadioBackend {
 public:
  // The module is constructed on first use; the SPI bus mutex is taken
  // around every operation to honor the load-bearing pattern from
  // research.md §7.4.
  RadioLibBackend() : module_(new SX1262(new Module(kPinSx1262Cs,
                                                   /*irq=*/kPinSx1262Irq,
                                                   /*rst=*/kPinSx1262Reset,
                                                   /*busy=*/kPinSx1262Busy))) {}

  void Configure(const Preset &preset) override {
    int8_t sf = static_cast<int8_t>(preset.spread_factor);
    float bw = static_cast<float>(preset.bandwidth_hz) / 1000.0f; // kHz
    uint8_t cr = static_cast<uint8_t>(preset.coding_rate);
    int8_t err = module_->begin(bw, sf, cr,
                                preset.sync_word,
                                preset.tx_power_dbm,
                                /*preambleLength=*/16,
                                /*tcxoVoltage=*/1.6,
                                /*useRegulatorLDO=*/false);
    if (err != RADIOLIB_ERR_NONE) {
      ESP_LOGE("tether.lora", "begin() failed: %d", err);
    }
  }

  void SetFrequency(uint64_t frequency_hz) override {
    float mhz = static_cast<float>(frequency_hz) / 1'000'000.0f;
    int8_t err = module_->setFrequency(mhz);
    if (err != RADIOLIB_ERR_NONE) {
      ESP_LOGE("tether.lora", "setFrequency() failed: %d", err);
    }
  }

  void WaitWhileBusy() override {
    // RadioLib's Module class polls BUSY internally on every SPI
    // transaction, so this is a no-op. Kept for interface symmetry
    // with the mock and to allow unit tests to assert the ordering.
  }

  void Send(std::span<const uint8_t> packet) override {
    int8_t err = module_->transmit(packet.data(),
                                   static_cast<size_t>(packet.size()));
    if (err != RADIOLIB_ERR_NONE) {
      ESP_LOGW("tether.lora", "transmit() returned %d", err);
    }
  }

  std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) override {
    uint8_t buf[256];
    int16_t state = module_->receive(buf, sizeof(buf), 0);
    if (state != RADIOLIB_ERR_NONE) {
      return std::nullopt;
    }
    size_t n = module_->getPacketLength();
    if (n == 0 || n > sizeof(buf)) {
      return std::nullopt;
    }
    return std::vector<uint8_t>(buf, buf + n);
  }

  bool StartCAD() override {
    int16_t res = module_->startChannelScan();
    if (res == RADIOLIB_PREAMBLE_DETECTED) {
      return false; // busy
    }
    return true; // clear (CAD done, no preamble)
  }

  void Sleep() override { module_->sleep(); }
  void Standby() override { module_->standby(); }

 private:
  // Owned via a pointer so the destructor runs before the underlying
  // module is destroyed (the SX1262 object must outlive the SPI bus).
  std::unique_ptr<SX1262> module_;
};

} // namespace

LoraRadio::LoraRadio(std::shared_ptr<RadioBackend> backend)
    : backend_(std::move(backend)) {}

void LoraRadio::Init(const Preset &preset) {
  Bus().Lock(portMAX_DELAY);
  backend_->Configure(preset);
  Bus().Unlock();
}

bool LoraRadio::SetChannel(uint8_t ch) {
  auto channel = Channel::FromIndex(ch);
  if (!channel.has_value()) return false;
  Bus().Lock(portMAX_DELAY);
  backend_->SetFrequency(channel->frequency_hz);
  Bus().Unlock();
  return true;
}

bool LoraRadio::StartCAD() {
  Bus().Lock(portMAX_DELAY);
  backend_->WaitWhileBusy();
  bool clear = backend_->StartCAD();
  Bus().Unlock();
  return clear;
}

void LoraRadio::Transmit(std::span<const uint8_t> packet) {
  Bus().Lock(portMAX_DELAY);
  backend_->WaitWhileBusy();
  backend_->Send(packet);
  Bus().Unlock();
}

std::optional<std::vector<uint8_t>>
LoraRadio::ReceiveBlocking(uint32_t timeout_ms) {
  Bus().Lock(portMAX_DELAY);
  backend_->WaitWhileBusy();
  auto out = backend_->Receive(timeout_ms);
  Bus().Unlock();
  return out;
}

void LoraRadio::Sleep() {
  Bus().Lock(portMAX_DELAY);
  backend_->Sleep();
  Bus().Unlock();
}

void LoraRadio::Standby() {
  Bus().Lock(portMAX_DELAY);
  backend_->Standby();
  Bus().Unlock();
}

}  // namespace tether::m5
