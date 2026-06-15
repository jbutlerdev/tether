// conv_manager.cpp — implementation of tether::m5::ConvManager
// (plan.md §5.5).
//
// The manager is intentionally tiny. It is a thin adapter
// between the wire format (UI_UPDATE packets) and the
// persistent ConvDb. The on-host tests drive OnUiUpdate()
// directly; the production task entry point (in a future
// phase) feeds packets in via the radio queue.

#include "conv_manager.h"

namespace tether::m5 {

ConvManager::ConvManager(ConvDb &db) : db_(db), sink_(&default_sink_) {}

esp_err_t ConvManager::Start() {
  if (started_) {
    return ESP_OK;
  }
  started_ = true;
  // Phase 3 wire format: MSG_TYPE_UI_UPDATE = 0x07.
  EmitSyncRequest();
  return ESP_OK;
}

void ConvManager::Stop() {
  // No FreeRTOS task to stop in v1 (we don't have a real
  // task yet — the radio task feeds us directly). The flag
  // is what OnUiUpdate checks in production.
  started_ = false;
}

void ConvManager::EmitSyncRequest() {
  if (!sink_)
    return;
  OutgoingPacket pkt{};
  pkt.msg_type = 0x07; // MSG_TYPE_UI_UPDATE per proto
  pkt.is_sync_request = true;
  sink_->SendPacket(pkt);
  ++ping_count_;
}

void ConvManager::ForcePingForTest() { EmitSyncRequest(); }

void ConvManager::OnUiUpdate(const UiUpdatePacket &pkt) {
  if (pkt.remove) {
    db_.Remove(pkt.info.id);
    ++remove_count_;
  } else {
    db_.Upsert(pkt.info);
    ++add_count_;
  }
}

} // namespace tether::m5
