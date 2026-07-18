# ssd1306/CMakeLists.txt — Tether MVSR OLED display component.
#
# The SSD1306 128×64 OLED is the MVSR's display (board.h
# kDisplayKind == kOled). Pure C++17; the host build stubs the I2C
# calls and captures the framebuffer for tests.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(ssd1306_host STATIC src/ssd1306.cpp)
  target_include_directories(ssd1306_host PUBLIC include)
  # board.h is a header-only component (no board_host library); pull
  # its include dir directly so ssd1306.cpp can resolve board::kPin*.
  target_include_directories(ssd1306_host PRIVATE
      ${CMAKE_CURRENT_SOURCE_DIR}/../board/include)
  target_compile_definitions(ssd1306_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_compile_features(ssd1306_host PUBLIC cxx_std_20)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/ssd1306.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver board
  )
else()
  message(STATUS "ssd1306: skipped")
endif()
