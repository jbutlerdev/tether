# sd_card/CMakeLists.txt — SD card LittleFS mount (plan.md §4.3).
#
# On real hardware we compile src/sd_card.cpp, which wraps esp_littlefs
# and exposes /sdcard as a POSIX VFS. The host build compiles
# src/sd_card_host.cpp which provides the same API against a tmpfs
# rooted at a test-supplied directory.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(sd_card_host STATIC src/sd_card_host.cpp)
  target_include_directories(sd_card_host PUBLIC include)
  target_compile_definitions(sd_card_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/sd_card.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver esp_littlefs spi_bus
  )
else()
  message(STATUS
    "sd_card: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
