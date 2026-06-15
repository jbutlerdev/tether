// esp_vfs_littlefs.h — additional declarations for the stub.
#pragma once

#include "esp_err.h"
esp_err_t esp_vfs_littlefs_register(const void *conf);
esp_err_t esp_vfs_littlefs_unregister(const char *label);
