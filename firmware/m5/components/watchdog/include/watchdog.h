// watchdog.h — Tether M5 watchdog feeder task (plan.md §4.8.6,
// §9.2).
//
// On real hardware this is the esp_task_wdt feed loop. It registers
// every FreeRTOS task that should be monitored, then re-feeds the
// hardware watchdog every 500 ms. The host build tracks feed counts
// per task so tests can verify the right tasks are registered.
//
// Phase 8 (§9.2) adds reset-reason capture: a small public API
// (LastResetReason, LastPanickedTask, ResetHistory, BootCount)
// records what triggered the last reset, which task was the
// culprit (if known), and how many times the M5 has booted since
// power-on. This is the operator-visible telemetry that drives
// the crash log (component crash_log, plan §9.3) and the
// operator's TUI (plan §10.1).

#pragma once

#include <cstdint>
#include <string>
#include <unordered_map>
#include <vector>

namespace tether::m5 {

// ResetReason enumerates the possible causes of a M5 boot. The
// values are stable across firmware versions: the operator's TUI
// (plan §10.1) and the post-mortem dump (plan §9.3) decode them
// with Watchdog::ResetReasonName.
enum class ResetReason : uint8_t {
  kUnknown = 0,       // uninitialised; should never appear in
                      // production after the first boot
  kPowerOn = 1,       // cold boot from a power cycle
  kSoftRestart = 2,   // esp_restart() called explicitly
  kTaskWdt = 3,       // esp_task_wdt fired: a registered task was
                      // starved past hung_threshold_ms
  kPanic = 4,         // an exception/assert fired
  kBrownout = 5,      // voltage dropped below the safe threshold
};

// ResetRecord is one entry in the bounded reset history. The
// history is exposed in the boot log and uploaded to the base
// station in the crash log (plan §9.3).
struct ResetRecord {
  ResetReason reason = ResetReason::kUnknown;
  std::string task_name; // empty for non-task reasons
  uint32_t boot_count = 0;
};

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

  // Phase 8 — reset reason telemetry.
  //
  // NotifyHung records that `task_name` missed a feed past the
  // threshold. On real hardware this is followed by a
  // RecordReset(ResetReason::kTaskWdt, task_name) and an
  // esp_restart(). On host we keep the two operations separate so
  // tests can drive each independently.
  void NotifyHung(const std::string &task_name);

  // RecordReset appends a reset event to the bounded history and
  // updates LastResetReason / LastPanickedTask. `task_name` may
  // be empty for non-task resets (e.g. kPowerOn).
  void RecordReset(ResetReason reason, const std::string &task_name);

  // NoteBoot increments BootCount. The boot loader calls this
  // once at the top of app_main().
  void NoteBoot();

  // ResetReason accessors.
  ResetReason LastResetReason() const { return last_reason_; }
  const std::string &LastPanickedTask() const { return last_panicked_; }
  uint32_t BootCount() const { return boot_count_; }
  uint32_t HungEventCount() const { return hung_count_; }
  const std::vector<ResetRecord> &ResetHistory() const { return history_; }

  // Static helper: human-readable name for a ResetReason. Used
  // by the boot log and the operator TUI.
  static const char *ResetReasonName(ResetReason r);

private:
  struct TaskInfo {
    uint64_t last_fed_at_ms = 0;
    uint64_t feed_count = 0;
  };
  std::unordered_map<std::string, TaskInfo> tasks_;
  uint64_t feed_count_ = 0;
  uint64_t now_ms_ = 0;
  uint32_t hung_threshold_ms_ = 5000;

  // Reset telemetry state.
  ResetReason last_reason_ = ResetReason::kPowerOn;
  std::string last_panicked_;
  uint32_t boot_count_ = 0;
  uint32_t hung_count_ = 0;
  std::vector<ResetRecord> history_;

  // Bounded history. The M5 has at most 16 entries in NVS; the
  // host build keeps the same cap so tests and prod agree.
  static constexpr std::size_t kMaxHistory = 16;
};

} // namespace tether::m5
