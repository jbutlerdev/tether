// esp_littlefs stub implementation.
#include "esp_littlefs.h"
#include "esp_vfs_littlefs.h"

#include <cstdio>

esp_err_t esp_vfs_littlefs_register(const esp_vfs_littlefs_conf_t * /*conf*/) {
  std::fprintf(stderr, "esp_littlefs_stub: register\n");
  return ESP_OK;
}

esp_err_t esp_vfs_littlefs_unregister(const char * /*label*/) {
  return ESP_OK;
}
