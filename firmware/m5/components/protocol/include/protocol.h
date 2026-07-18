// protocol.h — Tether M5 on-target wire-format codec (research.md §8.1).
//
// This is the C++ mirror of the Go codec in go/pkg/protocol
// (header.go, ack.go). The M5 firmware encodes every outgoing LoRa
// envelope with Protocol::Encode and decodes every incoming one with
// Protocol::Decode; the bridge forwards opaque bytes, so the envelope
// codec must live on the M5 and the PC — and only the PC side
// (pkg/protocol) existed before this component.
//
// The wire format is a FIXED 34-byte binary header + payload
// (research.md §8.1), NOT protobuf. The header layout:
//
//   offset  size  field
//   0       2     target_id        (LE uint16; 0xFFFF = broadcast)
//   2       2     sender_id        (LE uint16)
//   4       16    conversation_id  (UUID)
//   20      4     message_id       (LE uint32; monotonic per conv)
//   24      2     seq_num          (LE uint16; chunk index)
//   26      2     total_seqs       (LE uint16; total chunks)
//   28      1     msg_type
//   29      1     flags            (bit0=RETRANSMIT, bit1=LAST_TTS)
//   30      1     audio_kind       (0=mic, 1=tts, 2=beep)
//   31      1     reserved         (0)
//   32      2     header_crc       (CRC-16/CCITT-FALSE over bytes 0..31)
//
// The ACK is a self-describing 28-byte payload (research.md §8.6):
// conv_id(16) + msg_id(4 LE) + next_expected_seq(2 LE) + bitmap_lo(2 LE)
// + bitmap_hi(2 LE) + crc16(2 LE over bytes 0..25).
//
// CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF, no reflect, no xorout.
// Reference vector: "123456789" → 0x29B1. This MUST match
// protocol.Crc16CCITT in the Go side byte-for-byte; the cross-validation
// test pins the same vectors on both sides.

#pragma once

#include <cstddef>
#include <cstdint>
#include <span>

namespace tether::m5 {

// Wire-format constants (must match go/pkg/protocol).
inline constexpr std::size_t kHeaderSize = 34;      // §8.1
inline constexpr std::size_t kMaxPayloadSize = 221; // 255 FIFO − 34 header
inline constexpr std::size_t kAckPayloadSize = 28;  // §8.6
inline constexpr std::size_t kConvIDSize = 16;

// Message types (match protocolpb.MsgType).
enum class MsgType : uint8_t {
  kUnspecified = 0,
  kStart = 1,
  kData = 2,
  kEnd = 3,
  kAck = 4,
  kTtsData = 5,
  kTtsEnd = 6,
  kUiUpdate = 7,
};

// Audio kinds (match protocolpb.AudioKind).
enum class AudioKind : uint8_t {
  kUnspecified = 0,
  kMic = 1,
  kTts = 2,
  kBeep = 3,
};

// Flag bits.
inline constexpr uint8_t kFlagRetransmit = 0x01;
inline constexpr uint8_t kFlagLastTtsChunk = 0x02;

// Header is the decoded view of the 34-byte wire header. The CRC is
// populated by Decode and consumed by Encode; callers usually ignore it.
struct Header {
  uint16_t target_id = 0xFFFF; // 0xFFFF = broadcast
  uint16_t sender_id = 0;
  uint8_t conversation_id[kConvIDSize] = {};
  uint32_t message_id = 0;
  uint16_t seq_num = 0;
  uint16_t total_seqs = 0;
  MsgType msg_type = MsgType::kUnspecified;
  uint8_t flags = 0;
  AudioKind audio_kind = AudioKind::kUnspecified;
  uint16_t header_crc = 0; // over bytes 0..31
};

// Ack is the decoded view of the 28-byte ACK payload (§8.6).
struct Ack {
  uint8_t conversation_id[kConvIDSize] = {};
  uint32_t message_id = 0;
  uint16_t next_expected_seq = 0;
  uint16_t ack_bitmap_lo = 0;
  uint16_t ack_bitmap_hi = 0;
  uint16_t crc = 0;
};

// Protocol is a stateless codec. All methods are pure functions of
// their inputs; the class groups the primitives under one namespace
// and gives the test suite a place to hang its hooks.
class Protocol {
public:
  Protocol() = default;

  // Crc16CCITT computes the CRC-16/CCITT-FALSE of buf. Matches the Go
  // protocol.Crc16CCITT byte-for-byte. Reference: "123456789" → 0x29B1.
  static uint16_t Crc16CCITT(std::span<const uint8_t> buf);

  // Encode serialises hdr + payload into out. out must hold at least
  // kHeaderSize + payload.size() bytes. Returns the total number of
  // bytes written, or 0 on error (payload too large or out too small).
  // The header_crc is computed over bytes 0..31 and written at offset 32.
  static std::size_t Encode(const Header &hdr, std::span<const uint8_t> payload,
                            std::span<uint8_t> out);

  // Decode parses a wire buffer into hdr and returns a span over the
  // payload (out points into buf). Returns an empty span on error
  // (truncated or CRC mismatch). The caller retains ownership of buf.
  static std::span<const uint8_t> Decode(std::span<const uint8_t> buf,
                                         Header &hdr);

  // EncodeAck serialises ack into the 28-byte fixed payload (§8.6).
  // out must hold at least kAckPayloadSize bytes. Returns the number of
  // bytes written (kAckPayloadSize) or 0 on error.
  static std::size_t EncodeAck(const Ack &ack, std::span<uint8_t> out);

  // DecodeAck parses a 28-byte ACK payload, verifying the CRC-16.
  // Returns true on success (ack populated), false on CRC/length error.
  static bool DecodeAck(std::span<const uint8_t> buf, Ack &ack);
};

} // namespace tether::m5
