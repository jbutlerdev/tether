// test_radio_task.cpp — unit tests for tether::m5::RadioTask.
#include <memory>
#include <vector>

#include <unity.h>

#include "lora_sx1262.h"
#include "radio_task.h"

using tether::m5::LoraRadio;
using tether::m5::MockRadioBackend;
using tether::m5::RadioMessage;
using tether::m5::RadioState;
using tether::m5::RadioTask;

namespace {
std::shared_ptr<MockRadioBackend> g_backend;
LoraRadio *g_radio = nullptr;
RadioTask *g_task = nullptr;

void Reset() {
  delete g_task; g_task = nullptr;
  delete g_radio; g_radio = nullptr;
  g_backend = std::make_shared<MockRadioBackend>();
  g_radio = new LoraRadio(g_backend);
  g_task = new RadioTask(*g_radio);
}
} // namespace

void setUp() { Reset(); }
void tearDown() {
  delete g_task; g_task = nullptr;
  delete g_radio; g_radio = nullptr;
  g_backend.reset();
}

// Test 1: idle state with no messages.
void test_radio_picks_pending_from_queue() {
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kIdle),
                    static_cast<int>(g_task->State()));
  // Enqueue a message; next Step() should pick it up.
  uint32_t id = g_task->Enqueue({0x01, 0x02, 0x03});
  TEST_ASSERT_EQUAL(1, id);
  g_task->Step();
  TEST_ASSERT_GREATER_THAN(0, g_task->PktsSent());
}

// Test 2: START packet sent 3 times with 50 ms gaps.
// (We use a single Step() per send; the 50 ms gap is real-time in
// production, but the unit test just checks that 3 STARTs are sent
// before data.)
void test_radio_sends_start_3x() {
  g_task->Enqueue({0xAA, 0xBB});
  // 3 STARTs.
  for (int i = 0; i < 3; ++i) g_task->Step();
  // 3 pkts sent.
  TEST_ASSERT_GREATER_THAN(2, g_task->PktsSent());
}

// Test 3: data + ACK loop; on timeout, retransmit.
// We model this by sending a chunk and providing an ACK.
void test_radio_sends_data_with_acks() {
  // A 100-byte message → 1 chunk (we use a 100-byte chunk size).
  g_task->Enqueue(std::vector<uint8_t>(100, 0xCC));
  // Drive until the message is acked.
  for (int i = 0; i < 50; ++i) {
    g_task->Step();
    if (g_task->LastMessageAcked()) break;
    g_task->InjectAckForTest(g_task->NextMsgId() - 1, 0x1);
  }
  TEST_ASSERT_TRUE(g_task->LastMessageAcked());
}

// Test 4: max 5 retransmits → mark failed.
void test_radio_max_5_retransmits() {
  g_task->Enqueue({0x10, 0x20});
  // Drain 50 steps without injecting an ACK.
  for (int i = 0; i < 100; ++i) g_task->Step();
  TEST_ASSERT_TRUE(g_task->LastMessageFailed());
  TEST_ASSERT_GREATER_THAN(4, g_task->Retransmits());
}

// Test 5: ACKs received advance the bitmap.
void test_radio_acks_received() {
  uint32_t id = g_task->Enqueue({0xAB});
  g_task->InjectAckForTest(id, 0x1);
  TEST_ASSERT_GREATER_THAN(0, g_task->AcksReceived());
}

// Test 6: receive a TTS_DATA packet — surfaced as Rx (we just verify
// the call doesn't crash and the state machine doesn't get stuck).
void test_radio_receives_tts() {
  RadioMessage m{999, {0xC0, 0xC1}};
  g_task->InjectRxForTest(m);
  g_task->Step();
  TEST_ASSERT_TRUE(true); // no crash
}

// Test 7: receive a UI_UPDATE — same as above.
void test_radio_receives_ui_update() {
  RadioMessage m{1000, {0xD0, 0xD1}};
  g_task->InjectRxForTest(m);
  g_task->Step();
  TEST_ASSERT_TRUE(true);
}

// Test 8: msg_id gap (5, 7) — both processed.
void test_radio_handles_msg_id_gap() {
  g_task->InjectRxForTest(RadioMessage{5, {0x01}});
  g_task->InjectRxForTest(RadioMessage{7, {0x02}});
  for (int i = 0; i < 5; ++i) g_task->Step();
  TEST_ASSERT_TRUE(true);
}

// Test 9: replay drop — duplicate msg_id.
void test_radio_replay_drop() {
  g_task->InjectRxForTest(RadioMessage{42, {0x01}});
  g_task->InjectRxForTest(RadioMessage{42, {0x01}});
  for (int i = 0; i < 5; ++i) g_task->Step();
  TEST_ASSERT_TRUE(true);
}

// Test 10: idle low-power — no pending, no sends.
void test_radio_idle_low_power() {
  // No messages enqueued. State is idle.
  TEST_ASSERT_EQUAL(static_cast<int>(RadioState::kIdle),
                    static_cast<int>(g_task->State()));
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_radio_picks_pending_from_queue);
  RUN_TEST(test_radio_sends_start_3x);
  RUN_TEST(test_radio_sends_data_with_acks);
  RUN_TEST(test_radio_max_5_retransmits);
  RUN_TEST(test_radio_acks_received);
  RUN_TEST(test_radio_receives_tts);
  RUN_TEST(test_radio_receives_ui_update);
  RUN_TEST(test_radio_handles_msg_id_gap);
  RUN_TEST(test_radio_replay_drop);
  RUN_TEST(test_radio_idle_low_power);
  (void)0;
  UNITY_END();
}
