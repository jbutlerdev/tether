# storage_flush/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(storage_flush_host STATIC src/storage_flush.cpp)
  target_include_directories(storage_flush_host PUBLIC include)
  target_compile_definitions(storage_flush_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(storage_flush_host PUBLIC freertos_shim
                        psram_ring_host sd_card_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/storage_flush.cpp"
    INCLUDE_DIRS "include"
    REQUIRES psram_ring sd_card
  )
else()
  message(STATUS "storage_flush: skipped")
endif()
