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
  if (task_name.empty())
    return false;
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
  if (it == tasks_.end())
    return 0;
  return it->second.feed_count;
}

bool Watchdog::IsHungForTest(const std::string &task_name) const {
  auto it = tasks_.find(task_name);
  if (it == tasks_.end())
    return false;
  return (now_ms_ - it->second.last_fed_at_ms) > hung_threshold_ms_;
}

// NotifyHung flags `task_name` as hung. The production tick path
// calls this from the ISR after `hung_threshold_ms` has elapsed
// without a feed, and the next iteration calls RecordReset +
// esp_restart. We expose the operations separately so tests can
// drive them independently.
void Watchdog::NotifyHung(const std::string &task_name) {
  if (task_name.empty())
    return;
  auto it = tasks_.find(task_name);
  if (it == tasks_.end())
    return; // not a registered task; nothing to do
  hung_count_++;
}

void Watchdog::RecordReset(ResetReason reason, const std::string &task_name) {
  last_reason_ = reason;
  last_panicked_ = task_name;
  if (history_.size() >= kMaxHistory) {
    history_.erase(history_.begin());
  }
  history_.push_back(ResetRecord{reason, task_name, boot_count_});
}

void Watchdog::NoteBoot() { boot_count_++; }

const char *Watchdog::ResetReasonName(ResetReason r) {
  switch (r) {
  case ResetReason::kPowerOn:
    return "power-on";
  case ResetReason::kSoftRestart:
    return "soft-restart";
  case ResetReason::kTaskWdt:
    return "task-wdt";
  case ResetReason::kPanic:
    return "panic";
  case ResetReason::kBrownout:
    return "brownout";
  case ResetReason::kUnknown:
    return "unknown";
  }
  return "unknown";
}

} // namespace tether::m5
