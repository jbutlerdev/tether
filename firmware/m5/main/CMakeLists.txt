# main/CMakeLists.txt — the M5 firmware main component. Lists the
# entry-point source and its dependencies.
idf_component_register(
  SRCS "main.cpp"
  INCLUDE_DIRS "."
  REQUIRES board i2s_amp i2s_mic pca9557 audio_capture buttons conv_db
           conv_manager lora_sx1262 opus_enc power_mgmt psram_ring ptt
           radio_task sd_card spi_bus storage_flush ui_state watchdog
)
