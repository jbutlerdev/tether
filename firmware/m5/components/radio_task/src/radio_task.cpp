// radio_task.cpp — implementation of tether::m5::RadioTask.
//
// Rewritten for v0.2.0 to use the 34-byte fixed-header wire format
// (protocol.h, research.md §8.1) instead of the placeholder 100-byte
// chunks. Outgoing packets are encoded with Protocol::Encode; incoming
// packets are decoded with Protocol::Decode; ACKs use the 28-byte
// self-describing format (§8.6).
//
// State machine (research.md §8.3):
//   IDLE → kSendingStart (3× START) → kSendingData (DATA chunks w/ ACK)
//        → kIdle (all acked) or kIdle (failed: retry budget exceeded)
//
// The chunk size is kMaxPayloadSize (221 bytes = 255 FIFO − 34 header).

#include "radio_task.h"

#include <algorithm>
#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#endif

#include "protocol.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.radio";
constexpr int kMaxRetransmits = 5;
constexpr int kStartRepeats = 3;
constexpr int kEndRepeats = 2;
} // namespace

RadioTask::RadioTask(LoraRadio &radio, uint16_t sender_id, uint16_t target_id)
    : radio_(radio), sender_id_(sender_id), target_id_(target_id) {}

uint32_t RadioTask::Enqueue(const uint8_t conversation_id[kConvIDSize],
                            std::vector<uint8_t> payload) {
  RadioMessage m{next_msg_id_++, {}, std::move(payload)};
  std::memcpy(m.conversation_id, conversation_id, kConvIDSize);
  outbox_.push(std::move(m));
  return m.msg_id;
}

void RadioTask::InjectRxForTest(std::vector<uint8_t> raw) {
  rx_queue_.push(std::move(raw));
}

void RadioTask::InjectAckForTest(uint32_t msg_id, uint32_t bitmap) {
  // Build a 28-byte ACK payload for the test. Copy the
  // conversation_id from the current message or the outbox front.
  Ack ack{};
  ack.message_id = msg_id;
  ack.next_expected_seq = 0;
  ack.ack_bitmap_lo = static_cast<uint16_t>(bitmap & 0xFFFF);
  ack.ack_bitmap_hi = static_cast<uint16_t>((bitmap >> 16) & 0xFFFF);
  if (state_ == RadioState::kSendingData ||
      state_ == RadioState::kSendingStart) {
    std::memcpy(ack.conversation_id, current_msg_.conversation_id, kConvIDSize);
  } else if (!outbox_.empty()) {
    std::memcpy(ack.conversation_id, outbox_.front().conversation_id,
                kConvIDSize);
  }
  HandleAck(ack);
}

void RadioTask::StartSending() {
  if (outbox_.empty())
    return;
  current_msg_ = std::move(outbox_.front());
  outbox_.pop();
  current_chunks_total_ =
      (current_msg_.payload.size() + kMaxPayloadSize - 1) / kMaxPayloadSize;
  if (current_chunks_total_ == 0)
    current_chunks_total_ = 1; // empty payload still sends 1 chunk
  current_chunks_acked_ = 0;
  acked_bitmap_ = 0;
  retransmits_left_ = kMaxRetransmits;
  start_repeats_remaining_ = kStartRepeats;
  last_sent_chunk_ = -1;
  state_ = RadioState::kSendingStart;
}

void RadioTask::TransmitPacket(const Header &hdr,
                               std::span<const uint8_t> payload) {
  uint8_t buf[kHeaderSize + kMaxPayloadSize];
  std::size_t n = Protocol::Encode(hdr, payload, buf);
  if (n == 0) {
#ifdef TETHER_M5_HOST_TEST
#else
    ESP_LOGE(kTag, "Encode failed");
#endif
    return;
  }
  radio_.Transmit(std::span<const uint8_t>(buf, n));
  pkts_sent_++;
}

void RadioTask::SendStartPacket() {
  Header hdr{};
  hdr.target_id = target_id_;
  hdr.sender_id = sender_id_;
  std::memcpy(hdr.conversation_id, current_msg_.conversation_id, kConvIDSize);
  hdr.message_id = current_msg_.msg_id;
  hdr.seq_num = 0;
  hdr.total_seqs = static_cast<uint16_t>(current_chunks_total_);
  hdr.msg_type = MsgType::kStart;
  hdr.audio_kind = AudioKind::kMic;
  // START carries the total payload size as a hint (first 4 bytes of
  // the payload, LE). This lets the receiver pre-allocate.
  uint8_t size_hint[4];
  uint32_t total = static_cast<uint32_t>(current_msg_.payload.size());
  size_hint[0] = total & 0xFF;
  size_hint[1] = (total >> 8) & 0xFF;
  size_hint[2] = (total >> 16) & 0xFF;
  size_hint[3] = (total >> 24) & 0xFF;
  TransmitPacket(hdr, size_hint);
  if (--start_repeats_remaining_ <= 0) {
    state_ = RadioState::kSendingData;
    last_sent_chunk_ = -1;
  }
}

void RadioTask::SendEndPacket() {
  Header hdr{};
  hdr.target_id = target_id_;
  hdr.sender_id = sender_id_;
  std::memcpy(hdr.conversation_id, current_msg_.conversation_id, kConvIDSize);
  hdr.message_id = current_msg_.msg_id;
  hdr.seq_num = static_cast<uint16_t>(current_chunks_total_);
  hdr.total_seqs = static_cast<uint16_t>(current_chunks_total_);
  hdr.msg_type = MsgType::kEnd;
  hdr.audio_kind = AudioKind::kMic;
  TransmitPacket(hdr, {});
}

