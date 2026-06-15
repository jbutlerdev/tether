// conv_manager.h — Tether M5 conv manager task (plan.md §5.5).
//
// The conv manager owns the live conversation list on the M5.
// It receives UI_UPDATE packets from the base station (via the
// radio task) and applies them to the ConvDb. On startup it
// emits a single sync-request packet so the base can push any
// convs the M5 missed while offline.
//
// The manager is the bridge between the radio_task (which
// owns the wire format) and the ConvDb (which owns persistent
// state). The UI state reads from the ConvDb on every render
// (a fresh `List()`); the manager does not push convs into
// the UI directly.
//
// On real hardware the manager is a FreeRTOS task with a
// queue of incoming UI_UPDATE packets. The queue is fed by
// the radio_task. On host (unit tests) the queue is bypassed
// and OnUiUpdate() is called directly.

#pragma once

#include <cstdint>
#include <string>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include "conv_db.h"

namespace tether::m5 {

// A single UI_UPDATE packet, parsed from the wire format. The
// 'remove' flag distinguishes add from delete.
struct UiUpdatePacket {
  ConvInfo info;
  bool remove = false;
};

// The wire-format packet the manager emits to the radio. On
// real hardware this is a serialized proto; on host it is a
// plain struct the test sink records.
struct OutgoingPacket {
  uint8_t msg_type = 0;       // 0x07 = MSG_TYPE_UI_UPDATE per proto
  bool is_sync_request = false;
  ConvInfo info;              // populated for UI_UPDATE
  bool remove = false;        // populated for UI_UPDATE
};

// The packet sink. In production this is the radio task; in
// tests it is an in-memory recorder. The manager calls
// `SendPacket` once for the startup sync request and once
// every kPingIntervalMs while awake.
class ConvManagerSink {
public:
  virtual ~ConvManagerSink() = default;
  virtual esp_err_t SendPacket(const OutgoingPacket &pkt) = 0;
};

// Default in-memory sink used by the manager when none is
// wired. Tests use this implicitly; production code can
// inject a radio-task-backed sink.
class MemorySink : public ConvManagerSink {
public:
  esp_err_t SendPacket(const OutgoingPacket &pkt) override {
    packets_.push_back(pkt);
    return ESP_OK;
  }
  const std::vector<OutgoingPacket> &Packets() const { return packets_; }

private:
  std::vector<OutgoingPacket> packets_;
};

class ConvManager {
public:
  // The manager does not own the ConvDb. The pointer must
  // outlive the manager.
  explicit ConvManager(ConvDb &db);

  // Allow tests / production to inject a custom sink. The
  // manager takes a non-owning pointer.
  void SetSink(ConvManagerSink *sink) { sink_ = sink; }

  // The default sink used when SetSink is not called. Always
  // non-null after construction.
  MemorySink &DefaultSink() { return default_sink_; }
  const std::vector<OutgoingPacket> &SentPackets() const {
    return default_sink_.Packets();
  }

  // Start the task. Emits a single sync-request packet to
  // the sink and schedules the periodic ping. Idempotent.
  esp_err_t Start();

  // Stop the task. Idempotent.
  void Stop();

  // Handle a UI_UPDATE packet (called by the radio task on
  // real hardware; called directly by tests on host).
  void OnUiUpdate(const UiUpdatePacket &pkt);

  // Test seam: force-fire the periodic ping. Production code
  // uses a FreeRTOS timer (Phase 8 wires it).
  void ForcePingForTest();

  // Counters, for tests and metrics.
  size_t AddCount() const { return add_count_; }
  size_t RemoveCount() const { return remove_count_; }
  size_t PingCount() const { return ping_count_; }

  // Periodic ping cadence. The plan says "every 5 min while
  // awake" (plan §5.5).
  static constexpr uint32_t kPingIntervalMs = 5 * 60 * 1000;

private:
  void EmitSyncRequest();

  ConvDb &db_;
  ConvManagerSink *sink_ = nullptr;
  MemorySink default_sink_;
  bool started_ = false;
  size_t add_count_ = 0;
  size_t remove_count_ = 0;
  size_t ping_count_ = 0;
};

} // namespace tether::m5
