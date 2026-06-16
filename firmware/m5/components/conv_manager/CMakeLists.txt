# conv_manager/CMakeLists.txt — conv manager task (plan.md §5.5).
#
# The manager is a thin adapter over ConvDb. On host it links
# against the same typed VFS wrapper and ConvDb that the rest
# of the M5 firmware uses.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(conv_manager_host STATIC src/conv_manager.cpp)
  target_include_directories(conv_manager_host PUBLIC include)
  target_compile_definitions(conv_manager_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(conv_manager_host PUBLIC freertos_shim conv_db_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/conv_manager.cpp"
    INCLUDE_DIRS "include"
    REQUIRES conv_db
  )
else()
  message(STATUS "conv_manager: skipped")
endif()
