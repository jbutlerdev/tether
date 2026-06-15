// freertos_shim.cpp — implementation of the FreeRTOS shim declared in
// freertos_shim.h. Backed by std::thread / std::mutex /
// std::condition_variable.

#include "freertos_shim.h"

#include <map>
#include <memory>
#include <mutex>
#include <stdexcept>
#include <string>

namespace {

// We index tasks and semaphores by their handle pointer. The std::thread
// is owned by a shared_ptr kept in a global map; the handle points to the
// struct xTaskHandle_t which is a thin owning struct.

struct xTaskHandle_t {
  std::string name;
  UBaseType_t priority;
  std::shared_ptr<std::thread> thread;
  std::thread::id std_id;
  // Stop signal: set by vTaskDelete so a blocking FreeRTOS call unblocks.
  std::shared_ptr<std::atomic<bool>> stop_flag;
  bool joinable = true;
};

struct xSemaphoreHandle_t {
  std::mutex m;
  std::condition_variable cv;
  bool taken = false;
  std::thread::id owner;
};

struct QueueItem {
  std::vector<uint8_t> bytes;
};

struct xQueueHandle_t {
  std::mutex m;
  std::condition_variable cv_push;
  std::condition_variable cv_pop;
  std::queue<QueueItem> q;
  UBaseType_t max_items;
  UBaseType_t item_size;
  bool closed = false;
};

std::mutex g_task_map_mutex;
std::map<TaskHandle_t, std::shared_ptr<xTaskHandle_t>> g_task_map;
std::mutex g_sem_map_mutex;
std::map<SemaphoreHandle_t, std::shared_ptr<xSemaphoreHandle_t>> g_sem_map;
std::mutex g_queue_map_mutex;
std::map<QueueHandle_t, std::shared_ptr<xQueueHandle_t>> g_queue_map;

std::thread::id g_main_thread_id;

std::thread::id CurrentId() { return std::this_thread::get_id(); }

TickType_t NowTicks() {
  using namespace std::chrono;
  static auto start = steady_clock::now();
  auto delta = duration_cast<milliseconds>(steady_clock::now() - start).count();
  return static_cast<TickType_t>(delta) * (configTICK_RATE_HZ / 1000U);
}

bool ShouldStop(SemaphoreHandle_t sem) {
  // The shim does not own stop flags; tests call vSemaphoreDelete to
  // unblock. The conditional_variable::wait_for will return if the queue
  // is closed; the semaphore is "deleted" in vSemaphoreDelete which sets
  // a flag the wait loop checks. Keep it simple: rely on a short timeout
  // and a separate `deleted` flag.
  (void)sem;
  return false;
}

struct SemDeleted {
  std::atomic<bool> deleted{false};
};
std::map<SemaphoreHandle_t, std::shared_ptr<SemDeleted>> g_sem_deleted;
std::mutex g_sem_deleted_mutex;

} // namespace

