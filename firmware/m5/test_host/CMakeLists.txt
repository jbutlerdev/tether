# test_host/CMakeLists.txt — host-side test runner for the M5 firmware
# components (plan.md §0.2, §0.3).
#
# Mirrors the PlatformIO native env used for the bridge. Each test_*.cpp
# links against a small "host" translation of the component (guarded by
# TETHER_M5_HOST_TEST) and the bundled Unity framework.
#
# Build all tests:       cmake -S . -B build && cmake --build build
# Run all tests:         ctest --test-dir build --output-on-failure
# Coverage:              cmake --build build --target coverage

cmake_minimum_required(VERSION 3.16)
project(tether_m5_host_tests C CXX)

# CTest is required for add_test() to register.
enable_testing()
include(CTest)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Debug)
endif()

# Every test binary needs C++20, coverage, and the host-test macro.
add_library(tether_m5_host_iface INTERFACE)
target_include_directories(tether_m5_host_iface INTERFACE
  ${CMAKE_CURRENT_SOURCE_DIR}/..
)
target_compile_options(tether_m5_host_iface INTERFACE
  -Wall -Wextra -Wpedantic -Werror
  -DTETHER_M5_HOST_TEST=1
)
target_compile_definitions(tether_m5_host_iface INTERFACE
  TETHER_M5_HOST_TEST=1
)

# Bundle Unity (the test framework used by ESP-IDF and PlatformIO) and a
# thin FreeRTOS shim that backs xTaskHandle / xSemaphoreCreateMutex with
# std::mutex and std::condition_variable. The shim lives in
# test_host/freertos_shim/ and is used by every component's host build.
set(TETHER_M5_HOST_TEST 1)
include_directories(${CMAKE_CURRENT_SOURCE_DIR}/freertos_shim)
add_subdirectory(freertos_shim)
add_subdirectory(unity)

# Each component contributes a CMakeLists.txt that adds its test binary
# via `add_host_test`. We list them explicitly so a missing component
# fails the configure step instead of silently producing zero tests.

# `add_host_test(<name>)` — define a test binary for the named component.
# The component is expected to expose:
#   components/<name>/include/<name>.h
#   components/<name>/test/test_<name>.cpp
#   components/<name>/src/<name>_host.cpp
# `add_host_test` produces one ctest target.
function(add_host_test name)
  set(comp_dir ${CMAKE_CURRENT_SOURCE_DIR}/../components/${name})
  if(NOT EXISTS ${comp_dir})
    message(FATAL_ERROR "add_host_test: missing component ${name}")
  endif()
  # Build the host-side production source into a static library the test
  # binary links against. The component's CMakeLists.txt detects the
  # TETHER_M5_HOST_TEST variable and produces <name>_host for us.
  if(EXISTS ${comp_dir}/CMakeLists.txt)
    add_subdirectory(${comp_dir} ${CMAKE_BINARY_DIR}/components/${name})
    # Apply the host coverage flags to the static library so
    # gcov can record per-line coverage of the production source
    # when the test binary runs.
    if(TARGET ${name}_host)
      target_compile_options(${name}_host PRIVATE
        --coverage -fprofile-arcs -ftest-coverage
      )
    endif()
  endif()
  # The test source is conventionally test_<name>.cpp but the plan
  # uses a few suffixed variants: test_<name>_state.cpp (PTT),
  # test_<name>_render.cpp (EPD). Look for any of them, and
  # collect all matching files into a single test binary so
  # components with multiple test_*.cpp files (e.g. watchdog's
  # plan §9.2 reset-reason tests) compile and run together.
  set(test_srcs "")
  foreach(candidate
    ${comp_dir}/test/test_${name}.cpp
    ${comp_dir}/test/test_${name}_state.cpp
    ${comp_dir}/test/test_${name}_render.cpp
    ${comp_dir}/test/test_${name}_reset.cpp)
    if(EXISTS ${candidate})
      list(APPEND test_srcs ${candidate})
    endif()
  endforeach()
  if(NOT test_srcs)
    message(FATAL_ERROR "add_host_test(${name}): no test source found")
  endif()
  add_executable(test_${name} ${test_srcs})
  target_include_directories(test_${name} PRIVATE
    ${comp_dir}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/freertos_shim
    ${CMAKE_CURRENT_SOURCE_DIR}/unity
    ${CMAKE_CURRENT_SOURCE_DIR}/../components
  )
  target_compile_definitions(test_${name} PRIVATE
    TETHER_M5_HOST_TEST=1
    TESTDATA_DIR=\"${comp_dir}/testdata/screens\"
  )
  target_compile_options(test_${name} PRIVATE
    --coverage -fprofile-arcs -ftest-coverage
  )
  target_link_libraries(test_${name} PRIVATE
    tether_m5_host_iface
    unity_host
    freertos_shim
    ${name}_host
    gcov
  )
  add_test(NAME ${name} COMMAND test_${name})
  add_coverage_target(${name} test_${name})
