# ESP-IDF top-level CMake for the Tether M5 (ThinkNode M5) firmware.
# See plan.md §1.1.
cmake_minimum_required(VERSION 3.16)

set(EXTRA_COMPONENT_DIRS "")

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(tether-m5)
