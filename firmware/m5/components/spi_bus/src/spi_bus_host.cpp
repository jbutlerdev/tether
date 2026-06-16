// spi_bus_host.cpp — host-side implementation of tether::m5::SpiBus.
//
// This file is the host-test counterpart to spi_bus.cpp. It implements
// the same public API but uses std::thread / std::mutex to simulate the
// FreeRTOS mutex and a uintptr_t "fake handle" instead of an ESP-IDF
// spi_device_handle_t. The TETHER_M5_HOST_TEST guard is set by the
// parent test_host/CMakeLists.txt.

#ifdef TETHER_M5_HOST_TEST

#include "spi_bus.h"

#include <stdexcept>
#include <thread>

#include "board.h"

namespace tether::m5 {

namespace {

// A non-null sentinel that the host tests can check Handle() against.
spi_device_handle_t MakeFakeHandle(int cs) {
  // Encode the CS pin in the pointer so equality checks still work even
  // though the host has no real driver.
  return reinterpret_cast<spi_device_handle_t>(
      static_cast<uintptr_t>(0xBEEF0000ULL | (cs & 0xFFFF)));
}

// Per-thread token used to detect recursive Lock attempts from the same
// thread. Two threads calling Lock() concurrently get different addresses.
thread_local int g_lock_token = 0;

} // namespace

SpiBus::SpiBus(spi_host_device_t host, gpio_num_t pin_mosi, gpio_num_t pin_miso,
               gpio_num_t pin_sclk) {
  (void)host;
  (void)pin_mosi;
  (void)pin_miso;
  (void)pin_sclk;
  mutex_ = xSemaphoreCreateMutex();
  if (!mutex_) {
    throw std::runtime_error("SpiBus: xSemaphoreCreateMutex failed");
  }
  bus_initialized_ = true;
}

SpiBus::~SpiBus() {
  if (mutex_) {
    vSemaphoreDelete(mutex_);
    mutex_ = nullptr;
  }
  handles_.clear();
  bus_initialized_ = false;
}

esp_err_t SpiBus::AddDevice(int cs_pin, int clock_hz, int queue_size) {
  (void)queue_size;
  if (!bus_initialized_)
    return ESP_FAIL;
  auto it = handles_.find(cs_pin);
  if (it != handles_.end()) {
    return ESP_OK; // already registered
  }
  (void)clock_hz; // host build does not track clock rate
  handles_[cs_pin] = MakeFakeHandle(cs_pin);
  return ESP_OK;
}

spi_device_handle_t SpiBus::Handle(int cs_pin) const {
  auto it = handles_.find(cs_pin);
  if (it == handles_.end())
    return nullptr;
  return it->second;
}

bool SpiBus::Lock(TickType_t timeout_ticks) {
  if (!mutex_)
    return false;
  // Non-recursive: if the current thread already owns the mutex, fail
  // immediately. We use the address of a thread_local sentinel to detect
  // re-entry from the same thread.
  void *me = &g_lock_token;
  if (owner_ == me) {
    return false;
  }
  BaseType_t rc = xSemaphoreTake(mutex_, timeout_ticks);
  if (rc != pdPASS)
    return false;
  owner_ = me;
  return true;
}

bool SpiBus::Unlock() {
  if (!mutex_)
    return false;
  if (owner_ == nullptr)
    return false;
  // Allow any thread to release the mutex — this matches ESP-IDF's
  // xSemaphoreGive which is thread-agnostic for a binary mutex. The
  // non-recursive constraint is enforced in Lock().
  (void)&g_lock_token;
  owner_ = nullptr;
  return xSemaphoreGive(mutex_) == pdPASS;
}

namespace {
SpiBus *g_bus_instance = nullptr;
} // namespace

SpiBus &Bus() {
  if (!g_bus_instance) {
    g_bus_instance = new SpiBus(SPI2_HOST, board::kPinSpiMosi,
                                board::kPinSpiMiso, board::kPinSpiSck);
  }
  return *g_bus_instance;
}

} // namespace tether::m5

#endif // TETHER_M5_HOST_TEST
