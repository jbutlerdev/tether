# watchdog/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(watchdog_host STATIC src/watchdog.cpp)
  target_include_directories(watchdog_host PUBLIC include)
  target_compile_definitions(watchdog_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/watchdog.cpp"
    INCLUDE_DIRS "include"
    REQUIRES esp_task_wdt
  )
else()
  message(STATUS "watchdog: skipped")
endif()
