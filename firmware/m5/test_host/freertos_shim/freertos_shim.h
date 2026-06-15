// freertos_shim.h — minimal FreeRTOS shim for host-side Tether M5 tests.
//
// Real ESP-IDF code uses the FreeRTOS API (xTaskCreate, xSemaphoreCreateMutex,
// xQueueCreate, etc.). On host we back the small subset the components need
// with std::thread / std::mutex / std::condition_variable. Each shim symbol
// is a thin wrapper that exists in the same namespace the components use
// (FreeRTOS is C, so we use the same names prefixed with `x` as FreeRTOS
// does, and keep the `portTICK_PERIOD_MS` / `pdMS_TO_TICKS` macros).
//
// We deliberately do NOT provide a full FreeRTOS API; we only implement
// what the M5 components need in plan.md §4. As new components are added,
// the shim grows.
//
// All shim symbols are gated by TETHER_M5_HOST_TEST. On real hardware the
// real FreeRTOS headers are used.

#pragma once

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <functional>
#include <mutex>
#include <queue>
#include <thread>
#include <vector>

#ifdef __cplusplus
extern "C" {
#endif

// Tick rate: real ESP-IDF uses CONFIG_FREERTOS_HZ=1000 (sdkconfig.defaults).
// Match that so timeout_ticks math is consistent.
#define configTICK_RATE_HZ 1000U
#define portTICK_PERIOD_MS (1000U / configTICK_RATE_HZ)
#define pdMS_TO_TICKS(ms) ((ms) * (configTICK_RATE_HZ / 1000U))
#define portMAX_DELAY 0xFFFFFFFFU

typedef uint32_t TickType_t;
typedef int BaseType_t;
typedef unsigned long UBaseType_t;

#define pdTRUE 1
#define pdFALSE 0
#define pdPASS (pdTRUE)
#define pdFAIL (pdFALSE)

// Opaque task, semaphore, and queue handles. The real FreeRTOS API uses
// `TaskHandle_t` / `SemaphoreHandle_t` / `QueueHandle_t` typedefs to
// forward-declared structs. We mirror that with void* on the C side and
// hide the real type in the .cpp.
typedef void *TaskHandle_t;
typedef void *SemaphoreHandle_t;
typedef void *QueueHandle_t;

// Task entry point.
typedef void (*TaskFunction_t)(void *);

// ── Task API ───────────────────────────────────────────────────────────

BaseType_t xTaskCreate(TaskFunction_t fn, const char *name,
                       uint32_t /*stack_depth*/, void *arg,
                       UBaseType_t priority, TaskHandle_t *out_handle);
void vTaskDelete(TaskHandle_t handle);
void vTaskDelay(TickType_t ticks);
TaskHandle_t xTaskGetCurrentTaskHandle(void);
const char *pcTaskGetName(TaskHandle_t handle);

// ── Semaphore / Mutex API ──────────────────────────────────────────────

SemaphoreHandle_t xSemaphoreCreateMutex(void);
void vSemaphoreDelete(SemaphoreHandle_t sem);
BaseType_t xSemaphoreTake(SemaphoreHandle_t sem, TickType_t ticks);
BaseType_t xSemaphoreGive(SemaphoreHandle_t sem);

// ── Queue API ─────────────────────────────────────────────────────────

QueueHandle_t xQueueCreate(UBaseType_t length, UBaseType_t item_size);
void vQueueDelete(QueueHandle_t q);
BaseType_t xQueueSend(QueueHandle_t q, const void *item, TickType_t ticks);
BaseType_t xQueueReceive(QueueHandle_t q, void *out, TickType_t ticks);

// ── ESP-IDF shim ──────────────────────────────────────────────────────

// ESP-IDF logs and error helpers used in production code paths. We just
// print to stderr so the host test runner captures anything important.
// On real hardware these come from driver/spi_common.h, driver/gpio.h,
// driver/spi_master.h, and esp_err.h. On host we declare them just
// enough for the components to compile.
typedef int spi_host_device_t;
typedef void *spi_device_handle_t;
typedef int gpio_num_t;

#define SPI2_HOST 2
#define SPI3_HOST 3
#define GPIO_NUM_0 0
#define GPIO_NUM_1 1
#define GPIO_NUM_2 2
#define GPIO_NUM_3 3
#define GPIO_NUM_4 4
#define GPIO_NUM_5 5
#define GPIO_NUM_6 6
#define GPIO_NUM_7 7
#define GPIO_NUM_8 8
#define GPIO_NUM_9 9
#define GPIO_NUM_10 10
#define GPIO_NUM_11 11
#define GPIO_NUM_12 12
#define GPIO_NUM_13 13
#define GPIO_NUM_14 14
#define GPIO_NUM_15 15
#define GPIO_NUM_16 16
#define GPIO_NUM_17 17
#define GPIO_NUM_18 18
#define GPIO_NUM_19 19
#define GPIO_NUM_20 20
#define GPIO_NUM_21 21
#define GPIO_NUM_NC (-1)

typedef int esp_err_t;
#define ESP_OK 0
#define ESP_FAIL -1
#define ESP_ERR_INVALID_STATE -2
#define ESP_ERR_INVALID_ARG -3
#define ESP_ERR_NO_MEM -4
#define ESP_ERR_TIMEOUT -5
#define ESP_ERR_NOT_FOUND -6

#define ESP_LOGI(tag, ...)                                                     \
  do { std::fprintf(stderr, "I (%s) ", tag); std::fprintf(stderr, __VA_ARGS__); \
       std::fprintf(stderr, "\n"); } while (0)
#define ESP_LOGW(tag, ...)                                                     \
  do { std::fprintf(stderr, "W (%s) ", tag); std::fprintf(stderr, __VA_ARGS__); \
       std::fprintf(stderr, "\n"); } while (0)
#define ESP_LOGE(tag, ...)                                                     \
  do { std::fprintf(stderr, "E (%s) ", tag); std::fprintf(stderr, __VA_ARGS__); \
       std::fprintf(stderr, "\n"); } while (0)
#define ESP_LOGD(tag, ...)                                                     \
  do { std::fprintf(stderr, "D (%s) ", tag); std::fprintf(stderr, __VA_ARGS__); \
       std::fprintf(stderr, "\n"); } while (0)

#ifdef __cplusplus
}
#endif
