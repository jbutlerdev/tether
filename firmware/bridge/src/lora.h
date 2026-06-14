// lora.h — abstract LoRa radio wrapper (plan.md §3.2).
//
// The real RAK4631 build (env:rak4631) wires LoRaRadio to a RadioLib
// SX1262 backend; the host test build (env:native) wires it to
// MockRadioBackend. Both backends implement the same RadioBackend
// interface so the production logic in lora.cpp can be tested without
// hardware.
//
// Channel mapping follows US915 (research.md §6.2):
//   ch 0..63 → 902.3 MHz + ch * 125 kHz, 64 channels of 125 kHz each.

#pragma once

#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace tether::bridge {

// ── Channel / preset enums and POD types ──────────────────────────────────

enum class SpreadFactor : uint8_t {
  kSF7 = 7,
  kSF8 = 8,
  kSF9 = 9,
  kSF10 = 10,
  kSF11 = 11,
  kSF12 = 12,
};

enum class BandwidthHz : uint32_t {
  k125kHz = 125'000,
  k250kHz = 250'000,
  k500kHz = 500'000,
};

enum class CodingRate : uint8_t {
  k4_5 = 5,
  k4_6 = 6,
  k4_7 = 7,
  k4_8 = 8,
};

struct Preset {
  SpreadFactor spread_factor = SpreadFactor::kSF11;
  BandwidthHz bandwidth_hz = BandwidthHz::k125kHz;
  CodingRate coding_rate = CodingRate::k4_8;
  int8_t tx_power_dbm = 20;
  uint8_t sync_word = 0xF3;

  static Preset Default() { return Preset{}; }
};

inline constexpr uint64_t kUs915StartHz = 902'300'000ULL; // ch 0
inline constexpr uint64_t kUs915StepHz = 125'000ULL;      // 125 kHz
inline constexpr uint8_t kUs915NumChannels = 64;

struct Channel {
  uint8_t index = 0;
  uint64_t frequency_hz = 0;

  // Compute the US915 channel frequency for a given index.
  // Returns nullopt for indices > 63 (the 64 uplink channels).
  static std::optional<Channel> FromIndex(uint8_t idx) {
    if (idx >= 64) {
      return std::nullopt;
    }
    return Channel{idx,
                   kUs915StartHz + static_cast<uint64_t>(idx) * kUs915StepHz};
  }
};

// ── Backend interface ─────────────────────────────────────────────────────

// Operations the LoRaRadio performs on the radio. Implementations: real
// RadioLib (rak4631 env) and MockRadioBackend (test env). The interface
// stays intentionally small; complex state (packet buffers, IRQ flags)
// lives in the backend, not in LoRaRadio.
class RadioBackend {
public:
  virtual ~RadioBackend() = default;

  // Configure the radio for the given preset. Idempotent.
  virtual void Configure(const Preset &preset) = 0;

  // Switch to a new carrier frequency (in Hz). The radio is left in
  // standby; subsequent TX/RX uses the new frequency.
  virtual void SetFrequency(uint64_t frequency_hz) = 0;

  // Wait for the BUSY pin to drop before issuing the next SPI access.
  // The SX1262 datasheet says the host MUST poll BUSY after every
  // command and before the next CS-low. RadioLib's beginTransaction
  // does this internally; the backend exposes it so we can assert the
  // ordering in tests.
  virtual void WaitWhileBusy() = 0;

  // Transmit a packet (blocking until the radio accepts it).
  virtual void Send(std::span<const uint8_t> packet) = 0;

  // Block until a packet arrives or timeout_ms elapses. Returns
  // nullopt on timeout.
  virtual std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) = 0;

  // Run Channel Activity Detection. Returns true if the channel is
  // free (radio did not see a LoRa preamble).
  virtual bool StartCAD() = 0;

  virtual void Sleep() = 0;
  virtual void Standby() = 0;
};

// ── Mock backend (host tests) ─────────────────────────────────────────────

class MockRadioBackend : public RadioBackend {
public:
  // Call log: append-only vector of operation names. The ordering of
  // entries in this vector is what tests assert on.
  std::vector<std::string> call_log;

  // Last-applied configuration.
  SpreadFactor last_spread_factor = SpreadFactor::kSF7;
  BandwidthHz last_bandwidth_hz = BandwidthHz::k125kHz;
  CodingRate last_coding_rate = CodingRate::k4_5;
  uint8_t last_sync_word = 0;
  int8_t last_tx_power_dbm = 0;
  uint64_t last_frequency_hz = 0;

  // Flags for one-shot observations.
  bool saw_init = false;
  bool saw_start_cad = false;
  bool saw_sleep = false;
  bool saw_standby = false;

  // Receive configuration.
  bool receive_returns_empty = false;
  std::vector<uint8_t> next_received;

  void Configure(const Preset &preset) override {
    saw_init = true;
    last_spread_factor = preset.spread_factor;
    last_bandwidth_hz = preset.bandwidth_hz;
    last_coding_rate = preset.coding_rate;
    last_sync_word = preset.sync_word;
    last_tx_power_dbm = preset.tx_power_dbm;
    call_log.push_back("configure");
  }
  void SetFrequency(uint64_t frequency_hz) override {
    last_frequency_hz = frequency_hz;
    call_log.push_back("set_frequency");
  }
  void WaitWhileBusy() override { call_log.push_back("busy_wait"); }
  void Send(std::span<const uint8_t> /*packet*/) override {
    call_log.push_back("busy_wait");
    call_log.push_back("spi_transfer");
  }
  std::optional<std::vector<uint8_t>>
  Receive(uint32_t /*timeout_ms*/) override {
    call_log.push_back("receive");
    if (receive_returns_empty || next_received.empty()) {
      return std::nullopt;
    }
    return next_received;
  }
  bool StartCAD() override {
    saw_start_cad = true;
    call_log.push_back("start_cad");
    return true;
  }
  void Sleep() override {
    saw_sleep = true;
    call_log.push_back("sleep");
  }
  void Standby() override {
    saw_standby = true;
    call_log.push_back("standby");
  }
};

// ── LoRaRadio (the production class) ──────────────────────────────────────

class LoRaRadio {
public:
  // Take ownership of an already-constructed backend. Tests pass a
  // MockRadioBackend; the real build passes a RadioLib-backed one.
  explicit LoRaRadio(std::shared_ptr<RadioBackend> backend)
      : backend_(std::move(backend)) {}

  void Init(const Preset &preset) { backend_->Configure(preset); }

  // Returns true on success, false on out-of-range channel.
  bool SetChannel(uint8_t ch) {
    auto channel = Channel::FromIndex(ch);
    if (!channel.has_value()) {
      return false;
    }
    backend_->SetFrequency(channel->frequency_hz);
    return true;
  }

  bool StartCAD() {
    backend_->WaitWhileBusy();
    return backend_->StartCAD();
  }

  void Transmit(std::span<const uint8_t> packet) {
    backend_->WaitWhileBusy();
    backend_->Send(packet);
  }

  std::optional<std::vector<uint8_t>> ReceiveBlocking(uint32_t timeout_ms) {
    backend_->WaitWhileBusy();
    return backend_->Receive(timeout_ms);
  }

  void Sleep() { backend_->Sleep(); }
  void Standby() { backend_->Standby(); }

private:
  std::shared_ptr<RadioBackend> backend_;
};

} // namespace tether::bridge
