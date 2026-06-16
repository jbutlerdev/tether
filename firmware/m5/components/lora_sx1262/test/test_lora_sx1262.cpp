// test_lora_sx1262.cpp — unit tests for tether::m5::LoraRadio (plan.md §4.2).
//
// These tests use the MockRadioBackend defined in include/lora_sx1262.h.
// On real hardware the production class is wired to a RadioLib-backed
// backend; the host build exercises the same logic through the mock.

#include <unity.h>

#include <cstdint>
#include <memory>
#include <span>
#include <vector>

#include "frequency_hopping.h"
#include "lora_sx1262.h"

using tether::m5::BandwidthHz;
using tether::m5::Channel;
using tether::m5::ChooseNextChannel;
using tether::m5::CodingRate;
using tether::m5::kHopNumChannels;
using tether::m5::LoraRadio;
using tether::m5::MockRadioBackend;
using tether::m5::Preset;
using tether::m5::SpreadFactor;

namespace {
std::shared_ptr<MockRadioBackend> MakeRadio() {
  auto backend = std::make_shared<MockRadioBackend>();
  return backend;
}
} // namespace

void setUp() {}
void tearDown() {}

// Test 1: Init() configures the backend with the right preset.
void test_lora_init_sets_preset() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  Preset p;
  p.spread_factor = SpreadFactor::kSF11;
  p.bandwidth_hz = BandwidthHz::k125kHz;
  p.coding_rate = CodingRate::k4_8;
  p.tx_power_dbm = 20;
  p.sync_word = 0xF3;
  radio.Init(p);
  TEST_ASSERT_TRUE(backend->saw_init);
  TEST_ASSERT_EQUAL(static_cast<int>(SpreadFactor::kSF11),
                    static_cast<int>(backend->last_spread_factor));
  TEST_ASSERT_EQUAL(static_cast<int>(BandwidthHz::k125kHz),
                    static_cast<int>(backend->last_bandwidth_hz));
  TEST_ASSERT_EQUAL(static_cast<int>(CodingRate::k4_8),
                    static_cast<int>(backend->last_coding_rate));
  TEST_ASSERT_EQUAL(20, backend->last_tx_power_dbm);
  TEST_ASSERT_EQUAL_HEX32(0xF3, backend->last_sync_word);
}

// Test 2: SetChannel(0) → 902.3 MHz.
void test_lora_set_channel_0() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  TEST_ASSERT_TRUE(radio.SetChannel(0));
  TEST_ASSERT_EQUAL_UINT32(902'300'000, backend->last_frequency_hz);
}

// Test 3: SetChannel(63) → 910.175 MHz.
void test_lora_set_channel_63() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  TEST_ASSERT_TRUE(radio.SetChannel(63));
  TEST_ASSERT_EQUAL_UINT32(910'175'000, backend->last_frequency_hz);
}

// Test 4: SetChannel(64) returns false (out of range).
void test_lora_set_channel_64_out_of_range() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  TEST_ASSERT_FALSE(radio.SetChannel(64));
  TEST_ASSERT_FALSE(radio.SetChannel(255));
}

// Test 5: StartCAD() returns the backend's CAD result.
void test_lora_cad_returns_busy_or_clear() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  backend->saw_start_cad = false;
  bool clear = radio.StartCAD();
  TEST_ASSERT_TRUE(backend->saw_start_cad);
  // The mock always returns true (clear). On real hardware the SX1262
  // would return either true or false; both are valid outcomes.
  TEST_ASSERT_TRUE(clear);
}

// Test 6: Transmit() forwards the bytes to the backend and busy-waits.
void test_lora_transmit_blocks_until_done() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  std::vector<uint8_t> pkt(100, 0xAB);
  radio.Transmit(std::span<const uint8_t>(pkt.data(), pkt.size()));
  TEST_ASSERT_EQUAL_size_t(1, backend->sent_packets.size());
  TEST_ASSERT_EQUAL_size_t(100, backend->sent_packets[0].size());
  TEST_ASSERT_EQUAL_HEX32(0xAB, backend->sent_packets[0][0]);
  // The mock records: busy_wait, spi_transfer (see MockRadioBackend::Send).
  // Verify the call ordering put a busy_wait immediately before the
  // spi_transfer.
  bool found_busy_then_spi = false;
  for (size_t i = 0; i + 1 < backend->call_log.size(); ++i) {
    if (backend->call_log[i] == "busy_wait" &&
        backend->call_log[i + 1] == "spi_transfer") {
      found_busy_then_spi = true;
      break;
    }
  }
  TEST_ASSERT_TRUE(found_busy_then_spi);
}

