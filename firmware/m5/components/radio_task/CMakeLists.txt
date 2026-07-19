# radio_task/CMakeLists.txt
if(DEFINED TETHER_M5_HOST_TEST)
  add_library(radio_task_host STATIC src/radio_task.cpp)
  target_include_directories(radio_task_host PUBLIC include)
  target_compile_definitions(radio_task_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(radio_task_host PUBLIC freertos_shim
                        lora_sx1262_host protocol_host)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/radio_task.cpp"
    INCLUDE_DIRS "include"
    REQUIRES lora_sx1262 protocol
  )
else()
  message(STATUS "radio_task: skipped")
endif()
