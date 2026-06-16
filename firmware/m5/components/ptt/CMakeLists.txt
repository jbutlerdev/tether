# ptt/CMakeLists.txt — PTT state machine.
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(ptt_host STATIC src/ptt.cpp)
  target_include_directories(ptt_host PUBLIC include)
  target_compile_definitions(ptt_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(ptt_host PUBLIC freertos_shim buttons_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/ptt.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver buttons
  )
else()
  message(STATUS "ptt: skipped")
endif()
