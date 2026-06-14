// serial_link_test.cpp — unit tests for tether::bridge::serial_link
// (plan.md §3.3).
//
// The serial link glues the LoRaRadio (one side) to a serial port
// (other side). On real hardware it runs as a FreeRTOS task; in the
// host test build it exposes a Step() method that processes one
// iteration so tests can drive it synchronously.

#include <unity.h>

#include <cstdint>
#include <deque>
#include <memory>
#include <span>
#include <vector>

#include "frame.h"
#include "lora.h"
#include "serial_link.h"

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

void setUp() {}
void tearDown() {}

// Test 1: a packet received over LoRa is emitted on the serial port as a
// kRxPacket frame: 0xAA 0x55 0x02 <len_lo> <len_hi> <payload> <crc_lo> <crc_hi>.
void test_serial_link_rx_packet_to_serial() {
    auto serial = std::make_shared<MockSerialPort>();
    auto radio_backend = std::make_shared<MockRadioBackend>();
    auto radio = std::make_shared<LoRaRadio>(radio_backend);
    SerialLink link(serial, radio);

    // Simulate a LoRa RX: enqueue a packet the backend will hand back.
    radio_backend->next_received = std::vector<uint8_t>{0xDE, 0xAD, 0xBE, 0xEF};

    link.Step();  // drains serial, then attempts RX

    // The serial port's write buffer should hold one frame.
    TEST_ASSERT_EQUAL_size_t(1, serial->writes.size());
    const auto& bytes = serial->writes.front();

    // Decode the frame and check type + payload.
    auto decoded = DecodeFrame(std::span<const uint8_t>(bytes.data(), bytes.size()));
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
    TEST_ASSERT_EQUAL_UINT8(0x00, decoded->payload[0]);  // channel free
}

// Test 4: kSetConfig applies the new config to the radio. The frame
// payload encodes SF / BW / CR / sync / power as a small binary blob.
void test_serial_link_set_config_applies_preset() {
    auto serial = std::make_shared<MockSerialPort>();
    auto radio_backend = std::make_shared<MockRadioBackend>();
    auto radio = std::make_shared<LoRaRadio>(radio_backend);
    SerialLink link(serial, radio);

    // Payload format: [sf:1][bw:1][cr:1][power:1][sync:1] = 5 bytes.
    // SF=12, BW=500k → 2, CR=8 → 3, power=22 dBm, sync=0x12.
    std::vector<uint8_t> payload{12, 2, 3, 22, 0x12};
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
        0xAA, 0x55,
        static_cast<uint8_t>(FrameType::kAck),
        0x01, 0x00,
        0x55,
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
    size_t tx_count = 0;
    for (const auto& entry : radio_backend->call_log) {
        if (entry == "spi_transfer") {
            tx_count++;
        }
    }
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

    const size_t calls_before = radio_backend->call_log.size();
    link.Step();
    TEST_ASSERT_EQUAL_size_t(calls_before, radio_backend->call_log.size());
}

// Test 7: receive with no packet available produces no serial output
// (idle step does nothing).
void test_serial_link_idle_step_does_nothing() {
    auto serial = std::make_shared<MockSerialPort>();
    auto radio_backend = std::make_shared<MockRadioBackend>();
    radio_backend->receive_returns_empty = true;  // no LoRa packet
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

// Unity entry point.
int main(int /*argc*/, char** /*argv*/) {
    UNITY_BEGIN();
    RUN_TEST(test_serial_link_rx_packet_to_serial);
    RUN_TEST(test_serial_link_serial_to_tx);
    RUN_TEST(test_serial_link_cad_result_to_serial);
    RUN_TEST(test_serial_link_set_config_applies_preset);
    RUN_TEST(test_serial_link_drops_bad_crc_then_processes_next);
    RUN_TEST(test_serial_link_drops_unknown_type);
    RUN_TEST(test_serial_link_idle_step_does_nothing);
    RUN_TEST(test_serial_link_tx_done_emitted_to_serial);
    return UNITY_END();
}
