// serial_link_test.cpp — unit tests for tether::bridge::serial_link
// (plan.md §3.3).
//
// The serial link glues the LoRaRadio (one side) to a serial port
// (other side). On real hardware it runs as a FreeRTOS task; in the
// host test build it exposes a Step() method that processes one
// iteration so tests can drive it synchronously.

#include <unity.h>

#include <algorithm>
#include <cstdint>
#include <deque>
#include <memory>
#include <span>
#include <string>
#include <vector>

#include "frame.h"
#include "lora.h"
#include "serial_link.h"

using tether::bridge::BandwidthHz;
using tether::bridge::CodingRate;
using tether::bridge::DecodeFrame;
using tether::bridge::EncodeFrame;
using tether::bridge::Frame;
using tether::bridge::FrameDecoder;
using tether::bridge::FrameType;
using tether::bridge::LoRaRadio;
using tether::bridge::MockRadioBackend;
using tether::bridge::MockSerialPort;
using tether::bridge::SerialLink;
using tether::bridge::SerialPort;
using tether::bridge::SpreadFactor;

void setUp() {}
void tearDown() {}

// Test 1: a packet received over LoRa is emitted on the serial port as a
// kRxPacket frame: 0xAA 0x55 0x02 <len_lo> <len_hi> <payload> <crc_lo>
// <crc_hi>.
void test_serial_link_rx_packet_to_serial() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // Simulate a LoRa RX: enqueue a packet the backend will hand back.
  radio_backend->next_received = std::vector<uint8_t>{0xDE, 0xAD, 0xBE, 0xEF};

  link.Step(); // drains serial, then attempts RX

  // The serial port's write buffer should hold one frame.
  TEST_ASSERT_EQUAL_size_t(1, serial->writes.size());
  const auto &bytes = serial->writes.front();

  // Decode the frame and check type + payload.
  auto decoded =
      DecodeFrame(std::span<const uint8_t>(bytes.data(), bytes.size()));
  TEST_ASSERT_TRUE(decoded.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kRxPacket),
                          static_cast<uint8_t>(decoded->type));
  TEST_ASSERT_EQUAL_size_t(4, decoded->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0xDE, decoded->payload[0]);
  TEST_ASSERT_EQUAL_UINT8(0xEF, decoded->payload[3]);
}

// Test 2: a kAck frame received on the serial port is forwarded to the
// LoRa radio as a transmit.
void test_serial_link_serial_to_tx() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // Build a kAck frame with 1 payload byte and push it onto the serial
  // input. MockSerialPort::Feed copies the bytes into the available
  // buffer so the next Step() can read them.
  Frame ack{};
  ack.type = FrameType::kAck;
  ack.payload = std::vector<uint8_t>{0x55};
  std::vector<uint8_t> ack_bytes = EncodeFrame(ack);
  serial->Feed(std::span<const uint8_t>(ack_bytes.data(), ack_bytes.size()));

  // A size assertion so we can compare against the recorded transmit.
  const size_t calls_before = radio_backend->call_log.size();

  link.Step();

  // The radio's mock should have seen a transmit (the Send call shows
  // up in the call log as 'busy_wait' + 'spi_transfer').
  bool saw_tx = false;
  for (size_t i = calls_before; i < radio_backend->call_log.size(); ++i) {
    if (radio_backend->call_log[i] == "spi_transfer") {
      saw_tx = true;
      break;
    }
  }
  TEST_ASSERT_TRUE_MESSAGE(saw_tx, "kAck frame must trigger LoRa TX");
}

// Test 3: when an internal CAD completes, the serial port receives a
// kCadResult frame encoding the channel-busy result.
void test_serial_link_cad_result_to_serial() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // Queue a CAD result internally and step. The CAD itself is mocked
  // (the backend always returns true; the link only forwards the
  // success path here).
  link.QueueCadResult(/*channel_busy=*/false);
  link.Step();

  TEST_ASSERT_EQUAL_size_t(1, serial->writes.size());
  auto decoded = DecodeFrame(std::span<const uint8_t>(
      serial->writes.front().data(), serial->writes.front().size()));
  TEST_ASSERT_TRUE(decoded.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kCadResult),
                          static_cast<uint8_t>(decoded->type));
  TEST_ASSERT_EQUAL_size_t(1, decoded->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0x00, decoded->payload[0]); // channel free
}

