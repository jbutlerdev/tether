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
  endif()
  add_executable(test_${name}
    ${comp_dir}/test/test_${name}.cpp
  )
  target_include_directories(test_${name} PRIVATE
    ${comp_dir}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/freertos_shim
    ${CMAKE_CURRENT_SOURCE_DIR}/unity
    ${CMAKE_CURRENT_SOURCE_DIR}/../components
  )
  target_compile_definitions(test_${name} PRIVATE
    TETHER_M5_HOST_TEST=1
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
