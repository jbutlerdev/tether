// spi_bus.cpp — production ESP-IDF implementation of tether::m5::SpiBus.
//
// This file is compiled when building the firmware with `idf.py build`.
// The host build (test_host) compiles spi_bus_host.cpp instead, which
// uses the same public API against a fake bus.
//
// The non-recursive mutex pattern is load-bearing (research.md §7.4):
//   * All SPI activity takes spi_bus_mutex first.
//   * The SX1262 ISR is flag-setter only; the radio task does heavy
//     SPI work after taking the mutex.
//   * No task holds the mutex more than 10 ms; the watchdog will reset
//     a task that exceeds that.

#include "spi_bus.h"

#include <stdexcept>

#include "esp_log.h"
#include "board.h"

namespace tether::m5 {

SpiBus::SpiBus(spi_host_device_t host, gpio_num_t pin_mosi, gpio_num_t pin_miso,
               gpio_num_t pin_sclk) {
  mutex_ = xSemaphoreCreateMutex();
  if (!mutex_) {
    throw std::runtime_error("SpiBus: xSemaphoreCreateMutex failed");
  }
  // ESP-IDF's bus initialization. We use the DMA-capable channel
  // (SPI_DMA_CH_AUTO) and a 0-byte intr_alloc flag because we do all
  // transaction polling from FreeRTOS tasks.
  spi_bus_config_t bus_cfg = {};
  bus_cfg.mosi_io_num = pin_mosi;
  bus_cfg.miso_io_num = pin_miso;
  bus_cfg.sclk_io_num = pin_sclk;
  bus_cfg.quadwp_io_num = -1;
  bus_cfg.quadhd_io_num = -1;
  bus_cfg.max_transfer_sz = 0; // default
  bus_cfg.flags = SPICOMMON_BUSFLAG_MASTER | SPICOMMON_BUSFLAG_SCLK |
                  SPICOMMON_BUSFLAG_MOSI | SPICOMMON_BUSFLAG_MISO;
  esp_err_t err = spi_bus_initialize(host, &bus_cfg, SPI_DMA_CH_AUTO);
  if (err != ESP_OK) {
    vSemaphoreDelete(mutex_);
    mutex_ = nullptr;
    throw std::runtime_error("SpiBus: spi_bus_initialize failed");
  }
  host_ = host;
  bus_initialized_ = true;
}

SpiBus::~SpiBus() {
  if (bus_initialized_) {
    spi_bus_free(host_);
    bus_initialized_ = false;
  }
  if (mutex_) {
    vSemaphoreDelete(mutex_);
    mutex_ = nullptr;
  }
  handles_.clear();
}

esp_err_t SpiBus::AddDevice(int cs_pin, int clock_hz, int queue_size) {
  if (!bus_initialized_)
    return ESP_FAIL;
  if (handles_.find(cs_pin) != handles_.end()) {
    return ESP_OK; // already registered
  }
  spi_device_interface_config_t dev_cfg = {};
  dev_cfg.mode = 0;
  dev_cfg.clock_speed_hz = clock_hz;
  dev_cfg.spics_io_num = cs_pin;
  dev_cfg.queue_size = queue_size;
  dev_cfg.flags = 0;
  dev_cfg.pre_cb = nullptr;
  dev_cfg.post_cb = nullptr;
  spi_device_handle_t handle = nullptr;
  esp_err_t err = spi_bus_add_device(host_, &dev_cfg, &handle);
  if (err != ESP_OK) {
    return err;
  }
  handles_[cs_pin] = handle;
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
  if (xSemaphoreGetMutexHolder(mutex_) == xTaskGetCurrentTaskHandle()) {
    // Already own the mutex — refuse recursive lock.
    return false;
  }
  return xSemaphoreTake(mutex_, timeout_ticks) == pdTRUE;
}

bool SpiBus::Unlock() {
  if (!mutex_)
    return false;
  if (xSemaphoreGetMutexHolder(mutex_) != xTaskGetCurrentTaskHandle()) {
    return false;
  }
  return xSemaphoreGive(mutex_) == pdTRUE;
}

namespace {
SpiBus *g_bus_instance = nullptr;
} // namespace

SpiBus &Bus() {
  if (!g_bus_instance) {
    // The M5's shared SPI bus uses GPIO 16/15/7 for SCK/MOSI/MISO
    // (per the meshtastic variant.h — these are fixed on the
    // ELECROW board; do not remap). The CS lines for individual
    // devices (LoRa, EPD, SD) are added via AddDevice().
    g_bus_instance = new SpiBus(SPI2_HOST, board::kPinSpiMosi,
                                board::kPinSpiMiso, board::kPinSpiSck);
  }
  return *g_bus_instance;
}

} // namespace tether::m5
