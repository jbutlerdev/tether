// main.cpp — Tether firmware entry point (v0.2.0 integration).
//
// This is the production app_main() for the LilyGO T3-S3 MVSR variant.
// It wires the full data plane:
//
//   mic → I2S → Opus encode → PSRAM ring → drain → radio_task → LoRa TX
//   LoRa RX → radio_task → decode → conv_manager (UI_UPDATE) / amp (TTS)
//
// The MVSR variant is selected at build time by CONFIG_TETHER_BOARD_T3S3_MVSR
// (see board.h). The M5 variant's app_main is structurally similar but
// uses the EPD + PCA9557; the MVSR uses the SSD1306 OLED and direct GPIO.
//
// On host (unit tests) a separate main() runs the smoke test; this file
// is not compiled in the host build.

#include "esp_log.h"
#include "esp_task_wdt.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs_flash.h"

#include "audio_capture.h"
#include "board.h"
#include "buttons.h"
#include "conv_db.h"
#include "conv_manager.h"
#include "i2s_amp.h"
#include "i2s_mic.h"
#include "lora_sx1262.h"
#include "opus_dec.h"
#include "opus_enc.h"
#include "power_mgmt.h"
#include "protocol.h"
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
// types compile on BOTH builds. The #if guards below select which
// display init code runs.
#include "epd.h"
#include "pca9557.h"
#include "ssd1306.h"

namespace {
constexpr char kTag[] = "tether.main";

// Task priorities + stack sizes (research.md §7.1).
constexpr UBaseType_t kPrioAudio = 23;
constexpr UBaseType_t kPrioRadio = 23;
constexpr UBaseType_t kPrioStorage = 15;
constexpr UBaseType_t kPrioConvMgr = 16;
constexpr UBaseType_t kPrioUi = 8;
constexpr UBaseType_t kPrioWatchdog = 10;
constexpr size_t kStackAudio = 4096;
constexpr size_t kStackRadio = 8192;
constexpr size_t kStackStorage = 4096;
constexpr size_t kStackConvMgr = 4096;
constexpr size_t kStackUi = 4096;

// The default broadcast conversation_id (all-zeros). In v1 the M5
// has a single conversation until the base station pushes UI_UPDATEs.
constexpr uint8_t kDefaultConvID[tether::m5::kConvIDSize] = {};

// Shared state (owned by app_main, passed to tasks).
tether::m5::LoraRadio *g_radio = nullptr;
tether::m5::RadioTask *g_radio_task = nullptr;
tether::m5::AudioCapture *g_capture = nullptr;
tether::m5::Ptt *g_ptt = nullptr;
tether::m5::Ssd1306 *g_display = nullptr;
tether::m5::I2SAmp *g_amp = nullptr;

// Audio pipeline components.
tether::m5::PsramRing *g_ring = nullptr;
tether::m5::OpusEncoder *g_enc = nullptr;
tether::m5::OpusDecoder *g_dec = nullptr;
tether::m5::SdCard *g_sd = nullptr;
tether::m5::StorageFlush *g_flush = nullptr;

// Recording buffer: accumulates length-delimited Opus frames during
// PTT. The audio_capture writes 2-byte LE length + frame into the
// PSRAM ring; the AudioCaptureEntry task drains the ring into this
// buffer. On PTT release, this buffer is enqueued to the radio task
// as the message payload.
std::vector<uint8_t> g_recording_buffer;
bool g_is_recording = false;

// TTS playback buffer: accumulates incoming TTS_DATA payloads until
// TTS_END arrives, then decodes and plays via I2S.
std::vector<uint8_t> g_tts_buffer;

// PTT -> audio capture -> radio enqueue.
void OnPttStateChange(tether::m5::PttState old_s, tether::m5::PttState new_s) {
  if (old_s != tether::m5::PttState::kRecording &&
      new_s == tether::m5::PttState::kRecording) {
    // PTT pressed - start recording.
    g_recording_buffer.clear();
    g_is_recording = true;
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "REC");
      g_display->Flush();
    }
  } else if (old_s == tether::m5::PttState::kRecording &&
             new_s == tether::m5::PttState::kQueued) {
    // PTT released - enqueue the recording to the radio task.
    g_is_recording = false;
    if (g_radio_task && !g_recording_buffer.empty()) {
      g_radio_task->Enqueue(kDefaultConvID, std::move(g_recording_buffer));
      g_recording_buffer.clear();
    }
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "TX...");
      g_display->Flush();
    }
  } else if (new_s == tether::m5::PttState::kAcked) {
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "Sent OK");
      g_display->Flush();
    }
  } else if (new_s == tether::m5::PttState::kFailed) {
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "TX FAILED");
      g_display->Flush();
    }
  }
}

