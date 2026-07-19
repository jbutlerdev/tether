# main/CMakeLists.txt — the M5 firmware main component. Lists the
# entry-point source and its dependencies. The display + I/O-expander
# dependencies are variant-specific: the M5 uses the epd + pca9557
# components, the MVSR uses the ssd1306 component. See board/Kconfig
# (CONFIG_TETHER_BOARD_M5 / CONFIG_TETHER_BOARD_T3S3_MVSR).

# Common components required by both variants.
set(COMMON_REQS
  board i2s_amp i2s_mic audio_capture buttons conv_db
  conv_manager lora_sx1262 opus_enc opus_dec power_mgmt protocol
  psram_ring ptt radio_task sd_card spi_bus storage_flush ui_state
  watchdog nvs_flash
)

# Variant-specific display + I/O-expander components. All three are
# always required so their include paths are available to main.cpp;
# the #if defined(CONFIG_TETHER_BOARD_*) guards in main.cpp select
# which is actually used at compile time. (We can't gate REQUIRES on
# CONFIG_TETHER_BOARD_* because the Kconfig choice may not be
# available as a CMake variable when this file is processed — the
# sdkconfig.h define for the C++ preprocessor and the CMake variable
# can be out of sync during set-target.)
idf_component_register(
  SRCS "main.cpp"
  INCLUDE_DIRS "."
  REQUIRES ${COMMON_REQS} ssd1306 epd pca9557
)