extern "C" {

BaseType_t xTaskCreate(TaskFunction_t fn, const char *name,
                       uint32_t /*stack_depth*/, void *arg,
                       UBaseType_t priority, TaskHandle_t *out_handle) {
  auto handle = std::make_shared<xTaskHandle_t>();
  handle->name = name ? name : "?";
  handle->priority = priority;
  handle->stop_flag = std::make_shared<std::atomic<bool>>(false);
  handle->thread = std::make_shared<std::thread>([fn, arg, handle]() {
    handle->std_id = std::this_thread::get_id();
    fn(arg);
  });
  TaskHandle_t raw = handle.get();
  std::lock_guard<std::mutex> lk(g_task_map_mutex);
  g_task_map[raw] = handle;
  if (out_handle) {
    *out_handle = raw;
  }
  return pdPASS;
}

void vTaskDelete(TaskHandle_t handle) {
  if (!handle) {
    handle = xTaskGetCurrentTaskHandle();
  }
  std::shared_ptr<xTaskHandle_t> victim;
  {
    std::lock_guard<std::mutex> lk(g_task_map_mutex);
    auto it = g_task_map.find(handle);
    if (it == g_task_map.end())
      return;
    victim = it->second;
    g_task_map.erase(it);
  }
  if (victim->thread && victim->thread->joinable()) {
    if (victim->std_id != CurrentId()) {
      victim->thread->join();
    }
  }
}

void vTaskDelay(TickType_t ticks) {
  std::this_thread::sleep_for(std::chrono::milliseconds(ticks));
}

TaskHandle_t xTaskGetCurrentTaskHandle(void) {
  auto id = CurrentId();
  std::lock_guard<std::mutex> lk(g_task_map_mutex);
  for (auto &kv : g_task_map) {
    if (kv.second->std_id == id) {
      return kv.first;
    }
  }
  return nullptr;
}

const char *pcTaskGetName(TaskHandle_t handle) {
  if (!handle)
    return "?";
  std::lock_guard<std::mutex> lk(g_task_map_mutex);
  auto it = g_task_map.find(handle);
  if (it == g_task_map.end())
    return "?";
  return it->second->name.c_str();
}

SemaphoreHandle_t xSemaphoreCreateMutex(void) {
  auto sem = std::make_shared<xSemaphoreHandle_t>();
  auto del = std::make_shared<SemDeleted>();
  std::lock_guard<std::mutex> lk(g_sem_map_mutex);
  SemaphoreHandle_t raw = sem.get();
  g_sem_map[raw] = sem;
  g_sem_deleted[raw] = del;
  return raw;
}

void vSemaphoreDelete(SemaphoreHandle_t sem) {
  if (!sem)
    return;
  std::shared_ptr<xSemaphoreHandle_t> s;
  {
    std::lock_guard<std::mutex> lk(g_sem_map_mutex);
    auto it = g_sem_map.find(sem);
    if (it == g_sem_map.end())
      return;
    s = it->second;
  }
  {
    std::lock_guard<std::mutex> lk(g_sem_deleted_mutex);
    auto it = g_sem_deleted.find(sem);
    if (it != g_sem_deleted.end())
      it->second->deleted.store(true);
  }
  { std::lock_guard<std::mutex> lk(s->m); }
  s->cv.notify_all();
  std::lock_guard<std::mutex> lk(g_sem_map_mutex);
  g_sem_map.erase(sem);
  std::lock_guard<std::mutex> lk2(g_sem_deleted_mutex);
  g_sem_deleted.erase(sem);
}

BaseType_t xSemaphoreTake(SemaphoreHandle_t sem, TickType_t ticks) {
  if (!sem)
    return pdFAIL;
  std::shared_ptr<SemDeleted> del;
  std::shared_ptr<xSemaphoreHandle_t> s;
  {
    std::lock_guard<std::mutex> lk(g_sem_map_mutex);
    auto sit = g_sem_map.find(sem);
    if (sit == g_sem_map.end())
      return pdFAIL;
    s = sit->second;
  }
  {
    std::lock_guard<std::mutex> lk(g_sem_deleted_mutex);
    auto dit = g_sem_deleted.find(sem);
    if (dit == g_sem_deleted.end())
      return pdFAIL;
    del = dit->second;
  }
  std::unique_lock<std::mutex> lk(s->m);
  auto deadline = std::chrono::steady_clock::now() +
                  std::chrono::milliseconds(ticks * portTICK_PERIOD_MS);
  while (s->taken) {
    if (del->deleted.load())
      return pdFAIL;
    if (ticks == portMAX_DELAY) {
      s->cv.wait(lk);
    } else {
      if (s->cv.wait_until(lk, deadline) == std::cv_status::timeout) {
        return pdFAIL;
      }
    }
    if (del->deleted.load())
      return pdFAIL;
  }
  s->taken = true;
  s->owner = CurrentId();
  return pdPASS;
}

BaseType_t xSemaphoreGive(SemaphoreHandle_t sem) {
  if (!sem)
    return pdFAIL;
  std::shared_ptr<xSemaphoreHandle_t> s;
  {
    std::lock_guard<std::mutex> lk(g_sem_map_mutex);
    auto it = g_sem_map.find(sem);
    if (it == g_sem_map.end())
      return pdFAIL;
    s = it->second;
  }
  {
    std::lock_guard<std::mutex> lk(s->m);
    if (!s->taken)
      return pdFAIL;
    s->taken = false;
  }
  s->cv.notify_one();
  return pdPASS;
}

QueueHandle_t xQueueCreate(UBaseType_t length, UBaseType_t item_size) {
  auto q = std::make_shared<xQueueHandle_t>();
  q->max_items = length;
  q->item_size = item_size;
  QueueHandle_t raw = q.get();
  std::lock_guard<std::mutex> lk(g_queue_map_mutex);
  g_queue_map[raw] = q;
  return raw;
}

void vQueueDelete(QueueHandle_t q) {
  if (!q)
    return;
  std::shared_ptr<xQueueHandle_t> qh;
  {
    std::lock_guard<std::mutex> lk(g_queue_map_mutex);
    auto it = g_queue_map.find(q);
    if (it == g_queue_map.end())
      return;
    qh = it->second;
    g_queue_map.erase(it);
  }
  {
    std::lock_guard<std::mutex> lk(qh->m);
    qh->closed = true;
  }
  qh->cv_pop.notify_all();
  qh->cv_push.notify_all();
}

BaseType_t xQueueSend(QueueHandle_t q, const void *item, TickType_t ticks) {
  if (!q || !item)
    return pdFAIL;
  std::shared_ptr<xQueueHandle_t> queue;
  {
    std::lock_guard<std::mutex> lk(g_queue_map_mutex);
    auto it = g_queue_map.find(q);
    if (it == g_queue_map.end())
      return pdFAIL;
    queue = it->second;
  }
  std::unique_lock<std::mutex> lk(queue->m);
  auto deadline = std::chrono::steady_clock::now() +
                  std::chrono::milliseconds(ticks * portTICK_PERIOD_MS);
  while (!queue->closed && queue->q.size() >= queue->max_items) {
    if (ticks == portMAX_DELAY) {
      queue->cv_push.wait(lk);
    } else {
      if (queue->cv_push.wait_until(lk, deadline) == std::cv_status::timeout) {
        return pdFAIL;
      }
    }
  }
  if (queue->closed)
    return pdFAIL;
  QueueItem qi;
  qi.bytes.assign(reinterpret_cast<const uint8_t *>(item),
                  reinterpret_cast<const uint8_t *>(item) + queue->item_size);
  queue->q.push(std::move(qi));
  queue->cv_pop.notify_one();
  return pdPASS;
}

BaseType_t xQueueReceive(QueueHandle_t q, void *out, TickType_t ticks) {
  if (!q || !out)
    return pdFAIL;
  std::shared_ptr<xQueueHandle_t> queue;
  {
    std::lock_guard<std::mutex> lk(g_queue_map_mutex);
    auto it = g_queue_map.find(q);
    if (it == g_queue_map.end())
      return pdFAIL;
    queue = it->second;
  }
  std::unique_lock<std::mutex> lk(queue->m);
  auto deadline = std::chrono::steady_clock::now() +
                  std::chrono::milliseconds(ticks * portTICK_PERIOD_MS);
  while (!queue->closed && queue->q.empty()) {
    if (ticks == portMAX_DELAY) {
      queue->cv_pop.wait(lk);
    } else {
      if (queue->cv_pop.wait_until(lk, deadline) == std::cv_status::timeout) {
        return pdFAIL;
      }
    }
  }
  if (queue->closed && queue->q.empty())
    return pdFAIL;
  auto &front = queue->q.front();
  std::memcpy(out, front.bytes.data(), queue->item_size);
  queue->q.pop();
  queue->cv_push.notify_one();
  return pdPASS;
}

} // extern "C"

