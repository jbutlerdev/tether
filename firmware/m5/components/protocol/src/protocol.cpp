// protocol.cpp — Tether M5 on-target wire-format codec.
//
// Implementation of the fixed 34-byte header + payload encode/decode
// and the 28-byte ACK codec (research.md §8.1, §8.6). This MUST match
// the Go codec in go/pkg/protocol/header.go and ack.go byte-for-byte;
// the cross-validation tests pin the same CRC vectors and round-trips
// on both sides.
//
// The code is pure C++17 with no ESP-IDF or FreeRTOS dependencies, so
// the same translation unit builds for both the on-target firmware
// (via idf_component_register) and the host-side unit tests (via the
// TETHER_M5_HOST_TEST static library).

#include "protocol.h"

#include <cstring>

namespace tether::m5 {

namespace {

// Put a little-endian uint16 into dst[0..1].
void PutU16LE(uint8_t *dst, uint16_t v) {
  dst[0] = static_cast<uint8_t>(v & 0xFF);
  dst[1] = static_cast<uint8_t>((v >> 8) & 0xFF);
}

// Put a little-endian uint32 into dst[0..3].
void PutU32LE(uint8_t *dst, uint32_t v) {
  dst[0] = static_cast<uint8_t>(v & 0xFF);
  dst[1] = static_cast<uint8_t>((v >> 8) & 0xFF);
  dst[2] = static_cast<uint8_t>((v >> 16) & 0xFF);
  dst[3] = static_cast<uint8_t>((v >> 24) & 0xFF);
}

uint16_t GetU16LE(const uint8_t *src) {
  return static_cast<uint16_t>(src[0]) | (static_cast<uint16_t>(src[1]) << 8);
}

uint32_t GetU32LE(const uint8_t *src) {
  return static_cast<uint32_t>(src[0]) | (static_cast<uint32_t>(src[1]) << 8) |
         (static_cast<uint32_t>(src[2]) << 16) |
         (static_cast<uint32_t>(src[3]) << 24);
}

} // namespace

uint16_t Protocol::Crc16CCITT(std::span<const uint8_t> buf) {
  // Table-driven CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF,
  // no reflect, no xorout. Matches the Go implementation exactly.
  static uint16_t table[256];
  static bool table_init = false;
  if (!table_init) {
    for (int i = 0; i < 256; i++) {
      uint16_t crc = static_cast<uint16_t>(i) << 8;
      for (int j = 0; j < 8; j++) {
        if (crc & 0x8000) {
          crc = static_cast<uint16_t>((crc << 1) ^ 0x1021);
        } else {
          crc = static_cast<uint16_t>(crc << 1);
        }
      }
      table[i] = crc;
    }
    table_init = true;
  }
  uint16_t crc = 0xFFFF;
  for (uint8_t b : buf) {
    crc = static_cast<uint16_t>((crc << 8) ^ table[((crc >> 8) ^ b) & 0xFF]);
  }
  return crc;
}

std::size_t Protocol::Encode(const Header &hdr,
                             std::span<const uint8_t> payload,
                             std::span<uint8_t> out) {
  const std::size_t need = kHeaderSize + payload.size();
  if (out.size() < need)
    return 0;
  if (payload.size() > kMaxPayloadSize)
    return 0;

  // Bytes 0..1: target_id (LE uint16).
  PutU16LE(&out[0], hdr.target_id);
  // Bytes 2..3: sender_id.
  PutU16LE(&out[2], hdr.sender_id);
  // Bytes 4..19: conversation_id (16 bytes).
  std::memcpy(&out[4], hdr.conversation_id, kConvIDSize);
  // Bytes 20..23: message_id (LE uint32).
  PutU32LE(&out[20], hdr.message_id);
  // Bytes 24..25: seq_num (LE uint16).
  PutU16LE(&out[24], hdr.seq_num);
  // Bytes 26..27: total_seqs.
  PutU16LE(&out[26], hdr.total_seqs);
  // Byte 28: msg_type.
  out[28] = static_cast<uint8_t>(hdr.msg_type);
  // Byte 29: flags.
  out[29] = hdr.flags;
  // Byte 30: audio_kind.
  out[30] = static_cast<uint8_t>(hdr.audio_kind);
  // Byte 31: reserved (0).
  out[31] = 0;
  // Bytes 32..33: header_crc over bytes 0..31.
  const uint16_t crc = Crc16CCITT(out.subspan(0, 32));
  PutU16LE(&out[32], crc);
  // Payload.
  if (!payload.empty()) {
    std::memcpy(&out[kHeaderSize], payload.data(), payload.size());
  }
  return need;
}

std::span<const uint8_t> Protocol::Decode(std::span<const uint8_t> buf,
                                          Header &hdr) {
  if (buf.size() < kHeaderSize)
    return {};
  const uint16_t stored = GetU16LE(&buf[32]);
  if (Crc16CCITT(buf.subspan(0, 32)) != stored)
    return {};

  hdr.target_id = GetU16LE(&buf[0]);
  hdr.sender_id = GetU16LE(&buf[2]);
  std::memcpy(hdr.conversation_id, &buf[4], kConvIDSize);
  hdr.message_id = GetU32LE(&buf[20]);
  hdr.seq_num = GetU16LE(&buf[24]);
  hdr.total_seqs = GetU16LE(&buf[26]);
  hdr.msg_type = static_cast<MsgType>(buf[28]);
  hdr.flags = buf[29];
  hdr.audio_kind = static_cast<AudioKind>(buf[30]);
  hdr.header_crc = stored;
  return buf.subspan(kHeaderSize);
}

std::size_t Protocol::EncodeAck(const Ack &ack, std::span<uint8_t> out) {
  if (out.size() < kAckPayloadSize)
    return 0;
  // Bytes 0..15: conversation_id.
  std::memcpy(&out[0], ack.conversation_id, kConvIDSize);
  // Bytes 16..19: message_id (LE uint32).
  PutU32LE(&out[16], ack.message_id);
  // Bytes 20..21: next_expected_seq (LE uint16).
  PutU16LE(&out[20], ack.next_expected_seq);
  // Bytes 22..23: ack_bitmap_lo.
  PutU16LE(&out[22], ack.ack_bitmap_lo);
  // Bytes 24..25: ack_bitmap_hi.
  PutU16LE(&out[24], ack.ack_bitmap_hi);
  // Bytes 26..27: crc16 over bytes 0..25.
  const uint16_t crc = Crc16CCITT(out.subspan(0, 26));
  PutU16LE(&out[26], crc);
  return kAckPayloadSize;
}

bool Protocol::DecodeAck(std::span<const uint8_t> buf, Ack &ack) {
  if (buf.size() != kAckPayloadSize)
    return false;
  const uint16_t stored = GetU16LE(&buf[26]);
  if (Crc16CCITT(buf.subspan(0, 26)) != stored)
    return false;

  std::memcpy(ack.conversation_id, &buf[0], kConvIDSize);
  ack.message_id = GetU32LE(&buf[16]);
  ack.next_expected_seq = GetU16LE(&buf[20]);
  ack.ack_bitmap_lo = GetU16LE(&buf[22]);
  ack.ack_bitmap_hi = GetU16LE(&buf[24]);
  ack.crc = stored;
  return true;
}

} // namespace tether::m5