// Test 4: kSetConfig applies the new config to the radio. The frame
// payload encodes SF / BW / CR / sync / power as a small binary blob.
void test_serial_link_set_config_applies_preset() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // Payload format: [sf:1][bw:1][cr:1][power:1][sync:1] = 5 bytes.
  // SF=12, BW=2 (500 kHz), CR=8 (4/8), power=22 dBm, sync=0x12.
  std::vector<uint8_t> payload{12, 2, 8, 22, 0x12};
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> cfg_bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(cfg_bytes.data(), cfg_bytes.size()));

  link.Step();

  TEST_ASSERT_EQUAL_INT(static_cast<int>(SpreadFactor::kSF12),
                        static_cast<int>(radio_backend->last_spread_factor));
  TEST_ASSERT_EQUAL_INT(static_cast<int>(BandwidthHz::k500kHz),
                        static_cast<int>(radio_backend->last_bandwidth_hz));
  TEST_ASSERT_EQUAL_INT(static_cast<int>(CodingRate::k4_8),
                        static_cast<int>(radio_backend->last_coding_rate));
  TEST_ASSERT_EQUAL_INT8(22, radio_backend->last_tx_power_dbm);
  TEST_ASSERT_EQUAL_UINT8(0x12, radio_backend->last_sync_word);
}

// Test 5: malformed serial input (bad CRC) is dropped without
// disrupting subsequent frames.
void test_serial_link_drops_bad_crc_then_processes_next() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // Bad-CRC ack frame: magic + type + len=1 + 0x55 + bogus crc.
  std::vector<uint8_t> bad = {
      0xAA, 0x55, static_cast<uint8_t>(FrameType::kAck), 0x01, 0x00, 0x55,
      0x00, 0x00,
  };
  Frame ack{};
  ack.type = FrameType::kAck;
  ack.payload = std::vector<uint8_t>{0x77};
  std::vector<uint8_t> good = EncodeFrame(ack);
  serial->Feed(std::span<const uint8_t>(bad.data(), bad.size()));
  serial->Feed(std::span<const uint8_t>(good.data(), good.size()));

  // The radio should see exactly one transmit (the good ack).
  link.Step();
  // Count "spi_transfer" entries after Step; the bad frame must not
  // produce one.
  const size_t tx_count =
      std::count(radio_backend->call_log.begin(), radio_backend->call_log.end(),
                 std::string("spi_transfer"));
  TEST_ASSERT_EQUAL_size_t(1, tx_count);
}

// Test 6: a frame that arrives on the serial port with an unknown type
// is silently dropped (the link only knows kSetConfig, kAck, kCadResult,
// kRxPacket, kTxDone).
void test_serial_link_drops_unknown_type() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // kError type is in the protocol's reserved range; the link should
  // not forward it to the radio.
  Frame f{};
  f.type = FrameType::kError;
  f.payload = std::vector<uint8_t>{0xFF};
  std::vector<uint8_t> bytes = EncodeFrame(f);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));

  // Step() also calls TryReceiveOnce(), which is expected to issue
  // exactly 2 backend calls (WaitWhileBusy + Receive) per iteration.
  // The kError frame must not add any of its own.
  const size_t calls_before = radio_backend->call_log.size();
  link.Step();
  const size_t calls_after = radio_backend->call_log.size();
  TEST_ASSERT_EQUAL_size_t(2, calls_after - calls_before);
  // And the two calls should be busy_wait + receive (no spi_transfer).
  bool saw_tx = false;
  for (size_t i = calls_before; i < calls_after; ++i) {
    if (radio_backend->call_log[i] == "spi_transfer") {
      saw_tx = true;
    }
  }
  TEST_ASSERT_FALSE_MESSAGE(saw_tx, "kError must not trigger a LoRa TX");
}

// Test 7: receive with no packet available produces no serial output
// (idle step does nothing).
void test_serial_link_idle_step_does_nothing() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  radio_backend->receive_returns_empty = true; // no LoRa packet
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  link.Step();
  TEST_ASSERT_EQUAL_size_t(0, serial->writes.size());
}

