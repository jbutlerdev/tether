// test_radio_task.cpp — unit tests for tether::m5::RadioTask (v0.2.0).
//
// Tests the 34-byte-header protocol path: Enqueue with conversation_id,
// START 3× → DATA chunks → ACK → END, retransmit on timeout, incoming
// packet decode + dispatch.
#include <cstring>
#include <memory>
#include <vector>

#include <unity.h>

#include "lora_sx1262.h"
#include "protocol.h"
#include "radio_task.h"

using tether::m5::AudioKind;
using tether::m5::Header;
using tether::m5::IncomingPacket;
using tether::m5::kConvIDSize;
using tether::m5::kHeaderSize;
using tether::m5::kMaxPayloadSize;
using tether::m5::LoraRadio;
using tether::m5::MockRadioBackend;
using tether::m5::MsgType;
using tether::m5::Protocol;
using tether::m5::RadioState;
using tether::m5::RadioTask;

namespace {
std::shared_ptr<MockRadioBackend> g_backend;
LoraRadio *g_radio = nullptr;
RadioTask *g_task = nullptr;

// A fixed conversation_id for tests.
constexpr uint8_t kTestConvID[kConvIDSize] = {
    0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
    0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10};

void Reset() {
  delete g_task;
  g_task = nullptr;
  delete g_radio;
  g_radio = nullptr;
  g_backend = std::make_shared<MockRadioBackend>();
  g_radio = new LoraRadio(g_backend);
  g_task = new RadioTask(*g_radio);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_task;
  g_task = nullptr;
  delete g_radio;
  g_radio = nullptr;
  g_backend.reset();
}

// Test 1: idle state with no messages.
void test_radio_picks_pending_from_queue() {
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kIdle),
                    static_cast<int>(g_task->State()));
  uint32_t id = g_task->Enqueue(kTestConvID, {0x01, 0x02, 0x03});
  TEST_ASSERT_EQUAL(1, id);
  g_task->Step();
  TEST_ASSERT_GREATER_THAN(0, g_task->PktsSent());
}

// Test 2: START packet sent 3 times before DATA.
void test_radio_sends_start_3x() {
  g_task->Enqueue(kTestConvID, {0xAA, 0xBB});
  for (int i = 0; i < 3; ++i)
    g_task->Step();
  // 3 START packets sent.
  TEST_ASSERT_EQUAL(3, g_task->PktsSent());
  // After 3 STARTs, the state should be kSendingData.
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kSendingData),
                    static_cast<int>(g_task->State()));
}

// Test 3: data + ACK loop → message acked.
void test_radio_sends_data_with_acks() {
  // A 100-byte message → 1 chunk (≤221 bytes).
  g_task->Enqueue(kTestConvID, std::vector<uint8_t>(100, 0xCC));
  for (int i = 0; i < 50; ++i) {
    g_task->Step();
    if (g_task->LastMessageAcked())
      break;
    // Inject an ACK for the current message with bitmap=0x1 (chunk 0).
    g_task->InjectAckForTest(g_task->NextMsgId() - 1, 0x1);
  }
  TEST_ASSERT_TRUE(g_task->LastMessageAcked());
}

// Test 4: max 5 retransmits → mark failed.
void test_radio_max_5_retransmits() {
  g_task->Enqueue(kTestConvID, {0x10, 0x20});
  for (int i = 0; i < 100; ++i)
    g_task->Step();
  TEST_ASSERT_TRUE(g_task->LastMessageFailed());
  TEST_ASSERT_GREATER_THAN(4, g_task->Retransmits());
}

// Test 5: ACKs received advance the counter.
void test_radio_acks_received() {
  uint32_t id = g_task->Enqueue(kTestConvID, {0xAB});
  g_task->InjectAckForTest(id, 0x1);
  TEST_ASSERT_GREATER_THAN(0, g_task->AcksReceived());
}

// Helper: build a protocol-encoded packet for injection.
std::vector<uint8_t> MakePacket(MsgType type, uint32_t msg_id,
                                std::span<const uint8_t> payload) {
  Header hdr{};
  hdr.target_id = 0x0001; // M5
  hdr.sender_id = 0x0002; // base
  std::memcpy(hdr.conversation_id, kTestConvID, kConvIDSize);
  hdr.message_id = msg_id;
  hdr.msg_type = type;
  hdr.audio_kind = AudioKind::kTts;
  uint8_t buf[kHeaderSize + kMaxPayloadSize];
  std::size_t n = Protocol::Encode(hdr, payload, buf);
  return {buf, buf + n};
}

