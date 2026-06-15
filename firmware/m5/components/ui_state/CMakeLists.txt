# ui_state/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(ui_state_host STATIC src/ui_state.cpp)
  target_include_directories(ui_state_host PUBLIC include)
  target_compile_definitions(ui_state_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(ui_state_host PUBLIC freertos_shim ptt_host epd_host conv_db_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/ui_state.cpp"
    INCLUDE_DIRS "include"
    REQUIRES ptt epd conv_db
  )
else()
  message(STATUS "ui_state: skipped")
endif()
