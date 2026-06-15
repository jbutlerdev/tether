# opus_enc/CMakeLists.txt — Opus encoder wrapper (plan.md §4.5).
#
# On host we link against system libopus (libopus-dev). On real
# hardware we depend on the ESP-IDF-managed esphome/micro-opus or
# esp-libopus. The public API is the same in both cases.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(opus_enc_host STATIC src/opus_enc.cpp)
  target_include_directories(opus_enc_host PUBLIC include)
  target_compile_definitions(opus_enc_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(opus_enc_host PUBLIC opus)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/opus_enc.cpp"
    INCLUDE_DIRS "include"
    REQUIRES micro_opus
  )
else()
  message(STATUS
    "opus_enc: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
