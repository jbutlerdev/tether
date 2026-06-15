# unity/CMakeLists.txt — vendored Unity (ThrowTheSwitch) test framework.
# We bundle a minimal copy here so the host tests run without depending
# on ESP-IDF or PlatformIO. This is the same Unity release that ESP-IDF
# uses internally (2.5.x API).

add_library(unity_host STATIC
  unity.c
)
target_include_directories(unity_host PUBLIC
  ${CMAKE_CURRENT_SOURCE_DIR}
)
set_target_properties(unity_host PROPERTIES LINKER_LANGUAGE C)
