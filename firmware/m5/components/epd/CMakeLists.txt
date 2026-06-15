# epd/CMakeLists.txt — Tether M5 EPD controller + screens (plan §5.3).
#
# The same source file (src/screens.cpp) compiles for both host
# and ESP-IDF targets. The host build links against freertos_shim
# for the FreeRTOS includes; the production build links against
# the EPD driver in esp_lcd.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(epd_host STATIC src/screens.cpp)
  target_include_directories(epd_host PUBLIC include)
  target_compile_definitions(epd_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(epd_host PUBLIC freertos_shim conv_db_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/screens.cpp"
    INCLUDE_DIRS "include"
    REQUIRES conv_db
  )
else()
  message(STATUS "epd: skipped")
endif()