int RadioTask::SendOneDataChunk() {
  // Find the lowest unacked chunk.
  for (uint32_t i = 0; i < current_chunks_total_; ++i) {
    if (!(acked_bitmap_ & (1u << i))) {
      size_t offset = i * kMaxPayloadSize;
      size_t len = std::min(static_cast<size_t>(kMaxPayloadSize),
                            current_msg_.payload.size() - offset);
      Header hdr{};
      hdr.target_id = target_id_;
      hdr.sender_id = sender_id_;
      std::memcpy(hdr.conversation_id, current_msg_.conversation_id,
                  kConvIDSize);
      hdr.message_id = current_msg_.msg_id;
      hdr.seq_num = static_cast<uint16_t>(i);
      hdr.total_seqs = static_cast<uint16_t>(current_chunks_total_);
      hdr.msg_type = MsgType::kData;
      hdr.audio_kind = AudioKind::kMic;
      TransmitPacket(hdr, std::span<const uint8_t>(
                              current_msg_.payload.data() + offset, len));
      return static_cast<int>(i);
    }
  }
  return -1; // all chunks acked
}

void RadioTask::HandleAck(const Ack &ack) {
  // Accept an ACK for the currently-sending message, or — when idle —
  // for the message at the front of the outbox (tests inject ACKs
  // before the first Step()). research.md §8.5/§8.6: ACKs are scoped
  // to a specific conversation_id + msg_id.
  const bool is_current =
      state_ == RadioState::kSendingData || state_ == RadioState::kSendingStart;
  uint32_t target = current_msg_.msg_id;
  if (!is_current && !outbox_.empty()) {
    target = outbox_.front().msg_id;
  }
  if (ack.message_id != target)
    return;
  // Verify the conversation_id matches.
  const uint8_t *expected_conv = is_current ? current_msg_.conversation_id
                                            : outbox_.front().conversation_id;
  if (std::memcmp(ack.conversation_id, expected_conv, kConvIDSize) != 0)
    return;

  acks_received_++;
  if (!is_current)
    return; // nothing to advance yet

  // The ACK bitmap is a 32-bit window starting at next_expected_seq.
  // For v1 (messages ≤ 32 chunks) the window starts at 0.
  uint32_t bitmap = static_cast<uint32_t>(ack.ack_bitmap_lo) |
                    (static_cast<uint32_t>(ack.ack_bitmap_hi) << 16);
  acked_bitmap_ |= bitmap;
  current_chunks_acked_ = __builtin_popcount(acked_bitmap_);
  if (current_chunks_acked_ >= current_chunks_total_) {
    // All chunks acked — send END, then go idle.
    for (int i = 0; i < kEndRepeats; ++i) {
      SendEndPacket();
    }
    state_ = RadioState::kIdle;
    last_acked_ = true;
    last_failed_ = false;
  } else {
    state_ = RadioState::kSendingData;
  }
}

void RadioTask::HandleRxPacket(std::span<const uint8_t> raw) {
  Header hdr;
  auto payload = Protocol::Decode(raw, hdr);
  if (payload.empty()) {
    // CRC mismatch or truncated — drop.
    return;
  }
  if (hdr.msg_type == MsgType::kAck) {
    // It's an ACK — decode the 28-byte payload.
    Ack ack;
    if (Protocol::DecodeAck(payload, ack)) {
      HandleAck(ack);
    }
    return;
  }
  // Non-ACK packet — dispatch to the incoming handler (conv_manager
  // for UI_UPDATE, TTS playback for TTS_DATA / TTS_END).
  if (incoming_handler_) {
    IncomingPacket pkt;
    pkt.header = hdr;
    pkt.payload.assign(payload.begin(), payload.end());
    incoming_handler_(pkt);
  }
}

bool RadioTask::Step() {
  // Drain any injected RX packets.
  while (!rx_queue_.empty()) {
    auto raw = std::move(rx_queue_.front());
    rx_queue_.pop();
    HandleRxPacket(raw);
  }

  switch (state_) {
  case RadioState::kIdle:
    if (outbox_.empty())
      break;
    StartSending();
    // Send the first START immediately so a single Step() makes
    // progress (tests expect Enqueue + 1 Step -> PktsSent > 0).
    SendStartPacket();
    break;
  case RadioState::kSendingStart:
    SendStartPacket();
    break;
  case RadioState::kSendingData: {
    int chunk = SendOneDataChunk();
    if (chunk < 0) {
      // All chunks acked; message complete.
      for (int i = 0; i < kEndRepeats; ++i) {
        SendEndPacket();
      }
      state_ = RadioState::kIdle;
      last_acked_ = true;
      last_failed_ = false;
      break;
    }
    if (chunk == last_sent_chunk_) {
      // Same chunk as last step -> no ACK arrived -> retransmit.
      retransmits_++;
      retransmits_left_--;
      if (retransmits_left_ < 0) {
        state_ = RadioState::kIdle;
        last_failed_ = true;
        last_acked_ = false;
        break;
      }
    }
    last_sent_chunk_ = chunk;
    break;
  }
  case RadioState::kWaitingAck:
  case RadioState::kRxListening:
    // These states are handled by the radio ISR + background RX;
    // Step() is the TX pump. No-op here.
    break;
  }
  return state_ != RadioState::kIdle;
}

} // namespace tether::m5
