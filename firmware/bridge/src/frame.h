// frame.h — line-framed binary protocol between the RAK4631 bridge and the
// Go base station (tetherd).
//
// Wire format (little-endian):
//   0xAA 0x55  <type:1>  <len_lo:1>  <len_hi:1>  <payload:len>  <crc_lo:1>
//   <crc_hi:1>
//
// The CRC is CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no reflection,
// no XOR-out) over the bytes from <type> through the last payload byte.
// See plan.md §3.1.

#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <vector>

namespace tether::bridge {

inline constexpr uint8_t kMagic0 = 0xAA;
inline constexpr uint8_t kMagic1 = 0x55;
inline constexpr uint16_t kMaxFrameSize = 256;

enum class FrameType : uint8_t {
  kTxDone = 0x01,
  kRxPacket = 0x02,
  kAck = 0x03,
  kCadResult = 0x04,
  kSetConfig = 0x10,
  kLog = 0x80,
  kError = 0xFF,
};

struct Frame {
  FrameType type = FrameType::kError;
  // cppcheck flags these as unused when only the header is scanned, but
  // they are the wire-view round-trip of a decoded frame: callers can
  // inspect them, and the encoder reads `payload` while the decoder fills
  // all three.
  // cppcheck-suppress unusedStructMember
  std::array<uint8_t, 2> length{0, 0}; // LE, payload byte count
  // cppcheck-suppress unusedStructMember
  std::vector<uint8_t> payload; // 0..65535 bytes
  // cppcheck-suppress unusedStructMember
  std::array<uint8_t, 2> crc{0, 0}; // LE, CRC-16/CCITT over type..payload
};

// Encode a Frame to bytes for transmission over Serial.
// Throws std::invalid_argument if payload > 65535 bytes.
std::vector<uint8_t> EncodeFrame(const Frame &f);

// Decode bytes from Serial into a Frame. Returns nullopt on bad magic,
// bad length, bad CRC, or truncated.
std::optional<Frame> DecodeFrame(std::span<const uint8_t> bytes);

// Streaming decoder: accumulates bytes, emits complete frames.
class FrameDecoder {
public:
  void Feed(std::span<const uint8_t> bytes);
  std::optional<Frame> Next();

private:
  enum class State : uint8_t {
    kWaitMagic0,
    kWaitMagic1,
    kWaitType,
    kWaitLenLo,
    kWaitLenHi,
    kWaitPayload,
    kWaitCrcLo,
    kWaitCrcHi,
  };
  State state_ = State::kWaitMagic0;
  Frame pending_{};
  uint16_t payload_remaining_ = 0;
  std::vector<uint8_t> scratch_;
};

} // namespace tether::bridge
