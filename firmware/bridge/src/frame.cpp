// frame.cpp — see frame.h. See plan.md §3.1.

#include "frame.h"

#include <cstring>
#include <stdexcept>

namespace tether::bridge {

namespace {

// CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF, no reflection, no XOR-out.
// Bitwise implementation; small constant size, no table. Inputs of a few
// hundred bytes are fine on the nRF52840.
uint16_t crc16ccitt(const uint8_t *data, size_t len) {
  uint16_t crc = 0xFFFF;
  for (size_t i = 0; i < len; ++i) {
    crc ^= static_cast<uint16_t>(data[i]) << 8;
    for (int b = 0; b < 8; ++b) {
      if (crc & 0x8000) {
        crc = static_cast<uint16_t>((crc << 1) ^ 0x1021);
      } else {
        crc = static_cast<uint16_t>(crc << 1);
      }
    }
  }
  return crc;
}

constexpr size_t kHeaderSize = 5; // magic0, magic1, type, len_lo, len_hi
constexpr size_t kFooterSize = 2; // crc_lo, crc_hi

} // namespace

std::vector<uint8_t> EncodeFrame(const Frame &f) {
  if (f.payload.size() > 0xFFFF) {
    throw std::invalid_argument(
        "EncodeFrame: payload exceeds 65535 bytes (0xFFFF max)");
  }
  const uint16_t len = static_cast<uint16_t>(f.payload.size());

  std::vector<uint8_t> out;
  out.reserve(kHeaderSize + f.payload.size() + kFooterSize);

  out.push_back(kMagic0);
  out.push_back(kMagic1);
  out.push_back(static_cast<uint8_t>(f.type));
  out.push_back(static_cast<uint8_t>(len & 0xFF));
  out.push_back(static_cast<uint8_t>((len >> 8) & 0xFF));
  out.insert(out.end(), f.payload.begin(), f.payload.end());

  // CRC covers type..last payload byte (everything after the magic and
  // before the CRC footer).
  const uint8_t *crc_start = out.data() + 2; // skip the 2 magic bytes
  const size_t crc_len = out.size() - 2;
  const uint16_t crc = crc16ccitt(crc_start, crc_len);

  out.push_back(static_cast<uint8_t>(crc & 0xFF));
  out.push_back(static_cast<uint8_t>((crc >> 8) & 0xFF));
  return out;
}

std::optional<Frame> DecodeFrame(std::span<const uint8_t> bytes) {
  if (bytes.size() < kHeaderSize + kFooterSize) {
    return std::nullopt;
  }
  if (bytes[0] != kMagic0 || bytes[1] != kMagic1) {
    return std::nullopt;
  }

  const uint8_t type = bytes[2];
  const uint16_t len = static_cast<uint16_t>(
      static_cast<uint16_t>(bytes[3]) | (static_cast<uint16_t>(bytes[4]) << 8));

  if (bytes.size() < kHeaderSize + len + kFooterSize) {
    return std::nullopt;
  }

  const uint16_t expected_crc = static_cast<uint16_t>(
      static_cast<uint16_t>(bytes[kHeaderSize + len]) |
      (static_cast<uint16_t>(bytes[kHeaderSize + len + 1]) << 8));

  const uint16_t actual_crc =
      crc16ccitt(bytes.data() + 2, kHeaderSize - 2 + len);
  if (actual_crc != expected_crc) {
    return std::nullopt;
  }

  Frame f{};
  f.type = static_cast<FrameType>(type);
  f.length = {bytes[3], bytes[4]};
  f.payload.assign(bytes.begin() + kHeaderSize,
                   bytes.begin() + kHeaderSize + len);
  f.crc = {bytes[kHeaderSize + len], bytes[kHeaderSize + len + 1]};
  return f;
}

void FrameDecoder::Feed(std::span<const uint8_t> bytes) {
  scratch_.insert(scratch_.end(), bytes.begin(), bytes.end());
}

std::optional<Frame> FrameDecoder::Next() {
  while (!scratch_.empty()) {
    const uint8_t b = scratch_.front();
    scratch_.erase(scratch_.begin());

    switch (state_) {
    case State::kWaitMagic0:
      if (b == kMagic0) {
        state_ = State::kWaitMagic1;
      }
      break;
    case State::kWaitMagic1:
      if (b == kMagic1) {
        state_ = State::kWaitType;
      } else if (b == kMagic0) {
        // Stay in the WAIT_MAGIC1 equivalent: we've seen one
        // magic byte, keep waiting for the second.
      } else {
        state_ = State::kWaitMagic0;
      }
      break;
    case State::kWaitType:
      pending_.type = static_cast<FrameType>(b);
      state_ = State::kWaitLenLo;
      break;
    case State::kWaitLenLo:
      pending_.length[0] = b;
      state_ = State::kWaitLenHi;
      break;
    case State::kWaitLenHi:
      pending_.length[1] = b;
      payload_remaining_ = static_cast<uint16_t>(
          static_cast<uint16_t>(pending_.length[0]) |
          (static_cast<uint16_t>(pending_.length[1]) << 8));
      pending_.payload.clear();
      pending_.payload.reserve(payload_remaining_);
      if (payload_remaining_ == 0) {
        state_ = State::kWaitCrcLo;
      } else {
        state_ = State::kWaitPayload;
      }
      break;
    case State::kWaitPayload:
      pending_.payload.push_back(b);
      --payload_remaining_;
      if (payload_remaining_ == 0) {
        state_ = State::kWaitCrcLo;
      }
      break;
    case State::kWaitCrcLo:
      pending_.crc[0] = b;
      state_ = State::kWaitCrcHi;
      break;
    case State::kWaitCrcHi:
      pending_.crc[1] = b;
      {
        // Verify CRC before emitting.
        const uint16_t expected_crc = static_cast<uint16_t>(
            static_cast<uint16_t>(pending_.crc[0]) |
            (static_cast<uint16_t>(pending_.crc[1]) << 8));

        // Reconstruct the type..last-payload bytes for the CRC.
        std::vector<uint8_t> crc_buf;
        crc_buf.reserve(3 + pending_.payload.size());
        crc_buf.push_back(static_cast<uint8_t>(pending_.type));
        crc_buf.push_back(pending_.length[0]);
        crc_buf.push_back(pending_.length[1]);
        crc_buf.insert(crc_buf.end(), pending_.payload.begin(),
                       pending_.payload.end());
        const uint16_t actual_crc = crc16ccitt(crc_buf.data(), crc_buf.size());
        state_ = State::kWaitMagic0;
        if (actual_crc == expected_crc) {
          return pending_;
        }
        // Bad CRC: drop the frame and keep scanning. The
        // pending buffer is reset by the next state machine
        // step.
      }
      break;
    }
  }
  return std::nullopt;
}

} // namespace tether::bridge
