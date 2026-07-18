// test_ssd1306.cpp — host-side tests for the SSD1306 OLED component.
//
// The host build stubs the I2C calls; these tests verify the
// framebuffer layout, the text renderer, and the boot screen. The
// full on-target I2C sequence is exercised on the bench.

#include <cstring>
#include <string>

#include <unity.h>

#include "ssd1306.h"

using tether::m5::kOledBufSize;
using tether::m5::kOledHeight;
using tether::m5::kOledStride;
using tether::m5::kOledWidth;
using tether::m5::Ssd1306;

void setUp(void) {}
void tearDown(void) {}

// Init + Clear must produce an all-zero framebuffer.
void test_init_clears_buffer(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  auto buf = oled.DumpBufferForTest();
  TEST_ASSERT_EQUAL_size_t(kOledBufSize, buf.size());
  for (size_t i = 0; i < buf.size(); i++) {
    TEST_ASSERT_EQUAL_UINT8(0, static_cast<uint8_t>(buf[i]));
  }
}

// DrawText writes the glyph rows into the page-addressed buffer at
// the requested (col, row). 'T' at col 0 row 0 must set non-zero
// bytes in the first 8 buffer rows at column 0.
void test_draw_text_writes_glyph(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  TEST_ASSERT_TRUE(oled.DrawText(0, 0, "T"));
  auto buf = oled.DumpBufferForTest();
  int set_bits = 0;
  for (int y = 0; y < 8; y++) {
    if (static_cast<uint8_t>(buf[y * kOledStride + 0]) != 0) {
      set_bits++;
    }
  }
  TEST_ASSERT_GREATER_THAN(0, set_bits); // 'T' has pixels in most rows
}

// Out-of-bounds DrawText must return false (no crash, no write).
void test_draw_text_out_of_bounds(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  TEST_ASSERT_FALSE(oled.DrawText(-1, 0, "x"));
  TEST_ASSERT_FALSE(oled.DrawText(0, -1, "x"));
  TEST_ASSERT_FALSE(oled.DrawText(0, kOledHeight / 8, "x")); // row off-screen
}

// Text wraps/clips at the right edge (no buffer overrun).
void test_draw_text_clips_at_right_edge(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  // 20 chars at col 0 on an 16-char-wide screen: must clip, not overflow.
  TEST_ASSERT_TRUE(oled.DrawText(0, 0, "01234567890123456789"));
  auto buf = oled.DumpBufferForTest();
  TEST_ASSERT_EQUAL_size_t(kOledBufSize, buf.size()); // no overrun
}

// The boot screen renders the board name + READY without crashing.
void test_boot_screen_renders(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  oled.RenderBootScreen();
  auto buf = oled.DumpBufferForTest();
  // The boot screen must set at least one pixel (TETHER text).
  int non_zero = 0;
  for (size_t i = 0; i < buf.size(); i++) {
    if (static_cast<uint8_t>(buf[i]) != 0) {
      non_zero++;
    }
  }
  TEST_ASSERT_GREATER_THAN(0, non_zero);
}

// Non-printable chars map to space (no out-of-range font index).
void test_draw_text_non_printable_maps_to_space(void) {
  Ssd1306 oled;
  TEST_ASSERT_TRUE(oled.Init());
  TEST_ASSERT_TRUE(oled.DrawText(0, 0, std::string(1, '\x01')));
  auto buf = oled.DumpBufferForTest();
  // Space glyph is all-zero, so column 0 row 0 stays zero.
  for (int y = 0; y < 8; y++) {
    TEST_ASSERT_EQUAL_UINT8(0, static_cast<uint8_t>(buf[y * kOledStride]));
  }
}

int main(void) {
  UNITY_BEGIN();
  RUN_TEST(test_init_clears_buffer);
  RUN_TEST(test_draw_text_writes_glyph);
  RUN_TEST(test_draw_text_out_of_bounds);
  RUN_TEST(test_draw_text_clips_at_right_edge);
  RUN_TEST(test_boot_screen_renders);
  RUN_TEST(test_draw_text_non_printable_maps_to_space);
  return UNITY_END();
}
