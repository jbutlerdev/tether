// test_protocol.cpp — TDD unit tests for tether::m5::Protocol.
//
// Cross-validates the C++ wire-format codec against the same CRC
// vectors and round-trips the Go side (go/pkg/protocol) pins. The
// codec MUST be byte-for-byte compatible: the M5 encodes, the PC
// decodes (and vice versa), so a divergence here corrupts every
// packet on the link.
//
// Coverage:
//   1. CRC-16/CCITT-FALSE canonical vectors (must match the Go test).
//   2. Envelope Encode/Decode round-trip with non-zero fields.
//   3. Max-payload boundary and over-size rejection.
//   4. CRC corruption detection (header bit flip + CRC field flip).
//   5. Truncation guard.
//   6. ACK 28-byte round-trip + CRC rejection + self-describing
//      conv_id/msg_id.

#include <cstdint>
#include <cstring>
#include <span>

#include <unity.h>

#include "protocol.h"

using tether::m5::Ack;
using tether::m5::AudioKind;
using tether::m5::Header;
using tether::m5::kAckPayloadSize;
using tether::m5::kConvIDSize;
using tether::m5::kHeaderSize;
using tether::m5::kMaxPayloadSize;
using tether::m5::MsgType;
using tether::m5::Protocol;

// ── Helpers ──────────────────────────────────────────────────────────

namespace {

// Build a known-good header with non-zero fields.
Header makeHeader() {
  Header h;
  h.target_id = 0xFFFF;
  h.sender_id = 0x0001;
  for (size_t i = 0; i < kConvIDSize; i++)
    h.conversation_id[i] = 0xAB;
  h.message_id = 0xDEADBEEF;
  h.seq_num = 0;
  h.total_seqs = 1;
  h.msg_type = MsgType::kData;
  h.audio_kind = AudioKind::kMic;
  h.flags = 0;
  return h;
}

bool headersEqual(const Header &a, const Header &b) {
  return a.target_id == b.target_id && a.sender_id == b.sender_id &&
         a.message_id == b.message_id && a.seq_num == b.seq_num &&
         a.total_seqs == b.total_seqs && a.msg_type == b.msg_type &&
         a.flags == b.flags && a.audio_kind == b.audio_kind &&
         std::memcmp(a.conversation_id, b.conversation_id, kConvIDSize) == 0;
}

} // namespace

// Unity requires setUp/tearDown even if empty.
void setUp(void) {}
void tearDown(void) {}

// ── CRC vectors (must match go/pkg/protocol header_test.go) ──────────

void test_crc_canonical_vectors(void) {
  // "123456789" → 0x29B1 (the CRC-16/CCITT-FALSE check value).
  const uint8_t check[] = {'1', '2', '3', '4', '5', '6', '7', '8', '9'};
  TEST_ASSERT_EQUAL(0x29B1, Protocol::Crc16CCITT(check));

  // Empty → init 0xFFFF.
  TEST_ASSERT_EQUAL(0xFFFF, Protocol::Crc16CCITT({}));

  // Single 0x00 → 0xE1F0.
  const uint8_t zero[] = {0x00};
  TEST_ASSERT_EQUAL(0xE1F0, Protocol::Crc16CCITT(zero));

  // 0xFF × 4 → 0x1D0F.
  const uint8_t ffff[] = {0xFF, 0xFF, 0xFF, 0xFF};
  TEST_ASSERT_EQUAL(0x1D0F, Protocol::Crc16CCITT(ffff));
}

// ── Envelope round-trip ──────────────────────────────────────────────

void test_envelope_round_trip(void) {
  Header h = makeHeader();
  uint8_t payload[32];
  std::memset(payload, 0xCC, sizeof(payload));

  uint8_t wire[kHeaderSize + 32];
  std::size_t n = Protocol::Encode(h, payload, wire);
  TEST_ASSERT_EQUAL_size_t(kHeaderSize + 32, n);

  Header got;
  auto pl = Protocol::Decode(wire, got);
  TEST_ASSERT_EQUAL_size_t(32, pl.size());
  TEST_ASSERT_TRUE(headersEqual(h, got));
  TEST_ASSERT_EQUAL(0x29B1 != 0, got.header_crc != 0); // non-zero
  TEST_ASSERT_EQUAL_UINT8(0xCC, pl[0]);
}

// ── Max payload boundary ─────────────────────────────────────────────

void test_envelope_max_payload(void) {
  Header h = makeHeader();
  uint8_t payload[kMaxPayloadSize];
  std::memset(payload, 0x42, sizeof(payload));

  uint8_t wire[kHeaderSize + kMaxPayloadSize];
  std::size_t n = Protocol::Encode(h, payload, wire);
  TEST_ASSERT_EQUAL_size_t(kHeaderSize + kMaxPayloadSize, n);

  Header got;
  auto pl = Protocol::Decode(wire, got);
  TEST_ASSERT_EQUAL_size_t(kMaxPayloadSize, pl.size());
}

void test_envelope_oversized_payload_rejected(void) {
  Header h = makeHeader();
  uint8_t payload[kMaxPayloadSize + 1] = {};
  uint8_t wire[kHeaderSize + kMaxPayloadSize + 1];
  std::size_t n = Protocol::Encode(h, payload, wire);
  TEST_ASSERT_EQUAL_size_t(0, n); // rejected
}

// ── CRC corruption detection ─────────────────────────────────────────

