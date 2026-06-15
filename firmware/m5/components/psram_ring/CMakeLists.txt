# psram_ring/CMakeLists.txt — SPSC ring buffer (plan.md §4.4).
#
# The same src/psram_ring.cpp is used in both the host and firmware
# builds. On host, TETHER_M5_HOST_TEST is defined and we use new[] for
# the buffer; on real hardware we use heap_caps_malloc with
# MALLOC_CAP_SPIRAM so the ring lives in PSRAM.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(psram_ring_host STATIC src/psram_ring.cpp)
  target_include_directories(psram_ring_host PUBLIC include)
  target_compile_definitions(psram_ring_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/psram_ring.cpp"
    INCLUDE_DIRS "include"
    REQUIRES esp_psram
  )
else()
  message(STATUS
    "psram_ring: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