// Test 6: receive a TTS_DATA packet — dispatched to incoming handler.
void test_radio_receives_tts() {
  bool got_it = false;
  g_task->SetIncomingHandler([&](const IncomingPacket &pkt) {
    if (pkt.header.msg_type == MsgType::kTtsData) {
      got_it = true;
    }
  });
  std::vector<uint8_t> tts_payload = {0xC0, 0xC1};
  auto raw = MakePacket(MsgType::kTtsData, 999, tts_payload);
  g_task->InjectRxForTest(raw);
  g_task->Step();
  TEST_ASSERT_TRUE(got_it);
}

// Test 7: receive a UI_UPDATE — dispatched to incoming handler.
void test_radio_receives_ui_update() {
  bool got_it = false;
  g_task->SetIncomingHandler([&](const IncomingPacket &pkt) {
    if (pkt.header.msg_type == MsgType::kUiUpdate) {
      got_it = true;
    }
  });
  std::vector<uint8_t> ui_payload = {0xD0, 0xD1};
  auto raw = MakePacket(MsgType::kUiUpdate, 1000, ui_payload);
  g_task->InjectRxForTest(raw);
  g_task->Step();
  TEST_ASSERT_TRUE(got_it);
}

// Test 8: ACK for a different conversation_id is rejected.
void test_radio_rejects_ack_wrong_conv() {
  uint32_t id = g_task->Enqueue(kTestConvID, {0xAB});
  // Build an ACK with a different conversation_id.
  uint8_t other_conv[kConvIDSize] = {};
  std::memset(other_conv, 0xFF, kConvIDSize);
  // Manually inject via a raw ACK packet.
  tether::m5::Ack ack{};
  ack.message_id = id;
  std::memcpy(ack.conversation_id, other_conv, kConvIDSize);
  ack.ack_bitmap_lo = 0x0001;
  uint8_t ack_buf[28];
  Protocol::EncodeAck(ack, ack_buf);
  auto raw = MakePacket(MsgType::kAck, id, ack_buf);
  g_task->InjectRxForTest(raw);
  g_task->Step();
  // The ACK should have been rejected (0 acks received).
  TEST_ASSERT_EQUAL(0, g_task->AcksReceived());
}

// Test 9: large message fragments into multiple chunks.
void test_radio_large_message_fragments() {
  // 500 bytes → ceil(500/221) = 3 chunks.
  std::vector<uint8_t> payload(500, 0x42);
  g_task->Enqueue(kTestConvID, payload);
  // Drive through START (3 steps) + DATA.
  for (int i = 0; i < 3; ++i)
    g_task->Step(); // 3 STARTs
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kSendingData),
                    static_cast<int>(g_task->State()));
  // Send 1 DATA chunk.
  g_task->Step();
  // Should have sent 3 START + 1 DATA = 4 packets.
  TEST_ASSERT_EQUAL(4, g_task->PktsSent());
  // Ack all 3 chunks.
  g_task->InjectAckForTest(g_task->NextMsgId() - 1, 0x7); // bits 0,1,2
  for (int i = 0; i < 5; ++i) {
    g_task->Step();
    if (g_task->LastMessageAcked())
      break;
  }
  TEST_ASSERT_TRUE(g_task->LastMessageAcked());
}

// Test 10: idle low-power — no pending, no sends.
void test_radio_idle_low_power() {
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kIdle),
                    static_cast<int>(g_task->State()));
  g_task->Step();
  TEST_ASSERT_EQUAL(0, g_task->PktsSent());
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_radio_picks_pending_from_queue);
  RUN_TEST(test_radio_sends_start_3x);
  RUN_TEST(test_radio_sends_data_with_acks);
  RUN_TEST(test_radio_max_5_retransmits);
  RUN_TEST(test_radio_acks_received);
  RUN_TEST(test_radio_receives_tts);
  RUN_TEST(test_radio_receives_ui_update);
  RUN_TEST(test_radio_rejects_ack_wrong_conv);
  RUN_TEST(test_radio_large_message_fragments);
  RUN_TEST(test_radio_idle_low_power);
  UNITY_END();
}