// Test 8: queued kTxDone completion (from a previous LoRa TX) is
// emitted on the serial port as a kTxDone frame.
void test_serial_link_tx_done_emitted_to_serial() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  link.QueueTxDone();
  link.Step();

  TEST_ASSERT_EQUAL_size_t(1, serial->writes.size());
  auto decoded = DecodeFrame(std::span<const uint8_t>(
      serial->writes.front().data(), serial->writes.front().size()));
  TEST_ASSERT_TRUE(decoded.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kTxDone),
                          static_cast<uint8_t>(decoded->type));
}

// Test 9: a kSetConfig frame with an invalid SF is rejected and does
// not change the radio's current configuration.
void test_serial_link_set_config_rejects_invalid_sf() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  // SF=6 is invalid (must be 7..12). The link must NOT call Configure.
  const size_t calls_before = radio_backend->call_log.size();
  std::vector<uint8_t> payload{6, 0, 8, 20, 0xF3};
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  link.Step();
  // Step's RX attempt still produces 2 calls (busy_wait + receive),
  // but no 'configure' call should have been made.
  bool saw_configure = false;
  for (size_t i = calls_before; i < radio_backend->call_log.size(); ++i) {
    if (radio_backend->call_log[i] == "configure") {
      saw_configure = true;
    }
  }
  TEST_ASSERT_FALSE(saw_configure);
}

// Test 10: kSetConfig with an out-of-range bandwidth index is rejected.
void test_serial_link_set_config_rejects_invalid_bw() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  const size_t calls_before = radio_backend->call_log.size();
  std::vector<uint8_t> payload{11, 3, 8, 20, 0xF3}; // BW=3 is invalid
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  link.Step();
  bool saw_configure = false;
  for (size_t i = calls_before; i < radio_backend->call_log.size(); ++i) {
    if (radio_backend->call_log[i] == "configure") {
      saw_configure = true;
    }
  }
  TEST_ASSERT_FALSE(saw_configure);
}

// Test 11: kSetConfig with a short payload (less than 5 bytes) is rejected.
void test_serial_link_set_config_rejects_short_payload() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  const size_t calls_before = radio_backend->call_log.size();
  std::vector<uint8_t> payload{11, 0, 8}; // 3 bytes, missing power + sync
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  link.Step();
  bool saw_configure = false;
  for (size_t i = calls_before; i < radio_backend->call_log.size(); ++i) {
    if (radio_backend->call_log[i] == "configure") {
      saw_configure = true;
    }
  }
  TEST_ASSERT_FALSE(saw_configure);
}

// Test 12: kSetConfig with valid SF7, BW0 (125 kHz) is applied.
void test_serial_link_set_config_sf7_bw125() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  std::vector<uint8_t> payload{7, 0, 5, 14, 0x12}; // SF7, BW125, CR4_5
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  link.Step();

  TEST_ASSERT_EQUAL_INT(static_cast<int>(SpreadFactor::kSF7),
                        static_cast<int>(radio_backend->last_spread_factor));
  TEST_ASSERT_EQUAL_INT(static_cast<int>(BandwidthHz::k125kHz),
                        static_cast<int>(radio_backend->last_bandwidth_hz));
}

// Test 13: CAD result with channel_busy=true is encoded as 0x01.
void test_serial_link_cad_result_busy_emits_one() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  link.QueueCadResult(true);
  link.Step();

  TEST_ASSERT_EQUAL_size_t(1, serial->writes.size());
  auto decoded = DecodeFrame(std::span<const uint8_t>(
      serial->writes.front().data(), serial->writes.front().size()));
  TEST_ASSERT_TRUE(decoded.has_value());
  TEST_ASSERT_EQUAL_size_t(1, decoded->payload.size());
  TEST_ASSERT_EQUAL_UINT8(0x01, decoded->payload[0]);
}

