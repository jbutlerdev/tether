// board.h — Tether board pin map (variant selector).
//
// This is the single source of truth for every GPIO assignment on
// the M5-family device. AGENTS.md §3.4.1: "All M5 GPIO assignments
// live in board.h (a separate ESP-IDF component called board). Do
// not hard-code GPIO numbers in component code — #include "board.h"
// and reference the kPin… constants."
//
// Tether supports multiple hardware variants. The active variant is
// selected at build time by the Kconfig choice TETHER_BOARD
// (see board/Kconfig), which defines exactly one of:
//
//   CONFIG_TETHER_BOARD_M5           — ThinkNode M5 (ELECROW)
//   CONFIG_TETHER_BOARD_T3S3_MVSR    — LilyGO T3-S3 MVSR
//
// Each variant header (board_m5.h / board_t3s3_mvsr.h) defines the
// SAME set of symbols — the kPin… constants plus capability flags
// (kHasPca9557, kDisplayKind, kI2sMicPort, kMicInterface, the SPI
// hosts, the do-not-touch PSRAM pins, etc.) — with variant-specific
// values. main.cpp and the components reference those symbols, so
// the same source builds for either board and the pin assignment is
// data-driven. The test_host/board test pins a compile-time check
// that every variant defines the full symbol set.
//
// To add a new variant: add a board_<name>.h defining the full
// symbol set, add a Kconfig choice entry, and add a host test. See
// docs/VARIANTS.md.

#pragma once

#ifdef TETHER_M5_HOST_TEST
// On the host build we don't have ESP-IDF's driver/gpio.h. Provide
// a minimal shim that defines the GPIO_NUM_N constants used by the
// variant headers so the host build can compile. The shim must
// cover every GPIO referenced by any variant (0–48).
#include <cstdint>
using gpio_num_t = int; // host shim
#define GPIO_NUM_NC (-1)
#define GPIO_NUM_0 0
#define GPIO_NUM_1 1
#define GPIO_NUM_2 2
#define GPIO_NUM_3 3
#define GPIO_NUM_4 4
#define GPIO_NUM_5 5
#define GPIO_NUM_6 6
#define GPIO_NUM_7 7
#define GPIO_NUM_8 8
#define GPIO_NUM_9 9
#define GPIO_NUM_10 10
#define GPIO_NUM_11 11
#define GPIO_NUM_12 12
#define GPIO_NUM_13 13
#define GPIO_NUM_14 14
#define GPIO_NUM_15 15
#define GPIO_NUM_16 16
#define GPIO_NUM_17 17
#define GPIO_NUM_18 18
#define GPIO_NUM_19 19
#define GPIO_NUM_20 20
#define GPIO_NUM_21 21
#define GPIO_NUM_22 22
#define GPIO_NUM_23 23
#define GPIO_NUM_25 25
#define GPIO_NUM_26 26
#define GPIO_NUM_27 27
#define GPIO_NUM_28 28
#define GPIO_NUM_29 29
#define GPIO_NUM_30 30
#define GPIO_NUM_31 31
#define GPIO_NUM_32 32
#define GPIO_NUM_33 33
#define GPIO_NUM_34 34
#define GPIO_NUM_35 35
#define GPIO_NUM_36 36
#define GPIO_NUM_37 37
#define GPIO_NUM_38 38
#define GPIO_NUM_39 39
#define GPIO_NUM_40 40
#define GPIO_NUM_41 41
#define GPIO_NUM_42 42
#define GPIO_NUM_43 43
#define GPIO_NUM_44 44
#define GPIO_NUM_45 45
#define GPIO_NUM_46 46
#define GPIO_NUM_47 47
#define GPIO_NUM_48 48
#else
#include "driver/gpio.h"
#endif

// The Kconfig choice defines exactly one CONFIG_TETHER_BOARD_*. On
// the host build (TETHER_M5_HOST_TEST) there is no Kconfig, so we
// default to the M5 variant unless the test overrides it by defining
// TETHER_BOARD_T3S3_MVSR (the test_host CMake passes -D for the
// variant under test).
#if defined(TETHER_M5_HOST_TEST) && !defined(TETHER_BOARD_T3S3_MVSR)
#include "board_m5.h"
#elif defined(CONFIG_TETHER_BOARD_T3S3_MVSR) || defined(TETHER_BOARD_T3S3_MVSR)
#include "board_t3s3_mvsr.h"
#else
#include "board_m5.h"
#endif
