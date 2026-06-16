// frame_test.cpp — unit tests for tether::bridge::frame (plan.md §3.1).
//
// These tests are written first (TDD red phase). The implementation in
// frame.cpp is introduced only after they fail for the right reasons.

#include <unity.h>

#include <array>
#include <cstdint>
#include <optional>
#include <span>
#include <stdexcept>
#include <vector>

#include "frame.h"

using tether::bridge::DecodeFrame;
using tether::bridge::EncodeFrame;
using tether::bridge::Frame;
using tether::bridge::FrameDecoder;
using tether::bridge::FrameType;

namespace {

// Build a Frame with a known payload. The CRC field will be filled in by
// EncodeFrame — never set it manually here.
Frame MakeFrame(FrameType type, std::vector<uint8_t> payload) {
  Frame f{};
  f.type = type;
  f.payload = std::move(payload);
  return f;
}

// Decode helper: returns nullopt if DecodeFrame returns std::nullopt, else
// the Frame. Lets tests assert with TEST_ASSERT_NOT_NULL on an optional.
std::optional<Frame> Decode(const std::vector<uint8_t> &bytes) {
  return DecodeFrame(std::span<const uint8_t>(bytes.data(), bytes.size()));
}

} // namespace

void setUp() {}
void tearDown() {}

// ── Test 1: round-trip 10 frames of varying size ─────────────────────────
void test_encode_decode_round_trip() {
  struct Case {
    FrameType type;
    std::vector<uint8_t> payload;
  };
  const std::vector<Case> cases = {
      {FrameType::kTxDone, {}},
      {FrameType::kAck, {0x01}},
      {FrameType::kRxPacket, {0xAA, 0xBB, 0xCC}},
      {FrameType::kCadResult, {0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}},
      {FrameType::kSetConfig, std::vector<uint8_t>(16, 0xAB)},
      {FrameType::kLog, std::vector<uint8_t>(32, 0xCD)},
      {FrameType::kError, {0xFF}},
      {FrameType::kRxPacket,
       std::vector<uint8_t>(227, 0x55)}, // max LoRa payload
      {FrameType::kRxPacket, std::vector<uint8_t>(100, 0x00)},
      {FrameType::kRxPacket, std::vector<uint8_t>(255, 0xDE)},
  };

  for (const auto &c : cases) {
    Frame original = MakeFrame(c.type, c.payload);
    std::vector<uint8_t> bytes = EncodeFrame(original);
    auto decoded = Decode(bytes);
    TEST_ASSERT_TRUE_MESSAGE(decoded.has_value(), "decode round-trip");
    TEST_ASSERT_EQUAL_UINT8_MESSAGE(static_cast<uint8_t>(c.type),
                                    static_cast<uint8_t>(decoded->type),
                                    "type preserved");
    TEST_ASSERT_EQUAL_UINT32_MESSAGE(c.payload.size(), decoded->payload.size(),
                                     "payload length preserved");
    for (size_t i = 0; i < c.payload.size(); ++i) {
      TEST_ASSERT_EQUAL_UINT8_MESSAGE(c.payload[i], decoded->payload[i],
                                      "payload byte preserved");
    }
  }
}

// ── Test 2: bad magic byte rejected ──────────────────────────────────────
void test_decode_rejects_bad_magic() {
  std::vector<uint8_t> bytes{0xAA, 0x56, 0x02, 0x00, 0x00, 0x00, 0x00};
  auto decoded = Decode(bytes);
  TEST_ASSERT_FALSE(decoded.has_value());
}

// ── Test 3: bad CRC rejected ─────────────────────────────────────────────
void test_decode_rejects_bad_crc() {
  Frame original = MakeFrame(FrameType::kRxPacket, {0x10, 0x20, 0x30});
  std::vector<uint8_t> bytes = EncodeFrame(original);
  // Flip a single bit in the payload (byte index 5 = first payload byte).
  // 0x10 ^ 0x08 = 0x18. The CRC footer no longer matches.
  TEST_ASSERT_TRUE(bytes.size() >= 6);
  bytes[5] ^= 0x08;
  auto decoded = Decode(bytes);
  TEST_ASSERT_FALSE(decoded.has_value());
}

// ── Test 4: truncated frame rejected ─────────────────────────────────────
void test_decode_rejects_truncated() {
  Frame original = MakeFrame(FrameType::kRxPacket, {0x10, 0x20, 0x30});
  std::vector<uint8_t> bytes = EncodeFrame(original);
  // Drop the last two bytes (crc_lo, crc_hi).
  TEST_ASSERT_TRUE(bytes.size() > 5);
  bytes.resize(bytes.size() - 2);
  auto decoded = Decode(bytes);
  TEST_ASSERT_FALSE(decoded.has_value());
}

