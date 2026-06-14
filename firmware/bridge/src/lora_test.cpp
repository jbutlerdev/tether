// lora_test.cpp — unit tests for tether::bridge::lora (plan.md §3.2).
//
// Per the plan, "Most are integration tests on real hardware. Unit tests
// cover the non-RadioLib parts." We mock the RadioLib SX1262 backend via
// a small interface and record the calls. The real RadioLib glue lives
// in lora_radiolib.cpp and is only compiled in the [env:rak4631] env.

#include <unity.h>

#include <cstdint>
#include <optional>
#include <span>
#include <vector>

#include "lora.h"

using tether::bridge::Channel;
using tether::bridge::kUs915StartHz;
using tether::bridge::kUs915StepHz;
using tether::bridge::kUs915NumChannels;
using tether::bridge::Preset;
using tether::bridge::SpreadFactor;
using tether::bridge::BandwidthHz;
using tether::bridge::CodingRate;
using tether::bridge::MockRadioBackend;
using tether::bridge::LoRaRadio;

void setUp() {}
void tearDown() {}

// Test 1: Channel 0 maps to 902.3 MHz (US915 uplink ch 0, per research.md §6.2).
void test_lora_set_channel_frequency() {
    auto ch0 = Channel::FromIndex(0);
    TEST_ASSERT_EQUAL_UINT64(902'300'000ULL, ch0.frequency_hz);

    auto ch1 = Channel::FromIndex(1);
    TEST_ASSERT_EQUAL_UINT64(902'425'000ULL, ch1.frequency_hz);

    auto ch63 = Channel::FromIndex(63);
    TEST_ASSERT_EQUAL_UINT64(914'900'000ULL, ch63.frequency_hz);

    // Out-of-range index returns nullopt.
    TEST_ASSERT_FALSE(Channel::FromIndex(64).has_value());
    TEST_ASSERT_FALSE(Channel::FromIndex(255).has_value());
}

// Test 2: Preset maps to RadioLib's setSpreadingFactor / setBandwidth /
// setCodingRate / setSyncWord / setOutputPower calls.
void test_lora_preset_sf11_bw125() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);

    Preset preset{};
    preset.spread_factor = SpreadFactor::kSF11;
    preset.bandwidth_hz = BandwidthHz::k125kHz;
    preset.coding_rate = CodingRate::k4_8;
    preset.tx_power_dbm = 20;
    preset.sync_word = 0xF3;

    radio.Init(preset);

    // The mock records each call; assert the sequence.
    TEST_ASSERT_TRUE_MESSAGE(mock->saw_init, "init called");
    TEST_ASSERT_EQUAL_INT(static_cast<int>(SpreadFactor::kSF11),
                         static_cast<int>(mock->last_spread_factor));
    TEST_ASSERT_EQUAL_INT(static_cast<int>(BandwidthHz::k125kHz),
                         static_cast<int>(mock->last_bandwidth_hz));
    TEST_ASSERT_EQUAL_INT(static_cast<int>(CodingRate::k4_8),
                         static_cast<int>(mock->last_coding_rate));
    TEST_ASSERT_EQUAL_UINT8(0xF3, mock->last_sync_word);
    TEST_ASSERT_EQUAL_INT8(20, mock->last_tx_power_dbm);
}

// Test 3: SetChannel(ch) translates to setFrequency on the backend.
void test_lora_set_channel_calls_set_frequency() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    radio.SetChannel(7);
    // 902.3 MHz + 7 * 125 kHz = 903.175 MHz
    TEST_ASSERT_EQUAL_UINT64(903'175'000ULL, mock->last_frequency_hz);

    radio.SetChannel(63);
    TEST_ASSERT_EQUAL_UINT64(914'900'000ULL, mock->last_frequency_hz);
}

// Test 4: BUSY pin must be polled before every SPI transaction. We verify
// by injecting a mock that records the call order. Transmit triggers
// assert_busy() then SPI write of the packet.
void test_lora_busy_pin_polled_before_xfer() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    const std::vector<uint8_t> pkt{0x01, 0x02, 0x03, 0x04};
    radio.Transmit(pkt);

    // The mock records every call in order. The first call after Init must
    // be an SPI transfer of the packet contents (after CS handling). We
    // assert that the BUSY pin was polled (call sequence includes a
    // 'busy_wait' before 'spi_transfer').
    bool busy_then_xfer = false;
    for (size_t i = 0; i + 1 < mock->call_log.size(); ++i) {
        if (mock->call_log[i] == "busy_wait" &&
            mock->call_log[i + 1] == "spi_transfer") {
            busy_then_xfer = true;
            break;
        }
    }
    TEST_ASSERT_TRUE_MESSAGE(busy_then_xfer,
                             "BUSY must be polled before SPI transfer");
}

