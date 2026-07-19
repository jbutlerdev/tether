# opus_dec/CMakeLists.txt — Opus decoder (mirrors opus_enc).
#
# On real hardware we compile src/opus_dec.cpp, which wraps the
# micro_opus component's opus_decode. The host build compiles the same
# source but with TETHER_M5_HOST_TEST defined and links the system
# libopus instead.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(opus_dec_host STATIC src/opus_dec.cpp)
  target_include_directories(opus_dec_host PUBLIC include)
  target_compile_definitions(opus_dec_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(opus_dec_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/opus_dec.cpp"
    INCLUDE_DIRS "include"
    REQUIRES micro_opus
  )
else()
  message(STATUS
    "opus_dec: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
