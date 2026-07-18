# main/CMakeLists.txt — the M5 firmware main component. Lists the
# entry-point source and its dependencies. The display + I/O-expander
# dependencies are variant-specific: the M5 uses the epd + pca9557
# components, the MVSR uses the ssd1306 component. See board/Kconfig
# (CONFIG_TETHER_BOARD_M5 / CONFIG_TETHER_BOARD_T3S3_MVSR).

# Common components required by both variants.
set(COMMON_REQS
  board i2s_amp i2s_mic audio_capture buttons conv_db
  conv_manager lora_sx1262 opus_enc power_mgmt psram_ring ptt
  radio_task sd_card spi_bus storage_flush ui_state watchdog
)

if(CONFIG_TETHER_BOARD_T3S3_MVSR)
  set(VARIANT_REQS ssd1306)
else()
  set(VARIANT_REQS epd pca9557)
endif()

idf_component_register(
  SRCS "main.cpp"
  INCLUDE_DIRS "."
  REQUIRES ${COMMON_REQS} ${VARIANT_REQS}
)
