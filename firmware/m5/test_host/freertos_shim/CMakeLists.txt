# FreeRTOS shim — minimal C++ backing for the small FreeRTOS API the
# host tests need. See freertos_shim.h for the list of supported calls.
add_library(freertos_shim STATIC freertos_shim.cpp)
target_include_directories(freertos_shim PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