// Test 5: ReceiveBlocking returns nullopt on timeout.
void test_lora_receive_blocking_timeout() {
    auto mock = std::make_shared<MockRadioBackend>();
    mock->receive_returns_empty = true;  // simulate no packet
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    auto rx = radio.ReceiveBlocking(0);  // 0 ms timeout
    TEST_ASSERT_FALSE(rx.has_value());
}

// Test 6: ReceiveBlocking returns the packet the backend produced.
void test_lora_receive_blocking_returns_packet() {
    auto mock = std::make_shared<MockRadioBackend>();
    mock->next_received = std::vector<uint8_t>{0xDE, 0xAD, 0xBE, 0xEF};
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    auto rx = radio.ReceiveBlocking(100);
    TEST_ASSERT_TRUE(rx.has_value());
    TEST_ASSERT_EQUAL_size_t(4, rx->size());
    TEST_ASSERT_EQUAL_UINT8(0xDE, (*rx)[0]);
    TEST_ASSERT_EQUAL_UINT8(0xEF, (*rx)[3]);
}

// Test 7: StartCAD maps to RadioLib startChannelScan / startCAD.
void test_lora_start_cad() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    const bool ok = radio.StartCAD();
    TEST_ASSERT_TRUE(ok);
    TEST_ASSERT_TRUE(mock->saw_start_cad);
}

// Test 8: Sleep / Standby forward to the backend.
void test_lora_sleep_standby() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    radio.Sleep();
    TEST_ASSERT_TRUE(mock->saw_sleep);

    radio.Standby();
    TEST_ASSERT_TRUE(mock->saw_standby);
}

// Test 9: SetChannel on an out-of-range index returns false and does not
// touch the backend.
void test_lora_set_channel_out_of_range() {
    auto mock = std::make_shared<MockRadioBackend>();
    LoRaRadio radio(mock);
    radio.Init(Preset::Default());

    const size_t before = mock->call_log.size();
    TEST_ASSERT_FALSE(radio.SetChannel(64));
    TEST_ASSERT_FALSE(radio.SetChannel(255));
    TEST_ASSERT_EQUAL_size_t(before, mock->call_log.size());
}

// Test 10: Default preset (per research.md §6 / plan §2.4): SF11 / BW125 /
// CR 4/8 / sync 0xF3 / +20 dBm.
void test_lora_default_preset() {
    Preset p = Preset::Default();
    TEST_ASSERT_EQUAL_INT(static_cast<int>(SpreadFactor::kSF11),
                         static_cast<int>(p.spread_factor));
    TEST_ASSERT_EQUAL_INT(static_cast<int>(BandwidthHz::k125kHz),
                         static_cast<int>(p.bandwidth_hz));
    TEST_ASSERT_EQUAL_INT(static_cast<int>(CodingRate::k4_8),
                         static_cast<int>(p.coding_rate));
    TEST_ASSERT_EQUAL_UINT8(0xF3, p.sync_word);
    TEST_ASSERT_EQUAL_INT8(20, p.tx_power_dbm);
}

// Unity entry point.
int main(int /*argc*/, char** /*argv*/) {
    UNITY_BEGIN();
    RUN_TEST(test_lora_set_channel_frequency);
    RUN_TEST(test_lora_preset_sf11_bw125);
    RUN_TEST(test_lora_set_channel_calls_set_frequency);
    RUN_TEST(test_lora_busy_pin_polled_before_xfer);
    RUN_TEST(test_lora_receive_blocking_timeout);
    RUN_TEST(test_lora_receive_blocking_returns_packet);
    RUN_TEST(test_lora_start_cad);
    RUN_TEST(test_lora_sleep_standby);
    RUN_TEST(test_lora_set_channel_out_of_range);
    RUN_TEST(test_lora_default_preset);
    return UNITY_END();
}
