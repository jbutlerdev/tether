// watchdog.cpp — implementation of tether::m5::Watchdog.
#include "watchdog.h"

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_log.h"
#endif

namespace tether::m5 {

namespace {
constexpr char kTag[] = "tether.wdt";
}

bool Watchdog::Register(const std::string &task_name) {
  if (task_name.empty()) return false;
  tasks_[task_name] = TaskInfo{};
  return true;
}

void Watchdog::FeedAll() {
  feed_count_++;
  for (auto &kv : tasks_) {
    kv.second.last_fed_at_ms = now_ms_;
    kv.second.feed_count++;
  }
  now_ms_ += 500; // 500 ms period
}

uint64_t Watchdog::FeedCountFor(const std::string &task_name) const {
  auto it = tasks_.find(task_name);
  if (it == tasks_.end()) return 0;
  return it->second.feed_count;
}

bool Watchdog::IsHungForTest(const std::string &task_name) const {
  auto it = tasks_.find(task_name);
  if (it == tasks_.end()) return false;
  return (now_ms_ - it->second.last_fed_at_ms) > hung_threshold_ms_;
}

}  // namespace tether::m5
