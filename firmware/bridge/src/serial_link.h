// serial_link.h — bridge between LoRa radio and USB-Serial (plan.md §3.3).
//
// The link consumes decoded frames from the serial port (FrameDecoder),
// applies them to the LoRa radio (kSetConfig, kAck), and emits received
// LoRa packets back to the serial port as kRxPacket frames. It also
// forwards CAD results and TX-completion notifications.
//
// The class is plain C++: a Step() method runs one iteration. On real
// hardware (env:rak4631) a FreeRTOS task calls Step() in a loop; in the
// host test build (env:native) tests drive Step() synchronously.

#pragma once

#include <algorithm>
#include <cstdint>
#include <deque>
#include <memory>
#include <span>
#include <vector>

#include "frame.h"
#include "lora.h"

namespace tether::bridge {

// ── Serial port abstraction ────────────────────────────────────────────────

// Abstract serial port. The real build (rak4631 env) wraps Arduino's
// HardwareSerial; the host test build uses MockSerialPort.
class SerialPort {
public:
  virtual ~SerialPort() = default;

  // Number of bytes available to read.
  virtual size_t Available() const = 0;

  // Copy at most `len` bytes from the input stream into `out`. Returns
  // the number of bytes actually copied.
  virtual size_t Read(uint8_t *out, size_t len) = 0;

  // Write bytes to the output stream. The real implementation forwards
  // to Serial.write; the mock appends to an internal buffer.
  virtual void Write(std::span<const uint8_t> bytes) = 0;
};

// Mock serial port used by the host test build. Feed() pushes bytes into
// the read buffer; writes are recorded in `writes`.
class MockSerialPort : public SerialPort {
public:
  std::deque<uint8_t> input_buffer;         // bytes to be Read() next
  std::vector<std::vector<uint8_t>> writes; // one entry per Write() call

  // Push bytes for the next Read() call.
  void Feed(std::span<const uint8_t> bytes) {
    std::copy(bytes.begin(), bytes.end(), std::back_inserter(input_buffer));
  }

  size_t Available() const override { return input_buffer.size(); }
  size_t Read(uint8_t *out, size_t len) override {
    size_t n = std::min(len, input_buffer.size());
    for (size_t i = 0; i < n; ++i) {
      out[i] = input_buffer.front();
      input_buffer.pop_front();
    }
    return n;
  }
  void Write(std::span<const uint8_t> bytes) override {
    writes.emplace_back(bytes.begin(), bytes.end());
  }
};

// ── Serial link ───────────────────────────────────────────────────────────

class SerialLink {
public:
  SerialLink(std::shared_ptr<SerialPort> serial,
             std::shared_ptr<LoRaRadio> radio)
      : serial_(std::move(serial)), radio_(std::move(radio)) {}

  // Process one iteration. Drains the serial port, attempts a LoRa
  // receive, and emits any pending CAD / TX-completion notifications.
  // Safe to call repeatedly; idempotent when there is nothing to do.
  void Step();

  // Test hooks: inject a CAD completion or a TX-completion notification
  // into the link's outgoing queue. Real firmware calls these from the
  // SX1262 IRQ handler; the host build lets the test author script
  // exact ordering.
  void QueueCadResult(bool channel_busy);
  void QueueTxDone();

  // Override the default LoRa RX timeout (ms) used by Step().
  void SetReceiveTimeoutMs(uint32_t ms) { rx_timeout_ms_ = ms; }

private:
  // Process a decoded frame from the serial port. Returns true if the
  // frame was consumed.
  bool HandleSerialFrame(const Frame &f);

  // Process one LoRa RX attempt. Emits a kRxPacket frame if a packet
  // arrived, otherwise does nothing.
  void TryReceiveOnce();

  // Drain a single pending CAD / TX result into the serial output.
  void DrainPendingOutgoing();

  std::shared_ptr<SerialPort> serial_;
  std::shared_ptr<LoRaRadio> radio_;
  FrameDecoder decoder_;
  std::deque<bool> cad_results_;   // true = channel busy
  std::deque<bool> tx_done_queue_; // true = ack to send
  uint32_t rx_timeout_ms_ = 0;
};

} // namespace tether::bridge