endfunction()

# `add_coverage_target` — registers a per-component `coverage_<name>` target
# that runs gcov + lcov and prints the line-coverage summary.
function(add_coverage_target name target)
  if(NOT TARGET coverage_${name})
    add_custom_target(coverage_${name}
      COMMAND ${CMAKE_COMMAND} -E echo
              "Running coverage for ${name}..."
      COMMAND mkdir -p ${CMAKE_BINARY_DIR}/coverage/${name}
      COMMAND gcov
              -o ${CMAKE_BINARY_DIR}/CMakeFiles/${target}.dir/${name}
              -l -p -m
              ${comp_dir}/src/*.cpp
      WORKING_DIRECTORY ${CMAKE_BINARY_DIR}/coverage/${name}
      DEPENDS ${target}
      COMMENT "Coverage report for ${name}"
    )
  endif()
endfunction()

add_host_test(spi_bus)
add_host_test(lora_sx1262)
add_host_test(sd_card)
add_host_test(psram_ring)
add_host_test(opus_enc)
add_host_test(i2s_mic)
add_host_test(i2s_amp)
add_host_test(buttons)
add_host_test(ptt)
add_host_test(audio_capture)
add_host_test(storage_flush)
add_host_test(radio_task)
add_host_test(ui_state)
add_host_test(watchdog)
add_host_test(power_mgmt)
add_host_test(littlefs_vfs)
add_host_test(conv_db)
add_host_test(epd)
add_host_test(conv_manager)
add_host_test(aes_link)

# gen_epd_goldens — auxiliary tool that writes the EPD golden
# fixtures. Not a test; not registered with ctest. Run by hand
# after a deliberate renderer change to refresh the testdata.
add_executable(gen_epd_goldens
  ${CMAKE_CURRENT_SOURCE_DIR}/../components/epd/test/gen_epd_goldens.cpp
)
target_include_directories(gen_epd_goldens PRIVATE
  ${CMAKE_CURRENT_SOURCE_DIR}/../components/epd/include
  ${CMAKE_CURRENT_SOURCE_DIR}/../components
  ${CMAKE_CURRENT_SOURCE_DIR}/freertos_shim
  ${CMAKE_CURRENT_SOURCE_DIR}/unity
)
target_compile_definitions(gen_epd_goldens PRIVATE
  TETHER_M5_HOST_TEST=1
)
target_compile_options(gen_epd_goldens PRIVATE
  --coverage -fprofile-arcs -ftest-coverage
)
target_link_libraries(gen_epd_goldens PRIVATE
  tether_m5_host_iface
  unity_host
  freertos_shim
  epd_host
  conv_db_host
  littlefs_vfs_host
  gcov
)

# Phase 3 exit-gate integration test (plan.md §4.10). It exercises
# every task component under synthetic load and verifies no
# deadlocks. The smoke test lives in firmware/m5/test/test_smoke.cpp
# and pulls in every host library.
add_executable(test_smoke
  ${CMAKE_CURRENT_SOURCE_DIR}/../test/test_smoke.cpp
)
target_include_directories(test_smoke PRIVATE
  ${CMAKE_CURRENT_SOURCE_DIR}/../components
)
target_compile_definitions(test_smoke PRIVATE
  TETHER_M5_HOST_TEST=1
)
target_compile_options(test_smoke PRIVATE
  --coverage -fprofile-arcs -ftest-coverage
)
target_link_libraries(test_smoke PRIVATE
  tether_m5_host_iface
  unity_host
  freertos_shim
  spi_bus_host
  lora_sx1262_host
  sd_card_host
  psram_ring_host
  opus_enc_host
  i2s_mic_host
  i2s_amp_host
  buttons_host
  ptt_host
  audio_capture_host
  storage_flush_host
  radio_task_host
  ui_state_host
  watchdog_host
  power_mgmt_host
  conv_manager_host
  gcov
)
add_test(NAME smoke COMMAND test_smoke)