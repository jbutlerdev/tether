# aes_link/CMakeLists.txt — Tether M5 link-layer crypto (plan §9.1).
#
# Compiles a self-contained HKDF-SHA256 + AES-128-CTR key
# derivation. The production firmware uses mbedTLS for SHA-256
# (already part of ESP-IDF). The host build uses a freertos_shim
# SHA-256 implementation so the unit tests can run on the dev
# machine without ESP-IDF.

if(DEFINED TETHER_M5_HOST_TEST)
  add_library(aes_link_host STATIC src/aes_link.cpp)
  target_include_directories(aes_link_host PUBLIC include)
  target_compile_definitions(aes_link_host PUBLIC TETHER_M5_HOST_TEST=1)
  target_link_libraries(aes_link_host PUBLIC freertos_shim)
elseif(COMMAND idf_component_register)
  idf_component_register(
    SRCS "src/aes_link.cpp"
    INCLUDE_DIRS "include"
    REQUIRES mbedtls
  )
else()
  message(STATUS "aes_link: skipped")
endif()