// Test 14: CAD result + TX done queued together are both emitted in
// order (CAD first, then TxDone).
void test_serial_link_drains_cad_then_txdone_in_order() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  link.QueueCadResult(false);
  link.QueueTxDone();
  link.Step();

  TEST_ASSERT_EQUAL_size_t(2, serial->writes.size());
  auto f0 = DecodeFrame(std::span<const uint8_t>(serial->writes[0].data(),
                                                 serial->writes[0].size()));
  auto f1 = DecodeFrame(std::span<const uint8_t>(serial->writes[1].data(),
                                                 serial->writes[1].size()));
  TEST_ASSERT_TRUE(f0.has_value());
  TEST_ASSERT_TRUE(f1.has_value());
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kCadResult),
                          static_cast<uint8_t>(f0->type));
  TEST_ASSERT_EQUAL_UINT8(static_cast<uint8_t>(FrameType::kTxDone),
                          static_cast<uint8_t>(f1->type));
}

// Test 15: kSetConfig with SF=8/9/10 also applies correctly.
void test_serial_link_set_config_various_sf_bw() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  struct Case {
    uint8_t sf_byte;
    uint8_t bw_byte;
    SpreadFactor expected_sf;
    BandwidthHz expected_bw;
  };
  const std::vector<Case> cases = {
      {8, 0, SpreadFactor::kSF8, BandwidthHz::k125kHz},
      {9, 1, SpreadFactor::kSF9, BandwidthHz::k250kHz},
      {10, 2, SpreadFactor::kSF10, BandwidthHz::k500kHz},
  };
  for (const auto &c : cases) {
    std::vector<uint8_t> payload{c.sf_byte, c.bw_byte, 5, 15, 0x42};
    Frame cfg{};
    cfg.type = FrameType::kSetConfig;
    cfg.payload = payload;
    std::vector<uint8_t> bytes = EncodeFrame(cfg);
    serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
    link.Step();
    TEST_ASSERT_EQUAL_INT(static_cast<int>(c.expected_sf),
                          static_cast<int>(radio_backend->last_spread_factor));
    TEST_ASSERT_EQUAL_INT(static_cast<int>(c.expected_bw),
                          static_cast<int>(radio_backend->last_bandwidth_hz));
  }
}

// Test 16: kSetConfig with CR=4 (out of range) is rejected.
void test_serial_link_set_config_rejects_invalid_cr() {
  auto serial = std::make_shared<MockSerialPort>();
  auto radio_backend = std::make_shared<MockRadioBackend>();
  auto radio = std::make_shared<LoRaRadio>(radio_backend);
  SerialLink link(serial, radio);

  const size_t calls_before = radio_backend->call_log.size();
  std::vector<uint8_t> payload{11, 0, 4, 20, 0xF3}; // CR=4 invalid
  Frame cfg{};
  cfg.type = FrameType::kSetConfig;
  cfg.payload = payload;
  std::vector<uint8_t> bytes = EncodeFrame(cfg);
  serial->Feed(std::span<const uint8_t>(bytes.data(), bytes.size()));
  link.Step();
  bool saw_configure = false;
  for (size_t i = calls_before; i < radio_backend->call_log.size(); ++i) {
    if (radio_backend->call_log[i] == "configure") {
      saw_configure = true;
    }
  }
  TEST_ASSERT_FALSE(saw_configure);
}

// Unity entry point.
int main(int /*argc*/, char ** /*argv*/) {
  UNITY_BEGIN();
  RUN_TEST(test_serial_link_rx_packet_to_serial);
  RUN_TEST(test_serial_link_serial_to_tx);
  RUN_TEST(test_serial_link_cad_result_to_serial);
  RUN_TEST(test_serial_link_set_config_applies_preset);
  RUN_TEST(test_serial_link_drops_bad_crc_then_processes_next);
  RUN_TEST(test_serial_link_drops_unknown_type);
  RUN_TEST(test_serial_link_idle_step_does_nothing);
  RUN_TEST(test_serial_link_tx_done_emitted_to_serial);
  RUN_TEST(test_serial_link_set_config_rejects_invalid_sf);
  RUN_TEST(test_serial_link_set_config_rejects_invalid_bw);
  RUN_TEST(test_serial_link_set_config_rejects_short_payload);
  RUN_TEST(test_serial_link_set_config_sf7_bw125);
  RUN_TEST(test_serial_link_cad_result_busy_emits_one);
  RUN_TEST(test_serial_link_drains_cad_then_txdone_in_order);
  RUN_TEST(test_serial_link_set_config_various_sf_bw);
  RUN_TEST(test_serial_link_set_config_rejects_invalid_cr);
  return UNITY_END();
}
