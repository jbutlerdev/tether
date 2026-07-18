// ssd1306.h — Tether display driver for the LilyGO T3-S3 MVSR's
// 0.96" 128×64 SSD1306 OLED (I2C).
//
// This is the MVSR counterpart of the M5's `epd` component
// (board.h::kDisplayKind == kOled). The MVSR has no e-paper; its
// display is an SSD1306 monochrome OLED on I2C1 (GPIO 17 SCL / 18
// SDA, address 0x3C — see board_t3s3_mvsr.h). Unlike the EPD, the
// OLED does not need partial-refresh accounting or ghosting
// management: every frame is a full rewrite over I2C.
//
// The public API mirrors the EPD controller's shape (Init, Clear,
// a framebuffer the renderer writes, Flush to push it to the
// panel) so the UI layer can treat both displays uniformly. The
// full 128×64 screen renderer (the MVSR counterpart of the M5's
// screens.cpp) is a follow-up; this component provides the
// controller, a boot/idle text renderer, and a host-testable
// framebuffer so the variant builds and the wiring is exercised.
//
// On the host build (TETHER_M5_HOST_TEST) the I2C calls are stubbed
// and the framebuffer is captured in memory so tests can assert on
// the pixels.

#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#ifdef TETHER_M5_HOST_TEST
#include "freertos_shim.h"
#else
#include "esp_err.h"
#endif

#include "board.h"

namespace tether::m5 {

// OLED geometry (SSD1306 128×64).
inline constexpr size_t kOledWidth = 128;
inline constexpr size_t kOledHeight = 64;
inline constexpr size_t kOledStride = kOledWidth / 8; // 16 bytes/row
inline constexpr size_t kOledBufSize = kOledStride * kOledHeight; // 1024 bytes

// Ssd1306 is the OLED controller. Construct with the I2C port/pins
// from board.h (defaults to the MVSR's Wire1 bus); call Init once,
// then write into the framebuffer and call Flush to push it to the
// panel.
class Ssd1306 {
public:
  Ssd1306() = default;
  ~Ssd1306() = default;

  Ssd1306(const Ssd1306 &) = delete;
  Ssd1306 &operator=(const Ssd1306 &) = delete;

  // Initialize the I2C bus and the SSD1306 panel. Returns true on
  // success. Safe to call once at app start. On the M5 build this
  // component is not compiled in (kDisplayKind == kEpd).
  bool Init();

  // Clear the framebuffer (all pixels off). Does NOT flush.
  void Clear();

  // Mutable framebuffer access for renderers. The buffer is
  // kOledBufSize bytes, 1 bit per pixel, MSB = leftmost, row-major
  // (page addressing mode: each byte is an 8-pixel column).
  uint8_t *Buffer() { return buf_; }
  const uint8_t *Buffer() const { return buf_; }

  // Push the framebuffer to the panel over I2C. Returns true on
  // success. On host this is a no-op (the buffer is the test
  // fixture).
  bool Flush();

  // Draw a single line of 8×8 text at (col, row) in the framebuffer
  // using the embedded font. col is in chars (0..15), row in chars
  // (0..7). Used by the boot/idle screen. Returns false if the
  // position is out of bounds.
  bool DrawText(int col, int row, const std::string &text);

  // Render the boot/idle screen: the board name + "Tether ready".
  // This is the MVSR's minimal "it works" screen; the full screen
  // set (idle / recording / queued / TX / TTS / settings) is a
  // follow-up that mirrors the M5's screens.cpp at 128×64.
  void RenderBootScreen();

  // Test seam: return a copy of the framebuffer for host assertions.
  // On target this is unused (tests run on the host build).
  std::string DumpBufferForTest() const;

private:
  uint8_t buf_[kOledBufSize] = {};
  bool inited_ = false;
};

} // namespace tether::m5
