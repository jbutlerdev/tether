// pca9557.cpp — Tether M5 PCA9557 I/O expander driver implementation.
//
// The M5's PCA9557PW is on Wire1 (I2C1) at address 0x18, GPIO 47
// (SDA) and GPIO 48 (SCL) on the ESP32-S3. We talk to it via the
// ESP-IDF legacy I2C master driver (driver/i2c.h) because we are
// pinned to ESP-IDF v5.2.2 (see AGENTS.md §1) and the new
// i2c_master_* API is v5.3+.
//
// The PCA9557 register map is:
//
//   0x00 — Input port   (read-only)
//   0x01 — Output port  (R/W; bit = pin level)
//   0x02 — Polarity     (R/W; 0 = normal, 1 = inverted)
//   0x03 — Config       (R/W; 0 = output, 1 = input)
//
// We initialize every pin as an output (Config = 0) and drive
// safe defaults on the Output port.

#include "pca9557.h"

#include <cstring>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "driver/i2c.h"
#include "esp_log.h"
#endif

#include "board.h"

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.pca9557";

// PCA9557 register addresses.
constexpr uint8_t kRegInput = 0x00;
constexpr uint8_t kRegOutput = 0x01;
constexpr uint8_t kRegPolarity = 0x02;
constexpr uint8_t kRegConfig = 0x03;

// Pin map (matches pca9557.h comment).
constexpr uint8_t kPinLedNotification = 1; // blue LED
constexpr uint8_t kPinLedPowerRail = 2;    // LED V+ OR'd with VBUS
constexpr uint8_t kPinLedPower = 3;        // red LED
constexpr uint8_t kPinPeripheralPower = 4; // master power-gate
constexpr uint8_t kPinEinkBacklight = 5;   // E-ink backlight

#ifndef TETHER_M5_HOST_TEST
// I2C1 (Wire1) port. The legacy driver takes the port number
// directly per call, so no global handle is needed.
constexpr i2c_port_t kI2cPort = I2C_NUM_1;
constexpr int kI2cClockHz = 400'000; // Fast-mode
constexpr TickType_t kI2cTimeout = pdMS_TO_TICKS(100);
#endif
} // namespace

bool Pca9557::Init() {
#ifdef TETHER_M5_HOST_TEST
  initialized_ = true;
  return true;
#else
  if (initialized_)
    return true;

  // Configure I2C1 master on Wire1's pins (SDA=47, SCL=48).
  i2c_config_t conf = {};
  conf.mode = I2C_MODE_MASTER;
  conf.sda_io_num = board::kPinI2c1Sda;
  conf.scl_io_num = board::kPinI2c1Scl;
  conf.sda_pullup_en = GPIO_PULLUP_ENABLE;
  conf.scl_pullup_en = GPIO_PULLUP_ENABLE;
  conf.master.clk_speed = kI2cClockHz;
  esp_err_t err = i2c_param_config(kI2cPort, &conf);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2c_param_config: %d", err);
    return false;
  }
  err = i2c_driver_install(kI2cPort, I2C_MODE_MASTER, 0, 0, 0);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "i2c_driver_install: %d", err);
    return false;
  }

  // Configure all 8 pins as outputs (Config = 0x00).
  uint8_t cfg_zero[2] = {kRegConfig, 0x00};
  err = i2c_master_write_to_device(kI2cPort, board::kPca9557I2cAddr, cfg_zero,
                                   sizeof(cfg_zero), kI2cTimeout);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "PCA9557 set Config=0x00: %d", err);
    return false;
  }
  // No polarity inversion (Polarity = 0x00).
  uint8_t pol_zero[2] = {kRegPolarity, 0x00};
  err = i2c_master_write_to_device(kI2cPort, board::kPca9557I2cAddr, pol_zero,
                                   sizeof(pol_zero), kI2cTimeout);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "PCA9557 set Polarity=0x00: %d", err);
    return false;
  }

  // Read the current Output register so our cache is consistent.
  uint8_t reg = kRegOutput;
  uint8_t out_val = 0xFF;
  err = i2c_master_write_read_device(kI2cPort, board::kPca9557I2cAddr, &reg, 1,
                                     &out_val, 1, kI2cTimeout);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "PCA9557 read Output: %d", err);
    return false;
  }
  output_cache_ = out_val;

  // Default safe state: LEDs off, peripheral power ON, e-ink
  // backlight OFF. The master power rail MUST be on at boot; if
  // it's off the LoRa won't respond and we'll see a "sx1262 not
  // detected" fault.
  SetLedNotification(false);
  SetLedPower(false);
  SetLedPowerRail(true); // power the LED rail so future SetLedX
                         // calls actually illuminate the LEDs.
  SetEinkBacklight(false);
  SetPeripheralPower(true); // master rail: peripherals powered.

  initialized_ = true;
  ESP_LOGI(kTag, "PCA9557 ready (Wire1 @ 0x%02x, SDA=%d, SCL=%d)",
           board::kPca9557I2cAddr, board::kPinI2c1Sda, board::kPinI2c1Scl);
  return true;
#endif
}

bool Pca9557::WritePin(uint8_t pin, bool high) {
  if (pin >= 8)
    return false;
  uint8_t new_val = (output_cache_ & ~(1u << pin)) | (high ? (1u << pin) : 0u);
  if (new_val == output_cache_)
    return true; // no-op
#ifndef TETHER_M5_HOST_TEST
  uint8_t buf[2] = {kRegOutput, new_val};
  esp_err_t err = i2c_master_write_to_device(kI2cPort, board::kPca9557I2cAddr,
                                             buf, sizeof(buf), kI2cTimeout);
  if (err != ESP_OK) {
    ESP_LOGE(kTag, "PCA9557 set Output=0x%02x: %d", new_val, err);
    return false;
  }
#endif
  output_cache_ = new_val;
  return true;
}

void Pca9557::SetLedNotification(bool on) { WritePin(kPinLedNotification, on); }
void Pca9557::SetLedPower(bool on) { WritePin(kPinLedPower, on); }
void Pca9557::SetLedPowerRail(bool on) { WritePin(kPinLedPowerRail, on); }
void Pca9557::SetEinkBacklight(bool on) { WritePin(kPinEinkBacklight, on); }
void Pca9557::SetPeripheralPower(bool on) { WritePin(kPinPeripheralPower, on); }

void Pca9557::ResetForTest() {
  output_cache_ = 0xFF;
  initialized_ = false;
}

} // namespace tether::m5
