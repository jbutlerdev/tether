// esp_littlefs stub — minimal vendored header so the M5 firmware
// builds without internet access. Replace with the real
// espressif/littlefs component for production.
//
// This stub exposes the small subset of esp_littlefs used by
// sd_card.cpp: esp_vfs_littlefs_conf_t, esp_vfs_littlefs_register,
// esp_vfs_littlefs_unregister.
#pragma once

#include <cstdint>

#include "esp_err.h"

typedef struct {
  const char *base_path;
  const char *partition_label;
  bool format_if_mount_failed;
  bool dont_mount;
} esp_vfs_littlefs_conf_t;

esp_err_t esp_vfs_littlefs_register(const esp_vfs_littlefs_conf_t *conf);
esp_err_t esp_vfs_littlefs_unregister(const char *partition_label);

#include "esp_vfs_littlefs.h"
