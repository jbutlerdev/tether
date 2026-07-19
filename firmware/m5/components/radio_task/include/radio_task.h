// radio_task.h — Tether M5 radio task (plan.md §4.8.4).
//
// The radio task owns the LoRa state machine: it picks pending files
// from the queue, fragments them into 34-byte-header packets
// (protocol.h, research.md §8.1), sends START / DATA / END, and
// handles ACKs with the self-describing 28-byte ACK format (§8.6).
// On real hardware it runs at priority 23 on core 1; on host tests
// we expose the state machine directly.

#pragma once

#include <cstdint>
#include <functional>
#include <queue>
#include <string>
#include <vector>

#include "lora_sx1262.h"
#include "protocol.h"

namespace tether::m5 {

// RadioMessage is a queued outbound transmission. The conversation_id
// identifies which conversation this message belongs to (research.md
// §8.1); the msg_id is assigned by Enqueue.
struct RadioMessage {
  uint32_t msg_id = 0;
  uint8_t conversation_id[kConvIDSize] = {};
  std::vector<uint8_t> payload;
};

// IncomingPacket is a decoded LoRa packet from the radio. The radio
// task decodes the 34-byte header and hands the payload + header to
// the registered handler.
struct IncomingPacket {
  Header header;
  std::vector<uint8_t> payload;
};

// IncomingHandler is called for every decoded non-ACK packet. The
// handler dispatches to conv_manager (UI_UPDATE) or the TTS playback
// path (TTS_DATA / TTS_END).
using IncomingHandler = std::function<void(const IncomingPacket &pkt)>;

enum class RadioState : uint8_t {
  kIdle = 0,
  kSendingStart = 1,
  kSendingData = 2,
  kWaitingAck = 3,
  kRxListening = 4,
};

class RadioTask {
public:
  // sender_id is this node's address (M5 = 0x0001);
  // target_id is the base station's address (0x0002).
  RadioTask(LoraRadio &radio, uint16_t sender_id = 0x0001,
            uint16_t target_id = 0x0002);

  // Queue a new outbound message. Returns the assigned msg_id.
  uint32_t Enqueue(const uint8_t conversation_id[kConvIDSize],
                   std::vector<uint8_t> payload);

  // Set the handler for decoded incoming packets (DATA, TTS_DATA,
  // TTS_END, UI_UPDATE). ACKs are handled internally.
  void SetIncomingHandler(IncomingHandler h) {
    incoming_handler_ = std::move(h);
  }

  // Inject an inbound raw LoRa packet (used by tests to simulate a
  // packet received over LoRa). The packet is a 34-byte-header +
  // payload buffer.
  void InjectRxForTest(std::vector<uint8_t> raw);

  // Inject an ACK bitmap (used by tests to simulate the bridge
  // acknowledging chunks). The bitmap covers the current message.
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
  void SendStartPacket();
  // SendOneDataChunk transmits the lowest unacked chunk and returns
  // its index, or -1 if every chunk is acked.
  int SendOneDataChunk();
  void SendEndPacket();
  void HandleRxPacket(std::span<const uint8_t> raw);
  void HandleAck(const Ack &ack);
  // Transmit a protocol-encoded packet on the radio.
  void TransmitPacket(const Header &hdr, std::span<const uint8_t> payload);

  LoraRadio &radio_;
  uint16_t sender_id_;
  uint16_t target_id_;
  IncomingHandler incoming_handler_;

  std::queue<RadioMessage> outbox_;
  RadioMessage current_msg_;
  uint32_t current_chunks_total_ = 0;
  uint32_t current_chunks_acked_ = 0;
  uint32_t acked_bitmap_ = 0;
  int retransmits_left_ = 0;
  int start_repeats_remaining_ = 0;
  int last_sent_chunk_ = -1; // index of the last DATA chunk sent
  RadioState state_ = RadioState::kIdle;

  // Inbound queue (test-injected or radio-received).
  std::queue<std::vector<uint8_t>> rx_queue_;

  // Counters.
  uint64_t pkts_sent_ = 0;
  uint64_t acks_received_ = 0;
  uint64_t retransmits_ = 0;
  uint32_t next_msg_id_ = 1;
  bool last_acked_ = false;
  bool last_failed_ = false;
};

} // namespace tether::m5
