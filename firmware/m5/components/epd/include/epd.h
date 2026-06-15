// epd.h — Tether M5 e-paper display (plan.md §5.3 / research.md §9.3).
//
// The M5's 1.54" 200×200 monochrome EPD is driven through a small
// in-process abstraction:
//
//   * `EPD` — the controller. The production build drives the
//     GDEQ0154T100 panel over SPI. On host we have a no-op
//     controller that captures the last-rendered bitmap in
//     memory so the golden-image tests can read it back.
//   * `screens.h/.cpp` — pure rendering functions that take a
//     state struct and write a 200×200 monochrome bitmap. These
//     are the load-bearing pixels: every visual change on the M5
//     is produced by one of the RenderXxx() functions below.
//
// On real hardware the EPD controller takes a 200×200 / 8 = 5000
// byte buffer (1 bit per pixel, MSB = top-left). On host we use
// the same byte layout so the golden-image tests can compare the
// exact buffer the hardware would receive.
//
// Phase 3 only declared a placeholder EPD. Phase 4 implements
// the full controller, the renderer, and the screen state.

#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include "conv_db.h"

namespace tether::m5 {

// Display geometry (matches the GDEQ0154T100 panel).
inline constexpr size_t kEpdWidth = 200;
inline constexpr size_t kEpdHeight = 200;
inline constexpr size_t kEpdStride = kEpdWidth / 8; // 25 bytes / row
inline constexpr size_t kEpdBufSize = kEpdStride * kEpdHeight; // 5000 bytes
// Threshold for switching from partial to full refresh (plan §5.4).
inline constexpr uint32_t kEpdFullRefreshEvery = 50;

// Logical state passed to the idle renderer. Other screens take
// smaller state structs but expose the same buffer shape.
struct IdleState {
  // List of conversations (already sorted by activity).
  std::vector<ConvInfo> convs;
  size_t current_index = 0;  // index into `convs` of the active conv
  // Last 3 history entries (most recent first) for the active
  // conversation. Empty if no history.
  std::vector<HistoryEntry> recent;
  // Current battery voltage in millivolts. ≤ 3400 triggers the
  // low-battery warning screen.
  int vbat_mv = 3900;
  // True if the EPD is currently in low-battery state.
  bool low_battery = false;
  // Volume 0..100, shown on the idle screen and in settings.
  uint8_t volume = 60;
  // LoRa channel (0..63) and the current conv-switch scroll
  // position.
  uint8_t channel = 0;
  uint8_t scroll_pos = 0;  // first row of the conv tab strip
};

struct RecordingState {
  ConvInfo conv;             // the conv the recording is bound to
  uint32_t elapsed_ms = 0;   // duration of the recording so far
  uint16_t peak_amplitude = 0; // mic peak, 0..32767
};

struct QueuedState {
  ConvInfo conv;
  uint32_t file_bytes = 0;
  int64_t enqueued_at_ms = 0;
};

struct TransmittingState {
  ConvInfo conv;
  uint32_t sent_chunks = 0;
  uint32_t total_chunks = 0;
  uint32_t acked_chunks = 0;
  uint32_t elapsed_ms = 0;
  uint32_t estimated_total_ms = 0;
};

struct TtsState {
  ConvInfo conv;
  std::string current_text;  // truncated for EPD
  uint32_t elapsed_ms = 0;
  uint32_t total_ms = 0;
};

struct SettingsState {
  uint8_t channel = 0;
  uint8_t volume = 60;
  int vbat_mv = 3900;
  uint16_t node_addr = 0x4A1F;
  // "Modem" string for the screen (e.g. "SF11/BW125/CR 4/8").
  std::string modem;
  // Cursor position in the settings list (0..4).
  uint8_t cursor = 0;
};

struct LowBatteryState {
  int vbat_mv = 3300;
  bool critical = false; // < 3200 mV
};

// Pure renderer interface. All functions write exactly
// kEpdBufSize bytes into `out_buf`. They are allocation-free at
// runtime; the test suite enforces this with `--wrap=malloc`.
void RenderIdle(const IdleState &s, uint8_t *out_buf);
void RenderRecording(const RecordingState &s, uint8_t *out_buf);
void RenderQueued(const QueuedState &s, uint8_t *out_buf);
void RenderTransmitting(const TransmittingState &s, uint8_t *out_buf);
void RenderTtsPlayback(const TtsState &s, uint8_t *out_buf);
void RenderSettings(const SettingsState &s, uint8_t *out_buf);
void RenderLowBattery(const LowBatteryState &s, uint8_t *out_buf);

// Trivial bitmap helpers used by the renderers and the tests.
void BitmapFill(uint8_t *buf, bool pixel);
void BitmapDrawText(uint8_t *buf, int x, int y, const char *text,
                    bool pixel = true);
void BitmapDrawRect(uint8_t *buf, int x, int y, int w, int h, bool pixel);
void BitmapDrawHLine(uint8_t *buf, int x, int y, int w, bool pixel);
void BitmapDrawVLine(uint8_t *buf, int x, int y, int h, bool pixel);
void BitmapDrawProgressBar(uint8_t *buf, int x, int y, int w, int h,
                           uint8_t percent, bool pixel);

// ── Controller ─────────────────────────────────────────────────────────

class EPD {
public:
  // The last full bitmap the controller accepted. Tests read this
  // to compare against a golden image.
  const uint8_t *LastFullBitmap() const;
  const uint8_t *LastPartialBitmap() const;

  // Initialize the controller. Idempotent. Returns ESP_OK.
  esp_err_t Init();

  // Clear the display to all-white (or all-black on host).
  esp_err_t Clear();

  // Issue a partial refresh of a region with a bitmap. `bitmap`
  // is interpreted as a full 200×200 / 8 = 5000-byte buffer; the
  // driver itself tracks which region to update.
  esp_err_t PartialRefresh(const uint8_t *bitmap);

  // Issue a full refresh of a full-frame bitmap.
  esp_err_t FullRefresh(const uint8_t *bitmap);

  // Count of partial refreshes since the last full refresh. The
  // ui_state task polls this to decide when to escalate.
  uint32_t PartialRefreshCount() const { return partial_count_; }

  // Test seams: force the watchdog trip and inspect the bitmaps.
  void InjectControllerHangForTest() { controller_responsive_ = false; }
  void ClearControllerHangForTest() { controller_responsive_ = true; }
  bool IsControllerResponsiveForTest() const {
    return controller_responsive_;
  }

private:
  uint8_t last_full_[kEpdBufSize] = {};
  uint8_t last_partial_[kEpdBufSize] = {};
  uint32_t partial_count_ = 0;
  bool controller_responsive_ = true;
};

} // namespace tether::m5
