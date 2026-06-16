# main/CMakeLists.txt — the M5 firmware main component. Lists the
# entry-point source and its dependencies.
idf_component_register(
  SRCS "main.cpp"
  INCLUDE_DIRS "."
)
