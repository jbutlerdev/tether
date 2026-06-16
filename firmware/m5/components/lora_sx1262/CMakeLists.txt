# lora_sx1262/CMakeLists.txt — SX1262 LoRa driver (plan.md §4.2).
#
# On real hardware we compile src/lora_sx1262.cpp, which wraps
# RadioLib::SX1262 and takes the SPI bus mutex on every transaction
# (research.md §7.4). The host build compiles the same backend logic
# in src/lora_sx1262_host.cpp but with MockRadioBackend.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(lora_sx1262_host STATIC src/lora_sx1262_host.cpp)
  target_include_directories(lora_sx1262_host PUBLIC include)
  target_compile_definitions(lora_sx1262_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(lora_sx1262_host PUBLIC freertos_shim spi_bus_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/lora_sx1262.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver spi_bus RadioLib
  )
else()
  message(STATUS
    "lora_sx1262: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
