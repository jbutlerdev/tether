# i2s_mic/CMakeLists.txt — I2S microphone.
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(i2s_mic_host STATIC src/i2s_mic.cpp)
  target_include_directories(i2s_mic_host PUBLIC include)
  target_compile_definitions(i2s_mic_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/i2s_mic.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver
  )
else()
  message(STATUS "i2s_mic: skipped")
endif()
