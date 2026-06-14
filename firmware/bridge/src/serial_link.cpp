// serial_link.cpp — see serial_link.h. See plan.md §3.3.

#include "serial_link.h"

#include <cstring>
#include <vector>

namespace tether::bridge {

namespace {

constexpr size_t kReadChunkSize = 64;

} // namespace

void SerialLink::QueueCadResult(bool channel_busy) {
  cad_results_.push_back(channel_busy);
}

void SerialLink::QueueTxDone() { tx_done_queue_.push_back(true); }

void SerialLink::Step() {
  // 1. Drain the serial port into the FrameDecoder.
  uint8_t buf[kReadChunkSize];
  while (serial_->Available() > 0) {
    const size_t n = serial_->Read(buf, kReadChunkSize);
    if (n == 0) {
      break;
    }
    decoder_.Feed(std::span<const uint8_t>(buf, n));
  }

  // 2. Pull any complete frames out of the decoder and handle them.
  while (auto frame = decoder_.Next()) {
    HandleSerialFrame(*frame);
  }

  // 3. Try one LoRa RX. On real hardware this would block up to
  //    rx_timeout_ms_; in the test build MockRadioBackend respects
  //    the timeout and returns std::nullopt.
  TryReceiveOnce();

  // 4. Drain pending CAD / TX results to the serial port.
  DrainPendingOutgoing();
}

bool SerialLink::HandleSerialFrame(const Frame &f) {
  switch (f.type) {
  case FrameType::kSetConfig: {
    // Payload: [sf:1][bw:1][cr:1][power:int8][sync:1]
    //   sf: 7..12, bw: 0=125, 1=250, 2=500 kHz,
    //   cr: 5..8 (4/5..4/8), power: -9..+22 dBm, sync: any.
    if (f.payload.size() < 5) {
      return false;
    }
    Preset p{};
    const uint8_t sf = f.payload[0];
    switch (sf) {
    case 7:
      p.spread_factor = SpreadFactor::kSF7;
      break;
    case 8:
      p.spread_factor = SpreadFactor::kSF8;
      break;
    case 9:
      p.spread_factor = SpreadFactor::kSF9;
      break;
    case 10:
      p.spread_factor = SpreadFactor::kSF10;
      break;
    case 11:
      p.spread_factor = SpreadFactor::kSF11;
      break;
    case 12:
      p.spread_factor = SpreadFactor::kSF12;
      break;
    default:
      return false;
    }
    switch (f.payload[1]) {
    case 0:
      p.bandwidth_hz = BandwidthHz::k125kHz;
      break;
    case 1:
      p.bandwidth_hz = BandwidthHz::k250kHz;
      break;
    case 2:
      p.bandwidth_hz = BandwidthHz::k500kHz;
      break;
    default:
      return false;
    }
    const uint8_t cr = f.payload[2];
    if (cr < 5 || cr > 8) {
      return false;
    }
    p.coding_rate = static_cast<CodingRate>(cr);
    p.tx_power_dbm = static_cast<int8_t>(f.payload[3]);
    p.sync_word = f.payload[4];
    radio_->Init(p);
    return true;
  }

  case FrameType::kAck: {
    // Forward to the radio as a transmit. The payload is opaque
    // to the link; the radio's driver decides what to do with
    // it (typically: an ACK frame on the LoRa side).
    radio_->Transmit(f.payload);
    return true;
  }

  case FrameType::kTxDone:
  case FrameType::kRxPacket:
  case FrameType::kCadResult:
  case FrameType::kLog:
  case FrameType::kError:
    // These are output-only frame types; the link ignores them
    // when they appear on the input.
    return false;
  }
  return false;
}

void SerialLink::TryReceiveOnce() {
  auto rx = radio_->ReceiveBlocking(rx_timeout_ms_);
  if (!rx.has_value()) {
    return;
  }
  Frame f{};
  f.type = FrameType::kRxPacket;
  f.payload = std::move(*rx);
  std::vector<uint8_t> bytes = EncodeFrame(f);
  serial_->Write(std::span<const uint8_t>(bytes.data(), bytes.size()));
}

void SerialLink::DrainPendingOutgoing() {
  if (!cad_results_.empty()) {
    const bool busy = cad_results_.front();
    cad_results_.pop_front();
    Frame f{};
    f.type = FrameType::kCadResult;
    f.payload = std::vector<uint8_t>{static_cast<uint8_t>(busy ? 0x01 : 0x00)};
    std::vector<uint8_t> bytes = EncodeFrame(f);
    serial_->Write(std::span<const uint8_t>(bytes.data(), bytes.size()));
  }
  if (!tx_done_queue_.empty()) {
    tx_done_queue_.pop_front();
    Frame f{};
    f.type = FrameType::kTxDone;
    f.payload.clear();
    std::vector<uint8_t> bytes = EncodeFrame(f);
    serial_->Write(std::span<const uint8_t>(bytes.data(), bytes.size()));
  }
}

} // namespace tether::bridge
