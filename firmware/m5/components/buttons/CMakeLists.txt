# buttons/CMakeLists.txt — Buttons with debounce + long-press.
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(buttons_host STATIC src/buttons.cpp)
  target_include_directories(buttons_host PUBLIC include)
  target_compile_definitions(buttons_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(buttons_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/buttons.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver
  )
else()
  message(STATUS "buttons: skipped")
endif()
