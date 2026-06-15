// radio_task.cpp — implementation of tether::m5::RadioTask.

#include "radio_task.h"

#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.radio";
constexpr size_t kChunkSize = 64;
constexpr int kMaxRetransmits = 5;
constexpr int kStartRepeats = 3;
} // namespace

RadioTask::RadioTask(LoraRadio &radio) : radio_(radio) {}

uint32_t RadioTask::Enqueue(std::vector<uint8_t> payload) {
  RadioMessage m{next_msg_id_++, std::move(payload)};
  outbox_.push(m);
  return m.msg_id;
}

void RadioTask::InjectRxForTest(RadioMessage m) { rx_queue_.push(m); }

void RadioTask::InjectAckForTest(uint32_t msg_id, uint32_t bitmap) {
  HandleAck(msg_id, bitmap);
}

void RadioTask::StartSending() {
  if (outbox_.empty())
    return;
  current_msg_id_ = outbox_.front().msg_id;
  current_payload_ = std::move(outbox_.front().payload);
  outbox_.pop();
  current_chunks_total_ =
      (current_payload_.size() + kChunkSize - 1) / kChunkSize;
  if (current_chunks_total_ == 0)
    current_chunks_total_ = 1;
  current_chunks_acked_ = 0;
  acked_bitmap_ = 0;
  retransmits_left_ = kMaxRetransmits;
  start_repeats_remaining_ = kStartRepeats;
  state_ = RadioState::kSendingStart;
}

bool RadioTask::SendOneDataChunk() {
  // Find the lowest unacked chunk.
  for (uint32_t i = 0; i < current_chunks_total_; ++i) {
    if (!(acked_bitmap_ & (1u << i))) {
      size_t offset = i * kChunkSize;
      size_t len = kChunkSize;
      if (offset + len > current_payload_.size()) {
        len = current_payload_.size() - offset;
      }
      std::vector<uint8_t> chunk(current_payload_.begin() + offset,
                                 current_payload_.begin() + offset + len);
      radio_.Transmit(chunk);
      pkts_sent_++;
      return true;
    }
  }
  return false; // all chunks acked
}

void RadioTask::HandleRxPacket(const RadioMessage & /*m*/) {
  // The full message-receive path (reassembly, decrypt, dispatch to
  // TTS / UI) is implemented in Phase 4. For Phase 3 we just clear
  // the rx queue so Step() doesn't loop forever.
}

void RadioTask::HandleAck(uint32_t msg_id, uint32_t bitmap) {
  if (msg_id != current_msg_id_)
    return;
  acks_received_++;
  acked_bitmap_ |= bitmap;
  current_chunks_acked_ = __builtin_popcount(acked_bitmap_);
  if (current_chunks_acked_ >= current_chunks_total_) {
    state_ = RadioState::kIdle;
    last_acked_ = true;
    last_failed_ = false;
  } else {
    state_ = RadioState::kSendingData;
  }
}

bool RadioTask::Step() {
  // Drain any injected RX packets.
  while (!rx_queue_.empty()) {
    auto m = rx_queue_.front();
    rx_queue_.pop();
    HandleRxPacket(m);
  }
  switch (state_) {
  case RadioState::kIdle:
    if (!outbox_.empty()) {
      StartSending();
    }
    break;
  case RadioState::kSendingStart:
    // Emit one START packet.
    {
      std::vector<uint8_t> start_pkt{0x01, 0x02, 0x03}; // placeholder
      radio_.Transmit(start_pkt);
      pkts_sent_++;
    }
    if (--start_repeats_remaining_ <= 0) {
      state_ = RadioState::kSendingData;
    }
    break;
  case RadioState::kSendingData:
    if (!SendOneDataChunk()) {
      // All chunks acked; we're done.
      if (current_chunks_acked_ >= current_chunks_total_) {
        state_ = RadioState::kIdle;
        last_acked_ = true;
      } else {
        // Retransmit budget check.
        if (retransmits_left_ > 0) {
          retransmits_left_--;
          retransmits_++;
          // Loop; SendOneDataChunk() will find an unacked chunk.
        } else {
          state_ = RadioState::kIdle;
          last_failed_ = true;
          last_acked_ = false;
        }
      }
    }
    break;
  case RadioState::kWaitingAck:
  case RadioState::kRxListening:
    // Phase 4 handles the full state machine; for Phase 3 these
    // are no-ops.
    break;
  }
  return state_ != RadioState::kIdle;
}

} // namespace tether::m5
