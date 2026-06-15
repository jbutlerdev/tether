# crash_log/CMakeLists.txt — Tether M5 crash log to LittleFS (plan §9.3).
#
# Captures a fixed-size binary record to /crash/<name>.bin
# every time the M5 hits a panic, watchdog, or brownout. The
# conv manager (plan §5.5) reads the directory on boot, ships
# the records to the base station, and deletes the files.
#
# On real hardware this routes through the existing LittleFS
# VFS wrapper. On the host build it uses std::filesystem in a
# tmpfs so the unit tests run without ESP-IDF.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(crash_log_host STATIC src/crash_log.cpp)
  target_include_directories(crash_log_host PUBLIC include)
  target_compile_definitions(crash_log_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(crash_log_host PUBLIC freertos_shim littlefs_vfs_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/crash_log.cpp"
    INCLUDE_DIRS "include"
    REQUIRES littlefs_vfs
  )
else()
  message(STATUS "crash_log: skipped")
endif()
