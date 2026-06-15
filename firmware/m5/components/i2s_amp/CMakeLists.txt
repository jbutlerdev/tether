# i2s_amp/CMakeLists.txt — I2S amplifier with tone generator.
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(i2s_amp_host STATIC src/i2s_amp.cpp)
  target_include_directories(i2s_amp_host PUBLIC include)
  target_compile_definitions(i2s_amp_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/i2s_amp.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver
  )
else()
  message(STATUS "i2s_amp: skipped")
endif()
