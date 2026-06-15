// watchdog.h — Tether M5 watchdog feeder task (plan.md §4.8.6).
//
// On real hardware this is the esp_task_wdt feed loop. It registers
// every FreeRTOS task that should be monitored, then re-feeds the
// hardware watchdog every 500 ms. The host build tracks feed counts
// per task so tests can verify the right tasks are registered.

#pragma once

#include <cstdint>
#include <string>
#include <unordered_map>
#include <vector>

namespace tether::m5 {

class Watchdog {
public:
  // Register a task to be fed. `task_name` is for diagnostics; the
  // real hardware also takes a TaskHandle_t.
  bool Register(const std::string &task_name);

  // Feed all registered tasks. Called every 500 ms.
  void FeedAll();

  // Total feeds since construction.
  uint64_t FeedCount() const { return feed_count_; }

  // Per-task feed counts (for tests).
  uint64_t FeedCountFor(const std::string &task_name) const;

  // Hung-task detection (host). If a task hasn't been fed in
  // `hung_threshold_ms`, mark it as hung. The real hardware's
  // esp_task_wdt will trigger a reset in this case; the host just
  // records it.
  void SetHungThresholdMsForTest(uint32_t ms) { hung_threshold_ms_ = ms; }
  bool IsHungForTest(const std::string &task_name) const;

private:
  struct TaskInfo {
    uint64_t last_fed_at_ms = 0;
    uint64_t feed_count = 0;
  };
  std::unordered_map<std::string, TaskInfo> tasks_;
  uint64_t feed_count_ = 0;
  uint64_t now_ms_ = 0;
  uint32_t hung_threshold_ms_ = 5000;
};

} // namespace tether::m5