// ── Test 5: streaming partial feed then remainder ────────────────────────
void test_decode_streaming_partial() {
  Frame original = MakeFrame(FrameType::kRxPacket, {0x10, 0x20, 0x30, 0x40});
  std::vector<uint8_t> bytes = EncodeFrame(original);
  // 2 magic + 1 type + 2 len + 4 payload + 2 crc = 11 bytes total.
  TEST_ASSERT_EQUAL_size_t(11, bytes.size());

  FrameDecoder dec;
  dec.Feed(std::span<const uint8_t>(bytes.data(), 3));
  auto first = dec.Next();
  TEST_ASSERT_FALSE(first.has_value());

  dec.Feed(std::span<const uint8_t>(bytes.data() + 3, bytes.size() - 3));
  auto second = dec.Next();
  TEST_ASSERT_TRUE(second.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kRxPacket),
                          static_cast<uint8_t>(second->type));
  TEST_ASSERT_EQUAL_UINT32(4, second->payload.size());
  TEST_ASSERT_FALSE(dec.Next().has_value());
}

// ── Test 6: two frames fed across three chunks ───────────────────────────
void test_decode_streaming_two_frames() {
  Frame a = MakeFrame(FrameType::kAck, {0xAA});
  Frame b = MakeFrame(FrameType::kLog, {0x10, 0x20, 0x30, 0x40});
  std::vector<uint8_t> a_bytes = EncodeFrame(a);
  std::vector<uint8_t> b_bytes = EncodeFrame(b);

  std::vector<uint8_t> concat;
  concat.insert(concat.end(), a_bytes.begin(), a_bytes.end());
  concat.insert(concat.end(), b_bytes.begin(), b_bytes.end());

  // Split into 3 chunks: [0, 3), [3, 5), [5, end).
  TEST_ASSERT_TRUE(concat.size() > 5);
  size_t split1 = 3;
  size_t split2 = 5;
  FrameDecoder dec;
  dec.Feed(std::span<const uint8_t>(concat.data(), split1));
  dec.Feed(std::span<const uint8_t>(concat.data() + split1, split2 - split1));
  dec.Feed(
      std::span<const uint8_t>(concat.data() + split2, concat.size() - split2));

  auto f1 = dec.Next();
  TEST_ASSERT_TRUE(f1.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kAck),
                          static_cast<uint8_t>(f1->type));
  TEST_ASSERT_EQUAL_UINT32(1, f1->payload.size());

  auto f2 = dec.Next();
  TEST_ASSERT_TRUE(f2.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kLog),
                          static_cast<uint8_t>(f2->type));
  TEST_ASSERT_EQUAL_UINT32(4, f2->payload.size());

  TEST_ASSERT_FALSE(dec.Next().has_value());
}

// ── Test 7: oversize payload throws ──────────────────────────────────────
void test_encode_rejects_oversized() {
  Frame original;
  original.type = FrameType::kRxPacket;
  original.payload = std::vector<uint8_t>(70'000, 0x42);
  bool threw = false;
  try {
    EncodeFrame(original);
  } catch (const std::invalid_argument &) {
    threw = true;
  }
  TEST_ASSERT_TRUE_MESSAGE(threw, "oversize payload must throw");
}

// Test 8: DecodeFrame on a buffer too small for header+footer.
void test_decode_rejects_too_short() {
  // 6 bytes is below the 7-byte minimum (2 magic + 1 type + 2 len + 2 crc).
  std::vector<uint8_t> bytes{0xAA, 0x55, 0x02, 0x01, 0x00, 0x00};
  auto decoded = Decode(bytes);
  TEST_ASSERT_FALSE(decoded.has_value());
}

// Test 9: DecodeFrame on a frame that claims more payload than bytes.
void test_decode_rejects_length_mismatch() {
  // Valid header magic+type+len=0xFFFF, but buffer is far smaller.
  std::vector<uint8_t> bytes{0xAA, 0x55, 0x02, 0xFF, 0xFF, 0x00, 0x00};
  auto decoded = Decode(bytes);
  TEST_ASSERT_FALSE(decoded.has_value());
}

// Test 10: FrameDecoder drops a frame with bad CRC mid-stream.
void test_decoder_drops_bad_crc_and_resumes() {
  Frame good = MakeFrame(FrameType::kRxPacket, {0xAA, 0xBB});
  std::vector<uint8_t> good_bytes = EncodeFrame(good);

  // Hand-craft a bad-CRC frame (length says 2 payload bytes, but the CRC
  // we put on the end is wrong).
  std::vector<uint8_t> bad_bytes = {
      0xAA,
      0x55,                                       // magic
      static_cast<uint8_t>(FrameType::kRxPacket), // type
      0x02,
      0x00, // length=2
      0x11,
      0x22, // payload
      0x00,
      0x00, // bogus crc
  };
  TEST_ASSERT_EQUAL_size_t(9, bad_bytes.size());

  FrameDecoder dec;
  dec.Feed(std::span<const uint8_t>(bad_bytes.data(), bad_bytes.size()));
  dec.Feed(std::span<const uint8_t>(good_bytes.data(), good_bytes.size()));
  auto f = dec.Next();
  TEST_ASSERT_TRUE(f.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kRxPacket),
                          static_cast<uint8_t>(f->type));
  TEST_ASSERT_EQUAL_UINT32(2, f->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0xAA, f->payload[0]);
  TEST_ASSERT_EQUAL_UINT8(0xBB, f->payload[1]);
  TEST_ASSERT_FALSE(dec.Next().has_value());
}