// Test 7: Sleep() forwards to backend and records the call.
void test_lora_sleep_lowers_current() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  radio.Sleep();
  TEST_ASSERT_TRUE(backend->saw_sleep);
}

// Test 8: ReceiveBlocking() returns the mock's next packet.
void test_lora_receive_returns_packet() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  backend->next_received = {0x01, 0x02, 0x03};
  auto got = radio.ReceiveBlocking(100);
  TEST_ASSERT_TRUE(got.has_value());
  TEST_ASSERT_EQUAL_size_t(3, got->size());
  TEST_ASSERT_EQUAL_HEX32(0x01, (*got)[0]);
}

// Test 9: ReceiveBlocking() returns nullopt on empty.
void test_lora_receive_returns_nullopt_on_empty() {
  auto backend = MakeRadio();
  LoraRadio radio(backend);
  backend->receive_returns_empty = true;
  auto got = radio.ReceiveBlocking(100);
  TEST_ASSERT_FALSE(got.has_value());
}

// Test 10: Channel::FromIndex encodes US915 frequency correctly.
void test_lora_channel_from_index() {
  auto ch0 = Channel::FromIndex(0);
  TEST_ASSERT_TRUE(ch0.has_value());
  TEST_ASSERT_EQUAL_UINT8(0, ch0->index);
  TEST_ASSERT_EQUAL_UINT32(902'300'000, ch0->frequency_hz);
  auto ch64 = Channel::FromIndex(64);
  TEST_ASSERT_FALSE(ch64.has_value());
}

// ── v2 hook tests (plan §10.4) ────────────────────────────────────
// These tests pin the v2 frequency-hopping hook. They assert the
// v1 build returns the input channel unchanged for every
// (channel, counter) pair. v2 inverts the identity tests: the
// function must return a different channel for at least one
// (channel, counter) pair.

// Test 11: the v2 hook is callable in v1.
void test_v2_hop_stub_exists() {
  uint8_t got = ChooseNextChannel(0, 0);
  TEST_ASSERT_EQUAL_UINT8(0, got);
}

// Test 12: v1 returns the input channel unchanged for a
// representative sample of (channel, counter) pairs.
void test_v2_hop_v1_is_identity() {
  struct Case {
    uint8_t channel;
    uint32_t counter;
  };
  const Case cases[] = {
      {0, 0}, {0, 1},     {0, 0xFFFFFFFFu},
      {1, 0}, {32, 1234}, {kHopNumChannels - 1, 99},
  };
  for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
    uint8_t got = ChooseNextChannel(cases[i].channel, cases[i].counter);
    TEST_ASSERT_EQUAL_UINT8(cases[i].channel, got);
  }
}

// Test 13: v1 is a no-op for the full range of channels.
void test_v2_hop_v1_full_range_is_identity() {
  for (uint8_t ch = 0; ch < kHopNumChannels; ++ch) {
    for (uint32_t counter = 0; counter < 16; ++counter) {
      uint8_t got = ChooseNextChannel(ch, counter);
      TEST_ASSERT_EQUAL_UINT8(ch, got);
    }
  }
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_lora_init_sets_preset);
  RUN_TEST(test_lora_set_channel_0);
  RUN_TEST(test_lora_set_channel_63);
  RUN_TEST(test_lora_set_channel_64_out_of_range);
  RUN_TEST(test_lora_cad_returns_busy_or_clear);
  RUN_TEST(test_lora_transmit_blocks_until_done);
  RUN_TEST(test_lora_sleep_lowers_current);
  RUN_TEST(test_lora_receive_returns_packet);
  RUN_TEST(test_lora_receive_returns_nullopt_on_empty);
  RUN_TEST(test_lora_channel_from_index);
  RUN_TEST(test_v2_hop_stub_exists);
  RUN_TEST(test_v2_hop_v1_is_identity);
  RUN_TEST(test_v2_hop_v1_full_range_is_identity);
  (void)0;
  UNITY_END();
}