// Incoming packet handler (TTS / UI_UPDATE).
void OnIncomingPacket(const tether::m5::IncomingPacket &pkt) {
  if (pkt.header.msg_type == tether::m5::MsgType::kTtsData) {
    // TTS audio chunk - append to the TTS playback buffer. The
    // payload is a length-delimited blob of Opus frames (same
    // format as the recording buffer). We decode + play on TTS_END.
    g_tts_buffer.insert(g_tts_buffer.end(), pkt.payload.begin(),
                        pkt.payload.end());
  } else if (pkt.header.msg_type == tether::m5::MsgType::kTtsEnd) {
    // TTS_END - decode the buffered Opus frames and play via I2S.
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "Playing TTS");
      g_display->Flush();
    }
    // Decode the length-delimited blob frame by frame.
    size_t off = 0;
    while (off + 2 <= g_tts_buffer.size()) {
      uint16_t frameLen = g_tts_buffer[off] | (g_tts_buffer[off + 1] << 8);
      off += 2;
      if (frameLen == 0 || off + frameLen > g_tts_buffer.size())
        break;
      auto pcm = g_dec->DecodeFrame(g_tts_buffer.data() + off, frameLen);
      if (!pcm.empty() && g_amp) {
        g_amp->WritePCM(pcm.data(), pcm.size());
      }
      off += frameLen;
    }
    g_tts_buffer.clear();
    if (g_display) {
      g_display->Clear();
      g_display->DrawText(0, 0, "TTS done");
      g_display->Flush();
    }
  } else if (pkt.header.msg_type == tether::m5::MsgType::kUiUpdate) {
    // UI_UPDATE - the conv_manager would handle this. For v1 we
    // just log it; full conv DB wiring is below.
    ESP_LOGI(kTag, "UI_UPDATE received");
  }
}

// FreeRTOS task entry points.
void RadioTaskEntry(void * /*arg*/) {
  for (;;) {
    if (g_radio_task) {
      g_radio_task->Step();
      // Brief yield between steps; the radio task pumps TX + RX.
      vTaskDelay(pdMS_TO_TICKS(10));
    }
  }
}

void AudioCaptureEntry(void * /*arg*/) {
  for (;;) {
    if (g_capture && g_is_recording) {
      // Encode one frame into the PSRAM ring.
      g_capture->RunOnce();
      // Drain the ring into the recording buffer. The ring contains
      // 2-byte LE length-prefixed Opus frames. Read them out and
      // append to g_recording_buffer.
      uint8_t hdr[2];
      while (g_ring && g_ring->Available() >= 2) {
        g_ring->Read(hdr, 2);
        uint16_t frameLen = hdr[0] | (hdr[1] << 8);
        if (frameLen == 0)
          continue;
        // Ensure we have enough data in the ring for the full frame.
        if (g_ring->Available() < frameLen) {
          // Partial frame - can't unread, so reset and wait.
          // (In practice this shouldn't happen because RunOnce writes
          // the length + frame atomically, but guard against it.)
          g_ring->ResetForTest();
          break;
        }
        std::vector<uint8_t> frame(frameLen);
        g_ring->Read(frame.data(), frameLen);
        // Append the 2-byte length + frame to the recording buffer
        // (same format as the wire payload).
        g_recording_buffer.push_back(hdr[0]);
        g_recording_buffer.push_back(hdr[1]);
        g_recording_buffer.insert(g_recording_buffer.end(), frame.begin(),
                                  frame.end());
      }
    }
    vTaskDelay(pdMS_TO_TICKS(20)); // 20 ms = 1 Opus frame
  }
}

void WatchdogEntry(void * /*arg*/) {
  tether::m5::Watchdog wdt;
  wdt.Register("radio_task");
  wdt.Register("audio_capture");
  for (;;) {
    wdt.FeedAll();
    vTaskDelay(pdMS_TO_TICKS(500));
  }
}

} // namespace