// Test 11: FrameDecoder resyncs after a stray byte.
void test_decoder_resyncs_after_stray() {
  Frame good = MakeFrame(FrameType::kRxPacket, {0x42});
  std::vector<uint8_t> good_bytes = EncodeFrame(good);
  TEST_ASSERT_TRUE(good_bytes.size() > 2);

  FrameDecoder dec;
  std::vector<uint8_t> feed;
  // A leading 0xFF (not 0xAA) is dropped in the kWaitMagic0 state.
  feed.push_back(0xFF);
  feed.insert(feed.end(), good_bytes.begin(), good_bytes.end());
  dec.Feed(std::span<const uint8_t>(feed.data(), feed.size()));
  auto f = dec.Next();
  TEST_ASSERT_TRUE(f.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kRxPacket),
                          static_cast<uint8_t>(f->type));
  TEST_ASSERT_EQUAL_UINT32(1, f->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0x42, f->payload[0]);
}

// Test 12: FrameDecoder handles zero-length payload.
void test_decoder_zero_length_payload() {
  Frame original = MakeFrame(FrameType::kTxDone, {});
  std::vector<uint8_t> bytes = EncodeFrame(original);
  // 2 magic + 1 type + 2 len(=0) + 0 payload + 2 crc = 7 bytes.
  TEST_ASSERT_EQUAL_size_t(7, bytes.size());
  FrameDecoder dec;
  dec.Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  auto f = dec.Next();
  TEST_ASSERT_TRUE(f.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kTxDone),
                          static_cast<uint8_t>(f->type));
  TEST_ASSERT_EQUAL_UINT32(0, f->payload.size());
}

// Test 13: FrameDecoder recovers from a non-magic byte after 0xAA.
void test_decoder_handles_aa_then_garbage() {
  // The state machine saw 0xAA (magic0) then 0x77 (not magic1, not 0xAA):
  // it should fall back to looking for magic0 again.
  Frame good = MakeFrame(FrameType::kRxPacket, {0x33});
  std::vector<uint8_t> good_bytes = EncodeFrame(good);

  FrameDecoder dec;
  std::vector<uint8_t> feed;
  feed.push_back(0xAA); // enter kWaitMagic1
  feed.push_back(0x77); // bad, fall back to kWaitMagic0
  feed.insert(feed.end(), good_bytes.begin(), good_bytes.end());
  dec.Feed(std::span<const uint8_t>(feed.data(), feed.size()));
  auto f = dec.Next();
  TEST_ASSERT_TRUE(f.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kRxPacket),
                          static_cast<uint8_t>(f->type));
  TEST_ASSERT_EQUAL_UINT32(1, f->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0x33, f->payload[0]);
}

// Test 14: FrameDecoder treats 0xAA 0xAA 0x55 ... as a valid frame start.
void test_decoder_treats_aa_aa_55_as_magic_pair() {
  Frame good = MakeFrame(FrameType::kRxPacket, {0x99});
  std::vector<uint8_t> good_bytes = EncodeFrame(good);

  FrameDecoder dec;
  std::vector<uint8_t> feed;
  feed.push_back(0xAA); // enter kWaitMagic1
  feed.push_back(0xAA); // stay in kWaitMagic1 (treated as a leading magic0)
  feed.insert(feed.end(), good_bytes.begin(), good_bytes.end());
  dec.Feed(std::span<const uint8_t>(feed.data(), feed.size()));
  auto f = dec.Next();
  TEST_ASSERT_TRUE(f.has_value());
  TEST_ASSERT_EQUAL_UINT32(1, f->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0x99, f->payload[0]);
}

// Unity entry point.
int main(int /*argc*/, char ** /*argv*/) {
  UNITY_BEGIN();
  RUN_TEST(test_encode_decode_round_trip);
  RUN_TEST(test_decode_rejects_bad_magic);
  RUN_TEST(test_decode_rejects_bad_crc);
  RUN_TEST(test_decode_rejects_truncated);
  RUN_TEST(test_decode_streaming_partial);
  RUN_TEST(test_decode_streaming_two_frames);
  RUN_TEST(test_encode_rejects_oversized);
  RUN_TEST(test_decode_rejects_too_short);
  RUN_TEST(test_decode_rejects_length_mismatch);
  RUN_TEST(test_decoder_drops_bad_crc_and_resumes);
  RUN_TEST(test_decoder_resyncs_after_stray);
  RUN_TEST(test_decoder_zero_length_payload);
  RUN_TEST(test_decoder_handles_aa_then_garbage);
  RUN_TEST(test_decoder_treats_aa_aa_55_as_magic_pair);
  return UNITY_END();
}
