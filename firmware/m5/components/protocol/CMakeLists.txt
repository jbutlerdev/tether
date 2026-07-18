# protocol/CMakeLists.txt — Tether M5 on-target wire-format codec
# (research.md §8.1 / §8.6).
#
# Pure C++17 with no ESP-IDF or FreeRTOS deps, so the same source builds
# for both the on-target firmware and the host-side unit tests.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(protocol_host STATIC src/protocol.cpp)
  target_include_directories(protocol_host PUBLIC include)
  target_compile_definitions(protocol_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_compile_features(protocol_host PUBLIC cxx_std_20)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/protocol.cpp"
    INCLUDE_DIRS "include"
  )
else()
  message(STATUS "protocol: skipped")
endif()
