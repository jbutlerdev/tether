// spi_bus.h — Tether M5 SPI bus singleton with mutex and per-CS device
// handles (plan.md §4.1).
//
// The M5's onboard SX1262, SD card, and EPD all share one HSPI bus. A LoRa
// IRQ firing mid-SD-write would crash the bus, so every bus activity takes
// `spi_bus_mutex` first (research.md §7.4). Three `spi_device_handle_t`
// live on the same bus, one per CS pin.
//
// On real hardware the implementation in spi_bus.cpp wraps ESP-IDF. On
// host (unit tests) the same public API is backed by a fake bus defined
// in test/test_spi_bus_fake.h; the implementation lives in
// src/spi_bus_host.cpp behind the TETHER_M5_HOST_TEST macro.
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include "driver/gpio.h"
#include "driver/spi_common.h"
#include "driver/spi_master.h"
#include "esp_err.h"
#endif

namespace tether::m5 {

// Per-component log tag, also reused by every test.
inline constexpr char kSpiBusTag[] = "tether.spi";

// SPI bus singleton: a thin wrapper around the ESP-IDF SPI master driver
// that adds a recursive-non-mutex (see research.md §7.4) and per-CS device
// handles. Construction initializes the bus; the singleton is obtained
// via `Bus()`.
class SpiBus {
public:
  // Initialize the bus and arm the mutex. Pin numbers map directly to
  // ESP-IDF `spi_bus_initialize` arguments.
  SpiBus(spi_host_device_t host, gpio_num_t pin_mosi, gpio_num_t pin_miso,
         gpio_num_t pin_sclk);

  // Register a new device on the bus at `cs_pin`. `clock_hz` is the
  // maximum SPI clock the device tolerates. Idempotent on the same
  // (cs_pin, clock_hz) pair; re-registering with a different clock fails
  // with ESP_ERR_INVALID_STATE.
  esp_err_t AddDevice(int cs_pin, int clock_hz, int queue_size = 4);

  // Return the handle previously registered for `cs_pin`, or nullptr.
  spi_device_handle_t Handle(int cs_pin) const;

  // Try to take the bus mutex. Non-recursive: a task that already owns the
  // mutex will fail (returns false) instead of deadlocking. Blocks until
  // the mutex is available OR `timeout_ticks` elapses (pass
  // `portMAX_DELAY` for infinite). Returns true on success.
  bool Lock(TickType_t timeout_ticks = portMAX_DELAY);

  // Release the mutex. Calling Unlock without a matching Lock is a no-op
  // (returns false). Returns true when the lock was actually released.
  bool Unlock();

  // Destroy the mutex and free the registered devices. Real firmware
  // never calls this; tests use it between cases.
  ~SpiBus();

private:
  spi_host_device_t host_;
  bool bus_initialized_ = false;
  SemaphoreHandle_t mutex_ = nullptr;
  void *owner_ = nullptr; // current lock holder; nullptr = free
  std::map<int, spi_device_handle_t> handles_;
};

// Global singleton accessor. The first call lazily constructs the bus
// with the pin map from hardware.md. Subsequent calls return the same
// instance.
SpiBus &Bus();

} // namespace tether::m5
