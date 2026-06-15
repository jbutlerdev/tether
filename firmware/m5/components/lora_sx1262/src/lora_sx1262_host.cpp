// lora_sx1262_host.cpp — host-side implementation of LoraRadio.
//
// On real hardware the production class is wired to a RadioLib-backed
// RadioBackend; on host it is wired to MockRadioBackend. The
// MockRadioBackend methods are defined here so the test binary links
// cleanly. Real RadioLib wiring lives in lora_sx1262.cpp.

#ifdef TETHER_M5_HOST_TEST

#include "lora_sx1262.h"

#include <cstring>

#include "spi_bus.h"

namespace tether::m5 {

// ── MockRadioBackend ────────────────────────────────────────────────────

void MockRadioBackend::Configure(const Preset &preset) {
  saw_init = true;
  last_spread_factor = preset.spread_factor;
  last_bandwidth_hz = preset.bandwidth_hz;
  last_coding_rate = preset.coding_rate;
  last_sync_word = preset.sync_word;
  last_tx_power_dbm = preset.tx_power_dbm;
  call_log.push_back("configure");
}

void MockRadioBackend::SetFrequency(uint64_t frequency_hz) {
  last_frequency_hz = frequency_hz;
  call_log.push_back("set_frequency");
}

void MockRadioBackend::WaitWhileBusy() { call_log.push_back("busy_wait"); }

void MockRadioBackend::Send(std::span<const uint8_t> packet) {
  // Record the busy-wait and the SPI transfer so the test can assert
  // their ordering (research.md §7.4).
  call_log.push_back("busy_wait");
  call_log.push_back("spi_transfer");
  sent_packets.emplace_back(packet.begin(), packet.end());
}

std::optional<std::vector<uint8_t>>
MockRadioBackend::Receive(uint32_t /*timeout_ms*/) {
  call_log.push_back("receive");
  if (receive_returns_empty || next_received.empty()) {
    return std::nullopt;
  }
  return next_received;
}

bool MockRadioBackend::StartCAD() {
  saw_start_cad = true;
  call_log.push_back("start_cad");
  return true; // mock always reports clear
}

void MockRadioBackend::Sleep() {
  saw_sleep = true;
  call_log.push_back("sleep");
}

void MockRadioBackend::Standby() {
  saw_standby = true;
  call_log.push_back("standby");
}

// ── LoraRadio ──────────────────────────────────────────────────────────

LoraRadio::LoraRadio(std::shared_ptr<RadioBackend> backend)
    : backend_(std::move(backend)) {}

void LoraRadio::Init(const Preset &preset) {
  // Take the SPI bus mutex around the RadioLib begin() call (real
  // hardware) or the fake Configure() (host). research.md §7.4.
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

#endif  // TETHER_M5_HOST_TEST
