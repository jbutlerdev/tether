// test_board_t3s3_mvsr.cpp — validates the LilyGO T3-S3 MVSR pin map.
//
// This test is compiled with -DTETHER_BOARD_T3S3_MVSR so board.h
// selects board_t3s3_mvsr.h. It pins the load-bearing invariants of
// the variant: no two functions share a GPIO (except the intentional
// SPI SCK/MOSI/MISO aliases), the do-not-touch QSPI flash/PSRAM pins
// (27–32) are not used by any peripheral, the capability flags match
// the MVSR hardware (no PCA9557, OLED, 2 buttons, no hardware mod),
// and every required peripheral has a non-NC pin.
//
// The matching test for the M5 variant is the default board.h build
// (test_board.cpp is a stub that just checks the M5 constants
// compile). A pin-conflict here is a build/flash-time bug on hardware
// that the host tests must catch before CI.

#include <cstdint>
#include <set>
#include <vector>

#include <unity.h>

// Select the MVSR variant BEFORE including board.h.
#define TETHER_BOARD_T3S3_MVSR 1
#include "board.h"

using namespace tether::m5::board;

void setUp(void) {}
void tearDown(void) {}

// Collect every non-NC, non-reserved GPIO the variant assigns, with a
// label for conflict reporting. Shared-bus aliases (kPinSpiSck ==
// kPinLoraSck etc.) are expected to collide and are filtered.
static std::vector<std::pair<int, const char *>> assignedPins() {
  return {
      {kPinLoraCs, "LoraCs"},
      {kPinLoraSck, "LoraSck"},
      {kPinLoraMosi, "LoraMosi"},
      {kPinLoraMiso, "LoraMiso"},
      {kPinLoraReset, "LoraReset"},
      {kPinLoraBusy, "LoraBusy"},
      {kPinLoraDio1, "LoraDio1"},
      {kPinSdCs, "SdCs"},
      {kPinSdSck, "SdSck"},
      {kPinSdMosi, "SdMosi"},
      {kPinSdMiso, "SdMiso"},
      {kPinI2sWs, "I2sWs"},
      {kPinI2sDin, "I2sDin"},
      {kPinMicEn, "MicEn"},
      {kPinAmpBclk, "AmpBclk"},
      {kPinAmpWs, "AmpWs"},
      {kPinAmpDout, "AmpDout"},
      {kPinAmpSdMode, "AmpSdMode"},
      {kPinButtonPtt, "ButtonPtt"},
      {kPinButtonMenu, "ButtonMenu"},
      {kPinBatteryAdc, "BatteryAdc"},
      {kPinLed, "Led"},
      {kPinVibrationMotor, "VibrationMotor"},
      {kPinUartTx, "UartTx"},
      {kPinUartRx, "UartRx"},
      {kPinI2c0Scl, "I2c0Scl"},
      {kPinI2c0Sda, "I2c0Sda"},
      {kPinI2c1Scl, "I2c1Scl"},
      {kPinI2c1Sda, "I2c1Sda"},
  };
}

// No two distinct functions may share a GPIO. The LoRa SPI aliases
// (kPinSpiSck/Mosi/Miso == kPinLoraSck/Mosi/Miso) are the SAME
// function, so they are not in the list above.
void test_no_pin_conflicts(void) {
  auto pins = assignedPins();
  std::set<int> seen;
  for (auto &p : pins) {
    if (p.first == GPIO_NUM_NC) {
      continue;
    }
    if (seen.count(p.first)) {
      char msg[64];
      snprintf(msg, sizeof(msg), "GPIO %d reused (%s)", p.first, p.second);
      TEST_FAIL_MESSAGE(msg);
    }
    seen.insert(p.first);
  }
}

// The QSPI flash/PSRAM pins (27–32) are the do-not-touch set on the
// T3-S3 (quad flash/PSRAM). No peripheral may use them — driving
// them crashes the flash controller.
void test_no_do_not_touch_pins(void) {
  auto pins = assignedPins();
  for (int forbidden = 27; forbidden <= 32; forbidden++) {
    for (auto &p : pins) {
      if (p.first == forbidden) {
        char msg[80];
        snprintf(
            msg, sizeof(msg),
            "GPIO %d (%s) is in the QSPI flash/PSRAM do-not-touch range 27-32",
            p.first, p.second);
        TEST_FAIL_MESSAGE(msg);
      }
    }
  }
}

// Capability flags must match the MVSR hardware.
void test_capability_flags(void) {
  TEST_ASSERT_FALSE(kHasPca9557);
  TEST_ASSERT_TRUE(kDisplayKind == DisplayKind::kOled);
  TEST_ASSERT_EQUAL(2, kNumButtons);
  TEST_ASSERT_FALSE(kNeedsHardwareMod);
  // MVSR: mic on I2S0, amp on I2S1 (separate buses).
  TEST_ASSERT_EQUAL(0, kI2sMicPort);
  TEST_ASSERT_EQUAL(1, kI2sAmpPort);
  TEST_ASSERT_NOT_EQUAL(kI2sMicPort, kI2sAmpPort);
  // V1.1 mic is PDM.
  TEST_ASSERT_TRUE(kMicInterface == MicInterface::kPdm);
  // LoRa on SPI2, SD on SPI3 (two separate buses).
  TEST_ASSERT_EQUAL(1, kLoraSpiHost); // SPI2_HOST
  TEST_ASSERT_EQUAL(2, kSdSpiHost);   // SPI3_HOST
  TEST_ASSERT_NOT_EQUAL(kLoraSpiHost, kSdSpiHost);
}

// Required peripherals must have real (non-NC) pins.
void test_required_pins_assigned(void) {
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraCs);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraSck);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraMosi);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraMiso);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraReset);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraBusy);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinLoraDio1);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinSdCs);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinSdSck);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinSdMosi);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinSdMiso);
  // Mic: PDM uses WS (clk) + DIN (data); BCLK/DOUT are NC (ok).
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinI2sWs);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinI2sDin);
  // Amp on its own I2S1 bus.
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinAmpBclk);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinAmpWs);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinAmpDout);
  // Buttons.
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinButtonPtt);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinButtonMenu);
  // OLED I2C bus.
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinI2c1Scl);
  TEST_ASSERT_NOT_EQUAL(GPIO_NUM_NC, kPinI2c1Sda);
}

// The EPD pins are all NC on the MVSR (no e-paper).
void test_no_epd_pins(void) {
  TEST_ASSERT_EQUAL(GPIO_NUM_NC, kPinEpdCs);
  TEST_ASSERT_EQUAL(GPIO_NUM_NC, kPinEpdBusy);
  TEST_ASSERT_EQUAL(GPIO_NUM_NC, kPinEpdDc);
}

// The board name identifies the variant.
void test_board_name(void) {
  TEST_ASSERT_EQUAL_STRING("LilyGO-T3S3-MVSR", kBoardName);
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_no_pin_conflicts);
  RUN_TEST(test_no_do_not_touch_pins);
  RUN_TEST(test_capability_flags);
  RUN_TEST(test_required_pins_assigned);
  RUN_TEST(test_no_epd_pins);
  RUN_TEST(test_board_name);
  return UNITY_END();
}
