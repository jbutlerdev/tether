// main.cpp — Tether M5 (ThinkNode M5) firmware entry point.
//
// Phase 3 wires up all components and starts the FreeRTOS tasks. The
// app_main() function (called by the ESP-IDF runtime) does, in order:
//   1. Initialize NVS
//   2. Mount the SD card (LittleFS)
//   3. Initialize the SPI bus and add the SD / LoRa / EPD devices
//   4. Initialize the I2S mic and amp
//   5. Allocate the PSAM ring buffer
//   6. Initialize the buttons
//   7. Initialize the LoRa radio
//   8. Start all FreeRTOS tasks
//   9. Register them with the watchdog
//  10. Log 'tether ready' and feed the watchdog forever.
//
// On host (unit tests) we provide a separate main() that runs the
// smoke test, see test/test_smoke.cpp.

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "audio_capture.h"
#include "board.h"
#include "buttons.h"
#include "conv_db.h"
#include "conv_manager.h"
#include "i2s_amp.h"
#include "i2s_mic.h"
#include "lora_sx1262.h"
#include "opus_enc.h"
#include "power_mgmt.h"
#include "psram_ring.h"
#include "ptt.h"
#include "radio_task.h"
#include "sd_card.h"
#include "spi_bus.h"
#include "storage_flush.h"
#include "ui_state.h"
#include "watchdog.h"

// Variant-specific display + I/O-expander components. All three are
// always required (see main/CMakeLists.txt) and always included here
// so that `if constexpr` branches that reference variant-specific
// types (e.g. Pca9557 in the kHasPca9557 branch) compile on BOTH
// builds — `if constexpr` in a non-template function still compiles
// the discarded branch. The #if defined(CONFIG_TETHER_BOARD_*) guard
// below selects which display init code runs; the includes are
// unconditional.
#include "epd.h"
#include "pca9557.h"
#include "ssd1306.h"

namespace {
constexpr char kTag[] = "tether.main";
} // namespace

extern "C" void app_main(void) {
  ESP_LOGI(kTag, "boot");

  // 1. NVS (the radio channel and PSK live here in Phase 8, but we
  //    initialize the partition early so other components can use it).
  // nvs_flash_init(); // TODO(phase-8)

  // 2. Mount the SD card.
  tether::m5::SdCard sd;
  if (sd.Mount() != ESP_OK) {
    ESP_LOGE(kTag, "SD mount failed; continuing without storage");
  }

  // 3. Initialize the SPI bus(es) via the singletons. Bus() is the
  //    LoRa bus (SPI2 on both variants); SdBus() is the SD bus — the
  //    same instance as Bus() on the M5 (shared bus), a separate
  //    SPI3 instance on the MVSR. Components (lora_sx1262, sd_card)
  //    use the same singletons, so the bus + mutex are consistent.
  tether::m5::Bus().AddDevice(
      /*LORA_CS=*/tether::m5::board::kPinLoraCs, 8'000'000);
  tether::m5::SdBus().AddDevice(
      /*SD_CS=*/tether::m5::board::kPinSdCs, 20'000'000);

  // 4. I2S mic / amp. The mic and amp share a single I2S0 bus on
  //    the M5 (full-duplex, same BCLK/WS) and are on separate
  //    peripherals on the MVSR (I2S0 mic, I2S1 amp). The M5 path
  //    requires the two hardware mods (docs/HARDWARE-MODS.md); the
  //    MVSR path needs no mod. The port + interface come from
  //    board.h, so this code is identical across variants.
  static tether::m5::I2SMic mic;
  if (!mic.Init()) {
    ESP_LOGE(kTag, "i2s_mic init failed; PTT will record silence");
  }
  static tether::m5::I2SAmp amp;
  if (!amp.Init()) {
    ESP_LOGE(kTag, "i2s_amp init failed; no audio feedback");
  }

  // 4b. Display + I/O expander (variant-specific). The M5 has a
  //     PCA9557 I/O expander (LEDs, e-ink backlight, master power
  //     rail) and a 1.54" EPD; the MVSR has a direct-GPIO LED and a
  //     0.96" SSD1306 OLED, no expander. Gated on kHasPca9557 /
  //     kDisplayKind so the dead branch is eliminated per variant.
  if constexpr (tether::m5::board::kHasPca9557) {
    static tether::m5::Pca9557 pca;
    if (!pca.Init()) {
      ESP_LOGE(kTag, "pca9557 init failed; LEDs/peripherals may be off");
    }
  }
#if defined(CONFIG_TETHER_BOARD_T3S3_MVSR)
  static tether::m5::Ssd1306 display;
  if (display.Init()) {
    display.RenderBootScreen();
    display.Flush();
  } else {
    ESP_LOGE(kTag, "ssd1306 init failed; no display");
  }
#else
  // EPD init is wired in Phase 4 (the epd component's controller).
  // The M5 build links the epd component; placeholder here.
  (void)0;
#endif

  // 5. PSRAM ring buffer (shared by audio_capture and storage_flush).
  // We allocate a 32 KB ring in PSRAM. Two consumers, so we use the
  // SPSC pattern from research.md §7.3.
  static tether::m5::PsramRing ring(32 * 1024);

  // 6. Buttons.
  static tether::m5::Buttons buttons;
  buttons.Init([](tether::m5::ButtonEvent ev) {
    // The PTT state machine is driven by button events from the
    // ui_state task; this handler is a placeholder until Phase 4
    // wires the conv switcher.
    (void)ev;
  });

  // 7. LoRa radio. The mock backend is used in tests; on real
  // hardware we'd construct a RadioLibBackend.
  // (Radio init is done in the radio task itself so we don't need
  // to construct a global here.)

  // 8. PTT state machine and UI state.
  static tether::m5::Ptt ptt;
  static tether::m5::UiState ui;
  ui.SetPtt(&ptt);

  // 8b. Conversation DB and manager. The DB is on the SD card
  //     (rooted at /sdcard by sd_card.cpp). The manager emits
  //     a sync request on startup so the base station can push
  //     any convs the M5 missed while offline.
  static tether::m5::ConvDb conv_db;
  if (conv_db.Init("/sdcard") != ESP_OK) {
    ESP_LOGE(kTag, "conv_db init failed; conv list will be empty");
  }
  static tether::m5::ConvManager conv_mgr(conv_db);
  conv_mgr.Start();
  // Hand the UI a pointer to the live conv list. The UI
  // re-reads on every render so no extra synchronization is
  // needed here; the conv_db internal mutex guards the
  // underlying filesystem calls.
  ui.SetConversations(nullptr); // Phase 5 wires the live list

  // 9. Start FreeRTOS tasks. The task entry points are defined in
  // their respective components; Phase 3 wires them up here.
  //
  // For Phase 3 we only start the watchdog and ui_state tick (the
  // other tasks are exercised on the bench in Phase 4 / 5). Real
  // wiring happens in Phase 4 once EPD is up.

  // 10. Watchdog.
  static tether::m5::Watchdog wdt;
  wdt.Register("ui_state");
  wdt.Register("ptt");
  wdt.Register("conv_manager");

  ESP_LOGI(kTag, "tether ready");

  for (;;) {
    vTaskDelay(pdMS_TO_TICKS(500));
    wdt.FeedAll();
  }
}
