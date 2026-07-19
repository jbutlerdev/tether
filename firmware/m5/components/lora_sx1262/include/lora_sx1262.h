// lora_sx1262.h — Tether M5 SX1262 LoRa driver (plan.md §4.2).
//
// Thin wrapper around RadioLib's SX1262 class. All RadioLib SPI calls
// are wrapped in Bus().Lock() / Bus().Unlock() to enforce the
// non-recursive mutex pattern from research.md §7.4. The driver is
// split into:
//   * An abstract `RadioBackend` interface implemented by RadioLib
//     on real hardware and `MockRadioBackend` on host tests.
//   * A `LoraRadio` production class that owns a backend and exposes
//     the public API used by the M5 radio task.

#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <vector>

namespace tether::m5 {

// ── Channel / preset enums and POD types ─────────────────────────────────

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

inline constexpr uint64_t kUs915StartHz = 902'300'000ULL;
inline constexpr uint64_t kUs915StepHz = 125'000ULL;
inline constexpr uint8_t kUs915NumChannels = 64;

struct Channel {
  uint8_t index = 0;
  uint64_t frequency_hz = 0;

  static std::optional<Channel> FromIndex(uint8_t idx) {
    if (idx >= 64)
      return std::nullopt;
    return Channel{idx,
                   kUs915StartHz + static_cast<uint64_t>(idx) * kUs915StepHz};
  }
};

// ── Radio backend interface ─────────────────────────────────────────────

// Operations LoraRadio performs on the SX1262. Implementations: real
// RadioLib (firmware) and MockRadioBackend (host tests).
class RadioBackend {
public:
  virtual ~RadioBackend() = default;

  virtual void Configure(const Preset &preset) = 0;
  virtual void SetFrequency(uint64_t frequency_hz) = 0;
  virtual void WaitWhileBusy() = 0;
  virtual void Send(std::span<const uint8_t> packet) = 0;
  virtual std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) = 0;
  virtual bool StartCAD() = 0;
  virtual void Sleep() = 0;
  virtual void Standby() = 0;
};

// ── Mock backend for host tests ────────────────────────────────────────

class MockRadioBackend : public RadioBackend {
public:
  std::vector<std::string> call_log;
  SpreadFactor last_spread_factor = SpreadFactor::kSF7;
  BandwidthHz last_bandwidth_hz = BandwidthHz::k125kHz;
  CodingRate last_coding_rate = CodingRate::k4_5;
  uint8_t last_sync_word = 0;
  int8_t last_tx_power_dbm = 0;
  uint64_t last_frequency_hz = 0;
  bool saw_init = false;
  bool saw_start_cad = false;
  bool saw_sleep = false;
  bool saw_standby = false;
  bool receive_returns_empty = false;
  std::vector<uint8_t> next_received;
  // Bytes captured from Send(), for assertion.
  std::vector<std::vector<uint8_t>> sent_packets;

  void Configure(const Preset &preset) override;
  void SetFrequency(uint64_t frequency_hz) override;
  void WaitWhileBusy() override;
  void Send(std::span<const uint8_t> packet) override;
  std::optional<std::vector<uint8_t>> Receive(uint32_t timeout_ms) override;
  bool StartCAD() override;
  void Sleep() override;
  void Standby() override;
};

// ── LoraRadio (production) ─────────────────────────────────────────────

class LoraRadio {
public:
  explicit LoraRadio(std::shared_ptr<RadioBackend> backend);

  // Initialize the radio with the given preset. The driver takes the SPI
  // bus mutex around the RadioLib begin() call.
  void Init(const Preset &preset);

  // Switch to channel `ch` (US915 uplink channels 0..63). Returns true on
  // success, false on out-of-range index.
  bool SetChannel(uint8_t ch);

  // Run Channel Activity Detection. Returns true if the channel is free.
  bool StartCAD();

  // Transmit a packet. Blocks until the radio accepts it.
  void Transmit(std::span<const uint8_t> packet);

  // Block until a packet arrives or `timeout_ms` elapses. Returns
  // nullopt on timeout.
  std::optional<std::vector<uint8_t>> ReceiveBlocking(uint32_t timeout_ms);

  void Sleep();
  void Standby();

private:
  std::shared_ptr<RadioBackend> backend_;
};

// MakeRadioLibBackend creates a RadioLib-backed RadioBackend for the
// M5/MVSR (SX1262 on the board's SPI bus). Only available in the
// ESP-IDF build (not the host test build). The pin map comes from
// board.h. Declared here so main.cpp can construct the radio without
// seeing the RadioLibBackend class (which lives in an anonymous
// namespace in lora_sx1262.cpp).
std::shared_ptr<RadioBackend> MakeRadioLibBackend();

} // namespace tether::m5