// ── Host-side SHA-256 ─────────────────────────────────────────────────
//
// FIPS 180-4 §6.2. Textbook implementation; no platform
// dependencies. Mirrors what mbedTLS's mbedtls_sha256 does on
// real hardware, byte-for-byte. The component aes_link calls
// this on the host build (TETHER_M5_HOST_TEST); on real hardware
// it calls mbedtls_sha256 instead.
namespace tether::m5::test {

namespace {

constexpr uint32_t kSha256K[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

inline uint32_t rotr(uint32_t x, uint32_t n) { return (x >> n) | (x << (32 - n)); }

void Sha256Transform(uint32_t state[8], const uint8_t block[64]) {
  uint32_t w[64];
  for (int i = 0; i < 16; ++i) {
    w[i] = (uint32_t)block[i * 4] << 24 | (uint32_t)block[i * 4 + 1] << 16 |
           (uint32_t)block[i * 4 + 2] << 8 | (uint32_t)block[i * 4 + 3];
  }
  for (int i = 16; i < 64; ++i) {
    uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
    uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  uint32_t a = state[0], b = state[1], c = state[2], d = state[3];
  uint32_t e = state[4], f = state[5], g = state[6], h = state[7];
  for (int i = 0; i < 64; ++i) {
    uint32_t S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
    uint32_t ch = (e & f) ^ (~e & g);
    uint32_t t1 = h + S1 + ch + kSha256K[i] + w[i];
    uint32_t S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
    uint32_t mj = (a & b) ^ (a & c) ^ (b & c);
    uint32_t t2 = S0 + mj;
    h = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
  }
  state[0] += a; state[1] += b; state[2] += c; state[3] += d;
  state[4] += e; state[5] += f; state[6] += g; state[7] += h;
}

}  // namespace

void sha256(const uint8_t *data, std::size_t len, uint8_t out[32]) {
  uint32_t state[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
                       0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
  // Process full 64-byte blocks.
  std::size_t i = 0;
  while (i + 64 <= len) {
    Sha256Transform(state, data + i);
    i += 64;
  }
  // Final block(s) with length appended.
  uint8_t buf[128] = {0};
  std::size_t rem = len - i;
  std::memcpy(buf, data + i, rem);
  buf[rem] = 0x80;
  std::size_t pad_start;
  if (rem + 1 + 8 <= 64) {
    pad_start = 64;
  } else {
    pad_start = 128;
  }
  uint64_t bitlen = (uint64_t)len * 8;
  for (int j = 0; j < 8; ++j) {
    buf[pad_start - 1 - j] = (uint8_t)(bitlen >> (j * 8));
  }
  Sha256Transform(state, buf);
  if (pad_start == 128) {
    Sha256Transform(state, buf + 64);
  }
  for (int j = 0; j < 8; ++j) {
    out[j * 4] = (uint8_t)(state[j] >> 24);
    out[j * 4 + 1] = (uint8_t)(state[j] >> 16);
    out[j * 4 + 2] = (uint8_t)(state[j] >> 8);
    out[j * 4 + 3] = (uint8_t)(state[j]);
  }
}

}  // namespace tether::m5::test