void test_envelope_bad_crc_header_flip(void) {
  Header h = makeHeader();
  uint8_t payload[16] = {};
  uint8_t wire[kHeaderSize + 16];
  Protocol::Encode(h, payload, wire);
  // Flip a bit in the message_id field (offset 20, inside CRC region).
  wire[20] ^= 0x01;
  Header got;
  auto pl = Protocol::Decode(wire, got);
  TEST_ASSERT_EQUAL_size_t(0, pl.size()); // CRC mismatch → empty
}

void test_envelope_bad_crc_field_flip(void) {
  Header h = makeHeader();
  uint8_t payload[16] = {};
  uint8_t wire[kHeaderSize + 16];
  Protocol::Encode(h, payload, wire);
  wire[32] ^= 0x01; // corrupt the CRC field itself
  Header got;
  auto pl = Protocol::Decode(wire, got);
  TEST_ASSERT_EQUAL_size_t(0, pl.size());
}

// ── Truncation ───────────────────────────────────────────────────────

void test_envelope_truncated(void) {
  uint8_t buf[5] = {};
  Header got;
  auto pl = Protocol::Decode(buf, got);
  TEST_ASSERT_EQUAL_size_t(0, pl.size());
}

// ── ACK round-trip (28-byte, §8.6) ───────────────────────────────────

void test_ack_round_trip(void) {
  Ack a;
  for (size_t i = 0; i < kConvIDSize; i++)
    a.conversation_id[i] = 0x11;
  a.message_id = 99;
  a.next_expected_seq = 0xFFE0;
  a.ack_bitmap_lo = 0xFFFF;
  a.ack_bitmap_hi = 0xFFFF;

  uint8_t wire[kAckPayloadSize];
  std::size_t n = Protocol::EncodeAck(a, wire);
  TEST_ASSERT_EQUAL_size_t(kAckPayloadSize, n);

  Ack got;
  TEST_ASSERT_TRUE(Protocol::DecodeAck(wire, got));
  TEST_ASSERT_EQUAL_UINT32(99, got.message_id);
  TEST_ASSERT_EQUAL(0xFFE0, got.next_expected_seq);
  TEST_ASSERT_EQUAL(0xFFFF, got.ack_bitmap_lo);
  TEST_ASSERT_EQUAL(0xFFFF, got.ack_bitmap_hi);
  TEST_ASSERT_EQUAL_MEMORY(a.conversation_id, got.conversation_id, kConvIDSize);
}

void test_ack_crc_rejects_corruption(void) {
  Ack a;
  for (size_t i = 0; i < kConvIDSize; i++)
    a.conversation_id[i] = 0x22;
  a.message_id = 1;
  a.next_expected_seq = 4;
  a.ack_bitmap_lo = 0x00FF;
  uint8_t wire[kAckPayloadSize];
  Protocol::EncodeAck(a, wire);
  wire[22] ^= 0x01; // corrupt a bitmap byte
  Ack got;
  TEST_ASSERT_FALSE(Protocol::DecodeAck(wire, got));
}

void test_ack_wrong_length_rejected(void) {
  uint8_t buf[4] = {};
  Ack got;
  TEST_ASSERT_FALSE(Protocol::DecodeAck(buf, got));
}

// ── Cross-side byte compatibility ────────────────────────────────────
//
// The Go test (header_test.go TestEnvelope_RoundTrip) uses the exact
// same header field values. This test pins the wire bytes so a future
// change to either codec is caught: the first 4 bytes of the wire
// are target_id (0xFFFF LE) + sender_id (0x0001 LE) = FF FF 01 00.

void test_envelope_wire_bytes_match_go(void) {
  Header h = makeHeader();
  uint8_t payload[32];
  std::memset(payload, 0xCC, sizeof(payload));
  uint8_t wire[kHeaderSize + 32];
  Protocol::Encode(h, payload, wire);
  // target_id LE = FF FF, sender_id LE = 01 00.
  TEST_ASSERT_EQUAL_UINT8(0xFF, wire[0]);
  TEST_ASSERT_EQUAL_UINT8(0xFF, wire[1]);
  TEST_ASSERT_EQUAL_UINT8(0x01, wire[2]);
  TEST_ASSERT_EQUAL_UINT8(0x00, wire[3]);
  // conversation_id = 0xAB × 16 at offset 4.
  TEST_ASSERT_EQUAL_UINT8(0xAB, wire[4]);
  TEST_ASSERT_EQUAL_UINT8(0xAB, wire[19]);
  // message_id = 0xDEADBEEF LE at offset 20: EF BE AD DE.
  TEST_ASSERT_EQUAL_UINT8(0xEF, wire[20]);
  TEST_ASSERT_EQUAL_UINT8(0xBE, wire[21]);
  TEST_ASSERT_EQUAL_UINT8(0xAD, wire[22]);
  TEST_ASSERT_EQUAL_UINT8(0xDE, wire[23]);
  // msg_type = kData = 2 at offset 28.
  TEST_ASSERT_EQUAL_UINT8(2, wire[28]);
  // payload starts at offset 34.
  TEST_ASSERT_EQUAL_UINT8(0xCC, wire[34]);
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_crc_canonical_vectors);
  RUN_TEST(test_envelope_round_trip);
  RUN_TEST(test_envelope_max_payload);
  RUN_TEST(test_envelope_oversized_payload_rejected);
  RUN_TEST(test_envelope_bad_crc_header_flip);
  RUN_TEST(test_envelope_bad_crc_field_flip);
  RUN_TEST(test_envelope_truncated);
  RUN_TEST(test_ack_round_trip);
  RUN_TEST(test_ack_crc_rejects_corruption);
  RUN_TEST(test_ack_wrong_length_rejected);
  RUN_TEST(test_envelope_wire_bytes_match_go);
  return UNITY_END();
}
