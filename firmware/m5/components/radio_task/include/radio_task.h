// radio_task.h — Tether M5 radio task (plan.md §4.8.4).
//
// The radio task owns the LoRa state machine: it picks pending files
// from the queue, fragments them, sends START / DATA / ACK, and
// handles retransmits. On real hardware it runs at priority 23 on
// core 1; on host tests we expose the state machine directly.

#pragma once

#include <cstdint>
#include <functional>
#include <queue>
#include <string>
#include <vector>

#include "lora_sx1262.h"

namespace tether::m5 {

struct RadioMessage {
  uint32_t msg_id = 0;
  std::vector<uint8_t> payload;
};

enum class RadioState : uint8_t {
  kIdle = 0,
  kSendingStart = 1,
  kSendingData = 2,
  kWaitingAck = 3,
  kRxListening = 4,
};

class RadioTask {
public:
  RadioTask(LoraRadio &radio);

  // Queue a new outbound message. Returns the assigned msg_id.
  uint32_t Enqueue(std::vector<uint8_t> payload);

  // Inject an inbound message (used by tests to simulate a packet
  // received over LoRa).
  void InjectRxForTest(RadioMessage m);

  // Inject an ACK bitmap (used by tests to simulate the bridge
  // acknowledging chunks).
  void InjectAckForTest(uint32_t msg_id, uint32_t bitmap);

  // Pump the state machine one step. Returns true if the task is
  // still busy (caller should call again).
  bool Step();

  // Total packets transmitted (START + DATA + retransmits).
  uint64_t PktsSent() const { return pkts_sent_; }
  // Total ACKs received.
  uint64_t AcksReceived() const { return acks_received_; }
  // Total retransmits.
  uint64_t Retransmits() const { return retransmits_; }
  // True if the most recently completed message is fully acked.
  bool LastMessageAcked() const { return last_acked_; }
  // True if the most recently completed message hit the retry budget.
  bool LastMessageFailed() const { return last_failed_; }

  RadioState State() const { return state_; }
  uint32_t NextMsgId() const { return next_msg_id_; }

private:
  void StartSending();
  bool SendOneDataChunk();
  void HandleRxPacket(const RadioMessage &m);
  void HandleAck(uint32_t msg_id, uint32_t bitmap);

  LoraRadio &radio_;
  std::queue<RadioMessage> outbox_;
  std::vector<uint8_t> current_payload_;
  uint32_t current_msg_id_ = 0;
  uint32_t current_chunks_total_ = 0;
  uint32_t current_chunks_acked_ = 0;
  uint32_t acked_bitmap_ = 0;
  int retransmits_left_ = 0;
  int start_repeats_remaining_ = 0;
  RadioState state_ = RadioState::kIdle;

  // Inbound queue (test-injected).
  std::queue<RadioMessage> rx_queue_;

  // Counters.
  uint64_t pkts_sent_ = 0;
  uint64_t acks_received_ = 0;
  uint64_t retransmits_ = 0;
  uint32_t next_msg_id_ = 1;
  bool last_acked_ = false;
  bool last_failed_ = false;
};

} // namespace tether::m5