extern "C" void app_main(void) {
  ESP_LOGI(kTag, "tether boot (%s)", tether::m5::board::kBoardName);

  // 1. NVS.
  esp_err_t err = nvs_flash_init();
  if (err == ESP_ERR_NVS_NO_FREE_PAGES ||
      err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
    ESP_ERROR_CHECK(nvs_flash_erase());
    ESP_ERROR_CHECK(nvs_flash_init());
  }

  // 2. SD card (LittleFS on SD, store-and-forward queue).
  static tether::m5::SdCard sd;
  g_sd = &sd;
  if (sd.Mount() != ESP_OK) {
    ESP_LOGE(kTag, "SD mount failed; continuing without storage");
  }

  // 3. SPI buses. Bus() is LoRa (SPI2); SdBus() is SD (SPI3 on MVSR).
  tether::m5::Bus().AddDevice(
      /*LORA_CS=*/tether::m5::board::kPinLoraCs, 8'000'000);
  tether::m5::SdBus().AddDevice(
      /*SD_CS=*/tether::m5::board::kPinSdCs, 20'000'000);

  // 4. I2S mic + amp. The MVSR has them on separate I2S peripherals
  //    (I2S0 mic, I2S1 amp); no hardware mod needed.
  static tether::m5::I2SMic mic;
  if (!mic.Init()) {
    ESP_LOGE(kTag, "i2s_mic init failed; PTT will record silence");
  }
  static tether::m5::I2SAmp amp;
  g_amp = &amp;
  if (!amp.Init()) {
    ESP_LOGE(kTag, "i2s_amp init failed; no audio feedback");
  }

  // 4b. Display (variant-specific). The MVSR has a SSD1306 OLED.
  if constexpr (tether::m5::board::kHasPca9557) {
    static tether::m5::Pca9557 pca;
    if (!pca.Init()) {
      ESP_LOGE(kTag, "pca9557 init failed; LEDs/peripherals may be off");
    }
  }
#if defined(CONFIG_TETHER_BOARD_T3S3_MVSR)
  static tether::m5::Ssd1306 display;
  g_display = &display;
  if (display.Init()) {
    display.RenderBootScreen();
    display.Flush();
  } else {
    ESP_LOGE(kTag, "ssd1306 init failed; no display");
  }
#endif

  // 5. PSRAM ring + Opus encoder/decoder + audio capture.
  static tether::m5::PsramRing ring(32 * 1024);
  g_ring = &ring;
  static tether::m5::OpusEncoder enc;
  g_enc = &enc;
  static tether::m5::OpusDecoder dec;
  g_dec = &dec;
  static tether::m5::AudioCapture capture(ring, enc);
  g_capture = &capture;
  if (!capture.Init()) {
    ESP_LOGE(kTag, "audio_capture init failed");
  }

  // 6. Storage flush (drains PSRAM ring -> SD card).
  static tether::m5::StorageFlush flush(ring, sd);
  g_flush = &flush;
  if (!flush.Init()) {
    ESP_LOGE(kTag, "storage_flush init failed");
  }

  // 7. LoRa radio. Construct the RadioLib backend via the factory
  //    (the backend class is in an anonymous namespace in
  //    lora_sx1262.cpp). The SPI bus is already initialized.
  auto backend = tether::m5::MakeRadioLibBackend();
  static tether::m5::LoraRadio radio(backend);
  g_radio = &radio;
  radio.Init(tether::m5::Preset::Default());
  radio.SetChannel(0); // US915 ch 0 = 902.3 MHz

  // 8. Radio task (fragmentation, ACK, retransmit).
  static tether::m5::RadioTask radio_task(radio, /*sender_id=*/0x0001,
                                          /*target_id=*/0x0002);
  g_radio_task = &radio_task;
  radio_task.SetIncomingHandler(OnIncomingPacket);

  // 9. Buttons -> PTT state machine.
  static tether::m5::Ptt ptt;
  g_ptt = &ptt;
  ptt.OnStateChange(OnPttStateChange);
  static tether::m5::Buttons buttons;
  buttons.Init([](tether::m5::ButtonEvent ev) {
    if (g_ptt) {
      g_ptt->OnButton(ev);
    }
  });

  // 10. Conversation DB + manager.
  static tether::m5::ConvDb conv_db;
  if (conv_db.Init("/sdcard") != ESP_OK) {
    ESP_LOGE(kTag, "conv_db init failed; conv list will be empty");
  }
  static tether::m5::ConvManager conv_mgr(conv_db);
  conv_mgr.Start();

  // 11. Start FreeRTOS tasks (research.md §7.1).
  xTaskCreate(RadioTaskEntry, "radio_task", kStackRadio, nullptr, kPrioRadio,
              nullptr);
  xTaskCreate(AudioCaptureEntry, "audio_capture", kStackAudio, nullptr,
              kPrioAudio, nullptr);
  xTaskCreate(WatchdogEntry, "watchdog", 2048, nullptr, kPrioWatchdog, nullptr);

  ESP_LOGI(kTag, "tether ready");

  // 12. Main loop: keep the main task alive for ESP_LOG and display.
  for (;;) {
    vTaskDelay(pdMS_TO_TICKS(100));
  }
}
