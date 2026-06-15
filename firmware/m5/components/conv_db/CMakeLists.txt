# conv_db/CMakeLists.txt — conversation DB (plan.md §5.2).
#
# On real hardware the production translation uses the ESP-IDF
# LittleFS VFS. On host we use the same src/conv_db.cpp guarded
# by TETHER_M5_HOST_TEST, which means it links against the
# littlefs_vfs_host translation and the freertos shim.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(conv_db_host STATIC src/conv_db.cpp)
  target_include_directories(conv_db_host PUBLIC include)
  target_compile_definitions(conv_db_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(conv_db_host PUBLIC freertos_shim littlefs_vfs_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/conv_db.cpp"
    INCLUDE_DIRS "include"
    REQUIRES littlefs_vfs
  )
else()
  message(STATUS "conv_db: skipped")
endif()
