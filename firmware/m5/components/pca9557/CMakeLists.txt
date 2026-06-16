# pca9557/CMakeLists.txt — PCA9557 I/O expander driver.
# See include/pca9557.h for the public API and the pin map.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(pca9557_host STATIC src/pca9557.cpp)
  target_include_directories(pca9557_host PUBLIC include)
  target_compile_definitions(pca9557_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(pca9557_host PUBLIC freertos_shim)
  target_include_directories(pca9557_host PRIVATE ../board/include)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/pca9557.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver board
  )
else()
  message(STATUS "pca9557: skipped")
endif()
