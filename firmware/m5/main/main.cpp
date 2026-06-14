// Empty Tether M5 app entry point. Phase 0 skeleton.
// See plan.md §1.1.
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

extern "C" void app_main(void) {
  ESP_LOGI("tether", "boot");
  // Phase 0: nothing else to do. Subsequent phases fill in tasks.
  vTaskDelete(NULL);
}
