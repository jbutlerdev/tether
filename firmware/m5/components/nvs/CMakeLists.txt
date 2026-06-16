# nvs/CMakeLists.txt — Tether M5 NVS schema bindings (plan §9.6).
#
# The C++ side mirrors the Go schema in go/internal/nvs. The
# host build uses an in-memory map so the unit tests run on
# the dev machine; the production build routes through the
# ESP-IDF NVS API.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(nvs_host STATIC src/nvs.cpp)
  target_include_directories(nvs_host PUBLIC include)
  target_compile_definitions(nvs_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(nvs_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/nvs.cpp"
    INCLUDE_DIRS "include"
    REQUIRES nvs_flash
  )
else()
  message(STATUS "nvs: skipped")
endif()
