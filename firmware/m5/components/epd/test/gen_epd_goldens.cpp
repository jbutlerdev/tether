// gen_epd_goldens.cpp — generate the EPD golden-image fixtures.
//
// Run this once after a deliberate renderer change to update
// the checked-in testdata/screens/*.bin files. The host test
// runner (test_host/CMakeLists.txt) compiles this file as
// `gen_epd_goldens` and runs it on demand.
//
// The output of this program IS the golden image. Because the
// renderer is deterministic (no clock, no allocator, no
// randomness) the bytes produced here will be byte-for-byte
// identical to what the test produces on every run, which is
// exactly what we want.

#include <cstdio>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>

#include "conv_db.h"
#include "epd.h"

using tether::m5::ConvInfo;
using tether::m5::HistoryEntry;
using tether::m5::IdleState;
using tether::m5::kEpdBufSize;
using tether::m5::LowBatteryState;
using tether::m5::QueuedState;
using tether::m5::RecordingState;
using tether::m5::RenderIdle;
using tether::m5::RenderLowBattery;
using tether::m5::RenderQueued;
using tether::m5::RenderRecording;
using tether::m5::RenderSettings;
using tether::m5::RenderTransmitting;
using tether::m5::RenderTtsPlayback;
using tether::m5::SettingsState;
using tether::m5::TransmittingState;
using tether::m5::TtsState;

namespace {

ConvInfo MakeConv(const char *name, uint8_t kind = 0,
                  const char *target = "!room:matrix.example.com",
                  int64_t last_activity_ms = 1700000000000LL,
                  uint16_t unread = 0) {
  ConvInfo c{};
  c.exists = true;
  for (int i = 0; i < 16; ++i) {
    c.id[i] = static_cast<uint8_t>(0xA0 ^ i);
  }
  std::strncpy(c.name, name, sizeof c.name - 1);
  c.name[sizeof c.name - 1] = '\0';
  c.kind = kind;
  std::strncpy(c.target, target, sizeof c.target - 1);
  c.target[sizeof c.target - 1] = '\0';
  for (int i = 0; i < 16; ++i) {
    c.enc_key[i] = static_cast<uint8_t>(i * 3);
  }
  c.last_activity_ms = last_activity_ms;
  c.unread = unread;
  return c;
}

HistoryEntry MakeHistory(uint32_t msg_id, int64_t ts, uint8_t dir,
                         const char *text, uint8_t status) {
  HistoryEntry e{};
  e.msg_id = msg_id;
  e.timestamp_ms = ts;
  e.direction = dir;
  std::strncpy(e.text, text, sizeof e.text - 1);
  e.text[sizeof e.text - 1] = '\0';
  e.status = status;
  return e;
}

void WriteFile(const std::string &dir, const std::string &name,
               const uint8_t *buf, size_t n) {
  std::filesystem::create_directories(dir);
  std::string path = dir + "/" + name;
  std::ofstream f(path, std::ios::binary | std::ios::trunc);
  if (!f) {
    std::fprintf(stderr, "FAIL: cannot open %s for write\n", path.c_str());
    std::exit(1);
  }
  f.write(reinterpret_cast<const char *>(buf),
          static_cast<std::streamsize>(n));
  std::fprintf(stdout, "wrote %s (%zu bytes)\n", path.c_str(), n);
}

} // namespace

