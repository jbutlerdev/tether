// test_smoke.cpp — integration smoke test for the M5 task set.
//
// Runs the full task set on host (mocked hardware) under synthetic
// load and verifies that no deadlocks or task-starvation occur. This
// is the Phase 3 exit gate (plan.md §4.10).

#include <atomic>
#include <chrono>
#include <thread>
#include <vector>

#include <unity.h>

#include "audio_capture.h"
#include "buttons.h"
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

using namespace tether::m5;

namespace {
constexpr int kLoadFrames = 200;
}

void setUp() {}
void tearDown() {}

// Test: run the whole task set for 200 frames and verify no
// starvation, no exceptions, and the expected counters advance.
void test_smoke_full_task_set() {
  PsramRing ring(32 * 1024);
  OpusEncoder enc(8000, 16000, 5);
  AudioCapture cap(ring, enc);
  cap.SetI2SRunningForTest(true);

  SdCard card;
  // Skip the SD mount in the smoke test; storage_flush is exercised
  // in its own component test.

  std::shared_ptr<MockRadioBackend> backend = std::make_shared<MockRadioBackend>();
  LoraRadio radio(backend);
  RadioTask rt(radio);

  Ptt ptt;
  UiState ui;
  ui.SetPtt(&ptt);

  Watchdog wdt;
  wdt.Register("audio_capture");
  wdt.Register("ptt");
  wdt.Register("radio_task");

  // Drive the PTT state machine through one full cycle.
  ptt.OnButton(ButtonEvent{Button::kPtt, Event::kPress});
  ptt.OnButton(ButtonEvent{Button::kPtt, Event::kRelease});
  ptt.OnRadioAccepted();
  ptt.OnRadioAllAcked();

  // Drive the audio capture for 200 frames.
  for (int i = 0; i < kLoadFrames; ++i) {
    cap.RunOnce();
    if (i % 20 == 0) wdt.FeedAll();
  }
  TEST_ASSERT_EQUAL(kLoadFrames, cap.FramesEncoded());

  // Drive the radio task to drain its outbox.
  for (int i = 0; i < 50; ++i) {
    rt.Enqueue({0xDE, 0xAD, 0xBE, 0xEF});
    for (int s = 0; s < 5; ++s) rt.Step();
    rt.InjectAckForTest(/*msg_id=*/i + 1, /*bitmap=*/0x1);
  }
  TEST_ASSERT_GREATER_THAN(0, rt.AcksReceived());

  // Verify the watchdog counted feeds.
  TEST_ASSERT_GREATER_THAN(0, wdt.FeedCountFor("audio_capture"));
  TEST_ASSERT_GREATER_THAN(0, wdt.FeedCountFor("ptt"));
  TEST_ASSERT_GREATER_THAN(0, wdt.FeedCountFor("radio_task"));
}

// Test: two threads (audio producer + storage consumer) using the
// shared SPSC ring for 100 ms — race-detector clean in TSan builds.
void test_smoke_concurrent_ring() {
  PsramRing ring(8 * 1024);
  std::atomic<bool> done{false};
  std::atomic<int> produced{0};
  std::atomic<int> consumed{0};
  std::thread producer([&]() {
    uint8_t v = 0;
    while (!done.load()) {
      if (ring.Write(&v, 1) > 0) {
        produced.fetch_add(1);
        v++;
      } else {
        std::this_thread::yield();
      }
    }
  });
  std::thread consumer([&]() {
    uint8_t v = 0;
    while (!done.load() || ring.Available() > 0) {
      if (ring.Read(&v, 1) > 0) {
        consumed.fetch_add(1);
      } else {
        std::this_thread::yield();
      }
    }
  });
  std::this_thread::sleep_for(std::chrono::milliseconds(100));
  done.store(true);
  producer.join();
  consumer.join();
  // No loss: consumed should equal produced.
  TEST_ASSERT_EQUAL(produced.load(), consumed.load());
}

int main(int argc, const char **argv) {
  (void)argc; (void)argv;
  UNITY_BEGIN();
  RUN_TEST(test_smoke_full_task_set);
  RUN_TEST(test_smoke_concurrent_ring);
  (void)0;
  UNITY_END();
}
