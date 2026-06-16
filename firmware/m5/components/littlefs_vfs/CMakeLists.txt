# littlefs_vfs/CMakeLists.txt — typed LittleFS VFS wrapper (plan.md §5.1).
#
# On real hardware we compile src/littlefs_vfs.cpp, which wraps the
# SdCard VFS root (/sdcard) and exposes the typed LfsVfs API. The
# host build compiles src/littlefs_vfs_host.cpp which provides the
# same API against a tmpfs rooted at a test-supplied directory.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(littlefs_vfs_host STATIC src/littlefs_vfs_host.cpp)
  target_include_directories(littlefs_vfs_host PUBLIC include)
  target_compile_definitions(littlefs_vfs_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(littlefs_vfs_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/littlefs_vfs.cpp"
    INCLUDE_DIRS "include"
    REQUIRES sd_card
  )
else()
  message(STATUS
    "littlefs_vfs: skipped (no TETHER_M5_HOST_TEST and no idf_component_register)")
endif()
