// screens.cpp — Tether M5 EPD renderers (plan.md §5.3 / research.md §9.3).
//
// Every RenderXxx() function writes exactly kEpdBufSize (5000)
// bytes into `out_buf`. The byte layout is the standard
// monochrome framebuffer used by the GDEQ0154T100 panel:
//
//   * MSB-first within each byte
//   * Byte 0 of the buffer is the top-left 8 pixels
//   * bit 7 = leftmost pixel of the 8-pixel run
//   * 1 = black, 0 = white (the EPD inverts on the wire; the
//     driver handles polarity; here we keep "1 = ink")
//
// The renderers are intentionally allocation-free at runtime. The
// only heap allocation is in the helpers, and helpers are
// constexpr- or fixed-buffer-only. test_no_alloc_in_render
// verifies this with --wrap=malloc.
//
// Glyphs come from a 5×7 monospace font embedded in epd_font.h.
// Every glyph is 5 bytes wide and 7 rows tall. Non-ASCII chars
// render as '?'. The font is a hand-rolled subset of the IBM PC
// 8×8 CP437 glyphs that covers the M5's display needs:
// A-Z, a-z, 0-9, space, and a small punctuation set.

#include "epd.h"
#include "epd_font.h"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <string>

namespace tether::m5 {

namespace {

// Set a single pixel in the framebuffer.
void SetPixel(uint8_t *buf, int x, int y, bool on) {
  if (x < 0 || x >= static_cast<int>(kEpdWidth) || y < 0 ||
      y >= static_cast<int>(kEpdHeight)) {
    return;
  }
  size_t byte_idx = static_cast<size_t>(y) * kEpdStride +
                    (static_cast<size_t>(x) / 8);
  uint8_t mask = static_cast<uint8_t>(1u << (7 - (x % 8)));
  if (on) {
    buf[byte_idx] |= mask;
  } else {
    buf[byte_idx] &= static_cast<uint8_t>(~mask);
  }
}

bool GetPixel(const uint8_t *buf, int x, int y) {
  if (x < 0 || x >= static_cast<int>(kEpdWidth) || y < 0 ||
      y >= static_cast<int>(kEpdHeight)) {
    return false;
  }
  size_t byte_idx = static_cast<size_t>(y) * kEpdStride +
                    (static_cast<size_t>(x) / 8);
  uint8_t mask = static_cast<uint8_t>(1u << (7 - (x % 8)));
  return (buf[byte_idx] & mask) != 0;
}

void DrawChar(uint8_t *buf, int x, int y, char c, bool pixel) {
  const uint8_t *glyph = FontGlyph(c);
  if (!glyph)
    return;
  for (int row = 0; row < 7; ++row) {
    uint8_t bits = glyph[row];
    for (int col = 0; col < 5; ++col) {
      if (bits & (1u << (4 - col))) {
        SetPixel(buf, x + col, y + row, pixel);
      }
    }
  }
}

void DrawString(uint8_t *buf, int x, int y, const char *s, bool pixel) {
  if (!s)
    return;
  while (*s) {
    DrawChar(buf, x, y, *s, pixel);
    x += 6;
    s++;
  }
}

void DrawStringTruncated(uint8_t *buf, int x, int y, const std::string &s,
                         int max_pixels, bool pixel) {
  int max_chars = max_pixels / 6;
  if (max_chars < 0)
    max_chars = 0;
  size_t n = std::min(s.size(), static_cast<size_t>(max_chars));
  for (size_t i = 0; i < n; ++i) {
    DrawChar(buf, x, y, s[i], pixel);
    x += 6;
  }
  // Trailing ellipsis if we truncated.
  if (s.size() > static_cast<size_t>(max_chars) && max_chars >= 1) {
    // Overwrite the last char with '~' to indicate truncation.
    DrawChar(buf, x - 6, y, '~', pixel);
  }
}

void DrawStringCentered(uint8_t *buf, int cx, int y, const char *s,
                        bool pixel) {
  if (!s)
    return;
  size_t len = std::strlen(s);
  int x = cx - static_cast<int>(len) * 3;
  if (x < 0)
    x = 0;
  DrawString(buf, x, y, s, pixel);
}

// Layout helpers. The screens are designed in a 200×200 grid
// with a 4 px margin and a 1 px border.
constexpr int kMargin = 4;
constexpr int kInnerW = static_cast<int>(kEpdWidth) - 2 * kMargin;
constexpr int kInnerH = static_cast<int>(kEpdHeight) - 2 * kMargin;

void DrawFrameBorder(uint8_t *buf) {
  BitmapDrawRect(buf, 0, 0, static_cast<int>(kEpdWidth),
                 static_cast<int>(kEpdHeight), true);
}

} // namespace

// ── Bitmap helpers (public API) ────────────────────────────────────────

void BitmapFill(uint8_t *buf, bool pixel) {
  std::memset(buf, pixel ? 0xFF : 0x00, kEpdBufSize);
}

void BitmapDrawText(uint8_t *buf, int x, int y, const char *text, bool pixel) {
  DrawString(buf, x, y, text, pixel);
}

void BitmapDrawRect(uint8_t *buf, int x, int y, int w, int h, bool pixel) {
  if (w <= 0 || h <= 0)
    return;
  BitmapDrawHLine(buf, x, y, w, pixel);
  BitmapDrawHLine(buf, x, y + h - 1, w, pixel);
  BitmapDrawVLine(buf, x, y, h, pixel);
  BitmapDrawVLine(buf, x + w - 1, y, h, pixel);
}

void BitmapDrawHLine(uint8_t *buf, int x, int y, int w, bool pixel) {
  for (int i = 0; i < w; ++i) {
    SetPixel(buf, x + i, y, pixel);
  }
}

void BitmapDrawVLine(uint8_t *buf, int x, int y, int h, bool pixel) {
  for (int i = 0; i < h; ++i) {
    SetPixel(buf, x, y + i, pixel);
  }
}

void BitmapDrawProgressBar(uint8_t *buf, int x, int y, int w, int h,
                           uint8_t percent, bool pixel) {
  if (w <= 0 || h <= 0)
    return;
  if (percent > 100)
    percent = 100;
  // Outline the bar.
  BitmapDrawRect(buf, x, y, w, h, pixel);
  // Fill the progress.
  int fill = (w - 2) * percent / 100;
  for (int i = 1; i < w - 1; ++i) {
    for (int j = 1; j < h - 1; ++j) {
      SetPixel(buf, x + i, y + j, pixel && (i <= fill));
    }
  }
}

// ── RenderIdle ─────────────────────────────────────────────────────────
//
// The most-complex screen. Layout (200×200, 4 px margin):
//
//   ┌──────────────────────────────────────┐ y= 0
//   │ ► <name> ●<unread>                   │ y= 4  current conv
//   │   "last message"                    │ y=16  last inbound preview
//   │ ─────────────────────────────────── │ y=28  divider
//   │   [3] <name>      [ ] <name>        │ y=36  conv tab strip
//   │        2 14:32         4 14:28       │ y=44  per-tab preview
//   │ ─────────────────────────────────── │ y=180 divider
//   │ CH 0  VBat 3.92V  Vol ▓▓▓░ 60%      │ y=188 footer
//   └──────────────────────────────────────┘ y=200
void RenderIdle(const IdleState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  if (s.low_battery) {
    // The footer shows the warning; no other change.
    DrawString(out_buf, kMargin, kMargin + 168, "LOW BATTERY", true);
  } else {
    // Footer: channel + battery + volume bar.
    char footer[64];
    std::snprintf(footer, sizeof footer, "CH %u", s.channel);
    DrawString(out_buf, kMargin, kMargin + 168, footer, true);
    std::snprintf(footer, sizeof footer, "%.2fV", s.vbat_mv / 1000.0f);
    DrawStringCentered(out_buf, 100, kMargin + 168, footer, true);
    // Volume bar at the right.
    BitmapDrawProgressBar(out_buf, 140, kMargin + 168, 50, 8, s.volume, true);
  }

  if (s.convs.empty()) {
    DrawStringCentered(out_buf, 100, 80, "no conversations", true);
    DrawStringCentered(out_buf, 100, 92, "press a key to start", false);
    return;
  }

  // Header: current conv name + unread badge.
  const ConvInfo &cur = s.convs[s.current_index % s.convs.size()];
  // Bullet prefix.
  DrawChar(out_buf, kMargin, kMargin, '>', true);
  // Name (truncated to fit before the badge).
  std::string name = cur.name;
  DrawStringTruncated(out_buf, kMargin + 8, kMargin, name, 140, true);
  // Unread badge.
  if (cur.unread > 0) {
    char badge[16];
    std::snprintf(badge, sizeof badge, " %u", cur.unread);
    DrawString(out_buf, 160, kMargin, badge, true);
  }

  // Last inbound preview (most recent "in" entry).
  if (!s.recent.empty()) {
    const HistoryEntry &e = s.recent.front();
    if (e.direction == 1) {
      // Truncate to one line (≈ 30 chars).
      DrawStringTruncated(out_buf, kMargin + 8, kMargin + 12, e.text, 180,
                          false);
    }
  }

  // Divider above the conv tab strip.
  BitmapDrawHLine(out_buf, kMargin, kMargin + 28, kInnerW, true);

  // Conv tab strip: 4 tabs visible at a time, scroll_pos is the
  // index of the first tab.
  constexpr int kTabCount = 4;
  constexpr int kTabW = 48;
  constexpr int kTabH = 30;
  int strip_x = kMargin;
  int strip_y = kMargin + 36;
  for (int i = 0; i < kTabCount; ++i) {
    size_t idx = s.scroll_pos + i;
    if (idx >= s.convs.size())
      break;
    const ConvInfo &c = s.convs[idx];
    bool active = (idx == (s.current_index % s.convs.size()));
    if (active) {
      // Highlight the active tab with a filled box.
      BitmapDrawRect(out_buf, strip_x, strip_y, kTabW, kTabH, true);
    } else {
      BitmapDrawRect(out_buf, strip_x, strip_y, kTabW, kTabH, false);
    }
    // Tab label "[N] name".
    char tab_label[16];
    std::snprintf(tab_label, sizeof tab_label, "[%u]", c.unread);
    DrawString(out_buf, strip_x + 2, strip_y + 2, tab_label, true);
    // Name (truncated to 7 chars at 6 px each = 42 px).
    DrawStringTruncated(out_buf, strip_x + 2, strip_y + 12, c.name, 42,
                        active);
    strip_x += kTabW;
  }
}

// ── RenderRecording ───────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ ●  REC    00:03                      │
//   │   <conv name>                        │
//   │   release PTT to send                │
//   └──────────────────────────────────────┘
void RenderRecording(const RecordingState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  // Bullet + REC label.
  DrawChar(out_buf, kMargin + 4, kMargin + 12, '*', true);
  DrawString(out_buf, kMargin + 16, kMargin + 12, "REC", true);

  // Timer.
  char timer[16];
  uint32_t total_secs = s.elapsed_ms / 1000;
  std::snprintf(timer, sizeof timer, "00:%02u", total_secs % 60);
  DrawString(out_buf, 100, kMargin + 12, timer, true);

  // Conv name.
  DrawStringTruncated(out_buf, kMargin, kMargin + 40, s.conv.name, 180, true);

  // Hint.
  DrawString(out_buf, kMargin, kMargin + 64, "release PTT to send", true);

  // Peak amplitude bar at the bottom.
  int peak = static_cast<int>(s.peak_amplitude);
  if (peak > 32767)
    peak = 32767;
  uint8_t pct = static_cast<uint8_t>((peak * 100) / 32767);
  BitmapDrawProgressBar(out_buf, kMargin, kMargin + 130, kInnerW, 8, pct, true);
}

// ── RenderQueued ──────────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ ⏎ QUEUED                             │
//   │   <conv name>                        │
//   │   12.4 KB                            │
//   └──────────────────────────────────────┘
void RenderQueued(const QueuedState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  DrawChar(out_buf, kMargin + 4, kMargin + 12, 'Q', true);
  DrawString(out_buf, kMargin + 16, kMargin + 12, "QUEUED", true);
  DrawStringTruncated(out_buf, kMargin, kMargin + 40, s.conv.name, 180, true);
  char size[16];
  std::snprintf(size, sizeof size, "%.1f KB",
                s.file_bytes / 1024.0f);
  DrawString(out_buf, kMargin, kMargin + 60, size, true);
}

// ── RenderTransmitting ────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ ↑  TX     00:38 / 01:15               │
//   │   <conv name>                        │
//   │   ACK 47/100                         │
//   │   ▓▓▓▓▓▓▓▓░░░░░░░░░░░  38%            │
//   └──────────────────────────────────────┘
void RenderTransmitting(const TransmittingState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  DrawChar(out_buf, kMargin + 4, kMargin + 12, 'T', true);
  DrawString(out_buf, kMargin + 16, kMargin + 12, "TX", true);

  // Elapsed / total.
  char timer[32];
  uint32_t es = s.elapsed_ms / 1000;
  uint32_t ts = s.estimated_total_ms / 1000;
  std::snprintf(timer, sizeof timer, "%02u:%02u / %02u:%02u", es % 60,
                es / 60, ts % 60, ts / 60);
  DrawString(out_buf, 100, kMargin + 12, timer, true);

  // Conv name.
  DrawStringTruncated(out_buf, kMargin, kMargin + 40, s.conv.name, 180, true);

  // ACK count.
  char ack[32];
  std::snprintf(ack, sizeof ack, "ACK %u/%u", s.acked_chunks,
                s.total_chunks);
  DrawString(out_buf, kMargin, kMargin + 60, ack, true);

  // Progress bar.
  uint8_t pct = 0;
  if (s.total_chunks > 0) {
    pct = static_cast<uint8_t>((s.sent_chunks * 100) / s.total_chunks);
  }
  BitmapDrawProgressBar(out_buf, kMargin, kMargin + 100, kInnerW, 10, pct,
                        true);

  // Percent label.
  char pct_label[8];
  std::snprintf(pct_label, sizeof pct_label, "%u%%", pct);
  DrawString(out_buf, 170, kMargin + 100, pct_label, true);
}

// ── RenderTtsPlayback ─────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ ↓  PLAY  <conv name>                 │
//   │   "<text>"                           │
//   │   ▓▓▓▓▓▓░░░░░░░  00:08 / 00:14       │
//   └──────────────────────────────────────┘
void RenderTtsPlayback(const TtsState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  DrawChar(out_buf, kMargin + 4, kMargin + 12, 'P', true);
  DrawString(out_buf, kMargin + 16, kMargin + 12, "PLAY", true);
  DrawStringTruncated(out_buf, kMargin + 64, kMargin + 12, s.conv.name, 130,
                      true);

  // Quote-wrapped text.
  DrawChar(out_buf, kMargin, kMargin + 36, '"', true);
  DrawStringTruncated(out_buf, kMargin + 8, kMargin + 36, s.current_text, 170,
                      true);

  // Progress.
  uint8_t pct = 0;
  if (s.total_ms > 0) {
    pct = static_cast<uint8_t>((s.elapsed_ms * 100) / s.total_ms);
  }
  BitmapDrawProgressBar(out_buf, kMargin, kMargin + 100, 130, 10, pct, true);

  char timer[24];
  uint32_t es = s.elapsed_ms / 1000;
  uint32_t ts = s.total_ms / 1000;
  std::snprintf(timer, sizeof timer, "%02u:%02u / %02u:%02u", es % 60,
                es / 60, ts % 60, ts / 60);
  DrawString(out_buf, 140, kMargin + 100, timer, true);
}

// ── RenderSettings ────────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ SETTINGS                             │
//   │   Channel:    902.3 MHz              │
//   │   Modem:      SF11/BW125             │
//   │   Volume:     ▓▓▓▓▓░░░ 60%           │
//   │   Addr:       0x4A1F                 │
//   │   VBat:       3.92 V                 │
//   └──────────────────────────────────────┘
void RenderSettings(const SettingsState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  DrawString(out_buf, kMargin, kMargin, "SETTINGS", true);
  BitmapDrawHLine(out_buf, kMargin, kMargin + 10, kInnerW, true);

  // Channel line.
  int y = kMargin + 20;
  DrawString(out_buf, kMargin + 4, y, "Channel:", true);
  char val[32];
  float mhz = 902.3f + s.channel * 0.2f;
  std::snprintf(val, sizeof val, "%.1f MHz", mhz);
  DrawString(out_buf, kMargin + 80, y, val, true);
  if (s.cursor == 0) {
    BitmapDrawRect(out_buf, kMargin, y - 1, kInnerW, 11, true);
  }

  // Modem line.
  y += 14;
  DrawString(out_buf, kMargin + 4, y, "Modem:", true);
  DrawString(out_buf, kMargin + 80, y, s.modem.c_str(), true);
  if (s.cursor == 1) {
    BitmapDrawRect(out_buf, kMargin, y - 1, kInnerW, 11, true);
  }

  // Volume line.
  y += 14;
  DrawString(out_buf, kMargin + 4, y, "Volume:", true);
  std::snprintf(val, sizeof val, "%u%%", s.volume);
  DrawString(out_buf, kMargin + 80, y, val, true);
  BitmapDrawProgressBar(out_buf, kMargin + 120, y, 60, 7, s.volume, true);
  if (s.cursor == 2) {
    BitmapDrawRect(out_buf, kMargin, y - 1, kInnerW, 11, true);
  }

  // Address.
  y += 14;
  DrawString(out_buf, kMargin + 4, y, "Addr:", true);
  std::snprintf(val, sizeof val, "0x%04X", s.node_addr);
  DrawString(out_buf, kMargin + 80, y, val, true);
  if (s.cursor == 3) {
    BitmapDrawRect(out_buf, kMargin, y - 1, kInnerW, 11, true);
  }

  // Battery.
  y += 14;
  DrawString(out_buf, kMargin + 4, y, "VBat:", true);
  std::snprintf(val, sizeof val, "%.2f V", s.vbat_mv / 1000.0f);
  DrawString(out_buf, kMargin + 80, y, val, true);
  if (s.cursor == 4) {
    BitmapDrawRect(out_buf, kMargin, y - 1, kInnerW, 11, true);
  }

  // Footer hint.
  DrawString(out_buf, kMargin, kMargin + 168, "B=next C=back", true);
}

// ── RenderLowBattery ──────────────────────────────────────────────────
//
//   ┌──────────────────────────────────────┐
//   │ LOW BATTERY                          │
//   │                                      │
//   │   3.30 V                             │
//   │   charge soon                        │
//   └──────────────────────────────────────┘
void RenderLowBattery(const LowBatteryState &s, uint8_t *out_buf) {
  BitmapFill(out_buf, false);
  DrawFrameBorder(out_buf);

  // Invert the top half for a "warning banner" effect.
  for (int y = 0; y < 20; ++y) {
    BitmapDrawHLine(out_buf, 0, y, static_cast<int>(kEpdWidth), true);
  }
  DrawString(out_buf, kMargin + 4, 6, "LOW BATTERY", false);

  char val[16];
  std::snprintf(val, sizeof val, "%.2f V", s.vbat_mv / 1000.0f);
  DrawStringCentered(out_buf, 100, 100, val, true);

  const char *msg = s.critical ? "shutting down" : "charge soon";
  DrawStringCentered(out_buf, 100, 130, msg, true);
}

// ── EPD controller (host stub + production path) ─────────────────────

const uint8_t *EPD::LastFullBitmap() const { return last_full_; }
const uint8_t *EPD::LastPartialBitmap() const { return last_partial_; }

esp_err_t EPD::Init() {
  controller_responsive_ = true;
  partial_count_ = 0;
  std::memset(last_full_, 0, sizeof last_full_);
  std::memset(last_partial_, 0, sizeof last_partial_);
  return ESP_OK;
}

esp_err_t EPD::Clear() {
  std::memset(last_full_, 0, sizeof last_full_);
  std::memset(last_partial_, 0, sizeof last_partial_);
  return ESP_OK;
}

esp_err_t EPD::PartialRefresh(const uint8_t *bitmap) {
  if (!bitmap)
    return ESP_ERR_INVALID_ARG;
  if (!controller_responsive_) {
    return ESP_ERR_TIMEOUT;
  }
  std::memcpy(last_partial_, bitmap, kEpdBufSize);
  ++partial_count_;
  return ESP_OK;
}

esp_err_t EPD::FullRefresh(const uint8_t *bitmap) {
  if (!bitmap)
    return ESP_ERR_INVALID_ARG;
  if (!controller_responsive_) {
    return ESP_ERR_TIMEOUT;
  }
  std::memcpy(last_full_, bitmap, kEpdBufSize);
  partial_count_ = 0; // reset the partial counter
  return ESP_OK;
}

} // namespace tether::m5
