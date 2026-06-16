# spi_bus/CMakeLists.txt — ESP-IDF component CMake (plan.md §4.1).
#
# On real hardware we compile src/spi_bus.cpp, which wraps
# spi_bus_initialize() / spi_bus_add_device() from the ESP-IDF SPI driver.
# On host tests, we compile src/spi_bus_host.cpp (guarded by
# TETHER_M5_HOST_TEST) which provides the same public API against a fake
# bus.

if(DEFINED TETHER_M5_HOST_TEST)
  # Host build: build the *_host.cpp source against a fake driver. The
  # test_host/CMakeLists.txt calls `add_subdirectory(<comp>)` on us.
  add_library(spi_bus_host STATIC src/spi_bus_host.cpp)
  target_include_directories(spi_bus_host PUBLIC include)
  target_compile_definitions(spi_bus_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(spi_bus_host PUBLIC freertos_shim)
  target_include_directories(spi_bus_host PRIVATE ../board/include)
elseif(COMMAND idf_component_register)
  # Real ESP-IDF build.
  idf_component_register(
    SRCS "src/spi_bus.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver esp_littlefs RadioLib board
  )
else()
  # We are being included by something other than ESP-IDF or the host
  # test runner. Just no-op; the component is only meant to be built in
  # those two contexts.
  message(STATUS
    "spi_bus: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
