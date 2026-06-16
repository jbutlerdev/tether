# audio_capture/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(audio_capture_host STATIC src/audio_capture.cpp)
  target_include_directories(audio_capture_host PUBLIC include)
  target_compile_definitions(audio_capture_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(audio_capture_host PUBLIC freertos_shim
                        psram_ring_host opus_enc_host i2s_mic_host)
  target_include_directories(audio_capture_host PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../i2s_mic/include)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/audio_capture.cpp"
    INCLUDE_DIRS "include"
    REQUIRES opus_enc psram_ring i2s_mic
  )
else()
  message(STATUS "audio_capture: skipped")
endif()