int main(int argc, const char **argv) {
  (void)argc;
  (void)argv;
  std::string dir = "testdata/screens";
  uint8_t buf[kEpdBufSize] = {};

  // 1. idle default
  {
    IdleState s;
    s.channel = 7;
    s.vbat_mv = 3920;
    s.volume = 60;
    s.convs = {MakeConv("Alice"), MakeConv("Forge build")};
    s.current_index = 0;
    s.recent = {MakeHistory(1, 1700000000000, 1, "see you at 5", 1)};
    std::memset(buf, 0, sizeof buf);
    RenderIdle(s, buf);
    WriteFile(dir, "idle_default.bin", buf, sizeof buf);
  }

  // 2. idle with unread badge
  {
    IdleState s;
    s.channel = 0;
    s.vbat_mv = 4100;
    s.volume = 80;
    s.convs = {MakeConv("Alice (Matrix)", 0, "!a:b", 1, 5),
               MakeConv("Forge: build", 1, "uuid-1", 2, 0)};
    s.current_index = 0;
    s.recent = {MakeHistory(1, 1, 1, "new: ping", 1)};
    std::memset(buf, 0, sizeof buf);
    RenderIdle(s, buf);
    WriteFile(dir, "idle_unread.bin", buf, sizeof buf);
  }

  // 3. idle with no conversations
  {
    IdleState s;
    s.channel = 0;
    s.vbat_mv = 4000;
    s.volume = 60;
    s.convs = {};
    s.current_index = 0;
    s.recent = {};
    std::memset(buf, 0, sizeof buf);
    RenderIdle(s, buf);
    WriteFile(dir, "idle_no_convs.bin", buf, sizeof buf);
  }

  // 4. recording
  {
    RecordingState s;
    s.conv = MakeConv("Alice");
    s.elapsed_ms = 3000;
    s.peak_amplitude = 16000;
    std::memset(buf, 0, sizeof buf);
    RenderRecording(s, buf);
    WriteFile(dir, "recording.bin", buf, sizeof buf);
  }

  // 5. queued
  {
    QueuedState s;
    s.conv = MakeConv("Alice");
    s.file_bytes = 12 * 1024;
    s.enqueued_at_ms = 1700000000000LL;
    std::memset(buf, 0, sizeof buf);
    RenderQueued(s, buf);
    WriteFile(dir, "queued.bin", buf, sizeof buf);
  }

  // 6. transmitting with progress
  {
    TransmittingState s;
    s.conv = MakeConv("Forge: build");
    s.sent_chunks = 38;
    s.total_chunks = 100;
    s.acked_chunks = 47;
    s.elapsed_ms = 38000;
    s.estimated_total_ms = 75000;
    std::memset(buf, 0, sizeof buf);
    RenderTransmitting(s, buf);
    WriteFile(dir, "transmitting.bin", buf, sizeof buf);
  }

  // 7. tts
  {
    TtsState s;
    s.conv = MakeConv("Forge: build-fix");
    s.current_text = "running cargo test now";
    s.elapsed_ms = 8000;
    s.total_ms = 14000;
    std::memset(buf, 0, sizeof buf);
    RenderTtsPlayback(s, buf);
    WriteFile(dir, "tts.bin", buf, sizeof buf);
  }

  // 8. settings
  {
    SettingsState s;
    s.channel = 7;
    s.volume = 60;
    s.vbat_mv = 3920;
    s.node_addr = 0x4A1F;
    s.modem = "SF11/BW125";
    s.cursor = 2;
    std::memset(buf, 0, sizeof buf);
    RenderSettings(s, buf);
    WriteFile(dir, "settings.bin", buf, sizeof buf);
  }

  // 9. low battery
  {
    LowBatteryState s;
    s.vbat_mv = 3300;
    s.critical = false;
    std::memset(buf, 0, sizeof buf);
    RenderLowBattery(s, buf);
    WriteFile(dir, "low_battery.bin", buf, sizeof buf);
  }

  // 10. long conv name
  {
    IdleState s;
    s.channel = 0;
    s.vbat_mv = 4000;
    s.volume = 60;
    s.convs = {MakeConv("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")};
    s.current_index = 0;
    std::memset(buf, 0, sizeof buf);
    RenderIdle(s, buf);
    WriteFile(dir, "long_conv_name.bin", buf, sizeof buf);
  }

  // 11. long message
  {
    IdleState s;
    s.channel = 0;
    s.vbat_mv = 4000;
    s.volume = 60;
    s.convs = {MakeConv("Alice")};
    s.current_index = 0;
    s.recent = {MakeHistory(
        1, 1700000000000, 1,
        "this is a very long inbound message that will be truncated", 1)};
    std::memset(buf, 0, sizeof buf);
    RenderIdle(s, buf);
    WriteFile(dir, "long_message.bin", buf, sizeof buf);
  }

  return 0;
}
