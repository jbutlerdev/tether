// test_spi_bus.cpp — unit tests for tether::m5::SpiBus (plan.md §4.1).
//
// These tests are written first (TDD red phase). The production class
// `SpiBus` is introduced only after they fail for the right reason.
//
// Host-side tests use the fake bus defined in test_spi_bus_fake.h. The
// public API in include/spi_bus.h is implemented in src/spi_bus_host.cpp
// when TETHER_M5_HOST_TEST is defined, otherwise in src/spi_bus.cpp for
// real hardware.

#include <atomic>
#include <thread>

#include <unity.h>

#include "spi_bus.h"

namespace {

// Three CS pins used in the M5 design (hardware.md).
constexpr int kCsSd = 10;
constexpr int kCsLora = 8;
constexpr int kCsEpd = 9;

tether::m5::SpiBus *MakeBus() {
  return new tether::m5::SpiBus(
      SPI2_HOST, tether::m5::board::kPinSpiMosi,
      tether::m5::board::kPinSpiMiso, tether::m5::board::kPinSpiSck);
}

} // namespace

void setUp() {}
void tearDown() {}

// Test 1: initialize with 3 devices (SD, LoRa, EPD); all handles non-null.
void test_spi_bus_init() {
  auto *bus = MakeBus();
  TEST_ASSERT_EQUAL(ESP_OK, bus->AddDevice(kCsSd, 20'000'000));
  TEST_ASSERT_EQUAL(ESP_OK, bus->AddDevice(kCsLora, 8'000'000));
  TEST_ASSERT_EQUAL(ESP_OK, bus->AddDevice(kCsEpd, 4'000'000));
  TEST_ASSERT_NOT_NULL(bus->Handle(kCsSd));
  TEST_ASSERT_NOT_NULL(bus->Handle(kCsLora));
  TEST_ASSERT_NOT_NULL(bus->Handle(kCsEpd));
  delete bus;
}

// Test 2: recursive lock NOT allowed.
void test_spi_bus_lock_unlock() {
  auto *bus = MakeBus();
  TEST_ASSERT_TRUE(bus->Lock(0));
  // Second lock from the same task must fail (non-recursive).
  TEST_ASSERT_FALSE(bus->Lock(0));
  TEST_ASSERT_TRUE(bus->Unlock());
  // After releasing, a fresh lock must succeed.
  TEST_ASSERT_TRUE(bus->Lock(0));
  TEST_ASSERT_TRUE(bus->Unlock());
  delete bus;
}

// Test 3: lock from a different thread blocks.
void test_spi_bus_lock_blocks_other_core() {
  auto *bus = MakeBus();
  TEST_ASSERT_TRUE(bus->Lock(0));

  std::atomic<bool> acquired{false};
  std::thread t([&]() {
    // Try to take the lock; should block then succeed after a timeout
    // (we use a short timeout so the test doesn't hang).
    acquired.store(bus->Lock(pdMS_TO_TICKS(50)));
  });
  t.join();
  TEST_ASSERT_FALSE(acquired.load());

  // Release; the next attempt from any thread must succeed.
  TEST_ASSERT_TRUE(bus->Unlock());
  std::thread t2([&]() { acquired.store(bus->Lock(pdMS_TO_TICKS(50))); });
  t2.join();
  TEST_ASSERT_TRUE(acquired.load());
  TEST_ASSERT_TRUE(bus->Unlock());
  delete bus;
}

// Test 4: Handle() returns the same handle each time.
void test_spi_bus_lookup() {
  auto *bus = MakeBus();
  TEST_ASSERT_EQUAL(ESP_OK, bus->AddDevice(kCsSd, 20'000'000));
  auto h1 = bus->Handle(kCsSd);
  auto h2 = bus->Handle(kCsSd);
  TEST_ASSERT_EQUAL_PTR(h1, h2);
  delete bus;
}

// Test 5: Handle() of an unknown CS pin returns nullptr.
void test_spi_bus_unknown_device() {
  auto *bus = MakeBus();
  TEST_ASSERT_NULL(bus->Handle(99));
  delete bus;
}

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_spi_bus_init);
  RUN_TEST(test_spi_bus_lock_unlock);
  RUN_TEST(test_spi_bus_lock_blocks_other_core);
  RUN_TEST(test_spi_bus_lookup);
  RUN_TEST(test_spi_bus_unknown_device);
  (void)0;
  UNITY_END();
}
