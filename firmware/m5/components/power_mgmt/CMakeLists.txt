# power_mgmt/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(power_mgmt_host STATIC src/power_mgmt.cpp)
  target_include_directories(power_mgmt_host PUBLIC include)
  target_compile_definitions(power_mgmt_host PUBLIC TETHER_M5_HOST_TEST=1)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/power_mgmt.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver
  )
else()
  message(STATUS "power_mgmt: skipped")
endif()
