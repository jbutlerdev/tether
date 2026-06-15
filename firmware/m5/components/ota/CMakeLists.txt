# ota/CMakeLists.txt — Tether M5 USB OTA update path (plan §9.4).
#
# The OTA image itself arrives over USB via the ESP32-S3 ROM
# bootloader (`idf.py -p /dev/ttyUSB0 flash`); this component
# adds the firmware-side post-processing:
#
#   * SHA-256 verification of the streamed image
#   * State machine: kIdle -> kWriting -> kReady
#   * Rollback hook the conv manager calls on a boot loop
#
# On real hardware the partition write routes through
# esp_partition_* / esp_ota_*. On the host build it uses an
# in-memory vector so the unit tests can exercise the state
# machine without flashing a real ESP32.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(ota_host STATIC src/ota.cpp)
  target_include_directories(ota_host PUBLIC include)
  target_compile_definitions(ota_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(ota_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/ota.cpp"
    INCLUDE_DIRS "include"
    REQUIRES mbedtls app_update
  )
else()
  message(STATUS "ota: skipped")
endif()
