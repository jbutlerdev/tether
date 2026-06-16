// ota_lora.h — v2 OTA-LoRa hook. See plan.md §10.4 (Task 9.4).
//
// v2: OTA-LoRa. The v1 OTA path is USB only (the developer
// runs `idf.py -p /dev/ttyUSB0 flash` and the M5's ROM
// bootloader accepts the image — see OtaUpdater in ota.h).
// v2 will add a second channel: the M5 receives an OTA
// image in chunks over LoRa, reassembles it in PSRAM, and
// hands it to the same VerifyAndCommit path that v1 uses
// for USB.
//
// The hook point is OtaLoraBegin: today it is a no-op that
// returns false; v2 will select the OTA-LoRa subsystem and
// return true on success. The chunk hook is OtaLoraFeed:
// today it accepts and drops chunks; v2 will append them to
// the PSRAM reassembly buffer.
//
// The stubs are unit-tested by test_ota.cpp on the host to
// assert the symbols exist and that v1 returns the v1
// "not enabled" sentinel. v2 inverts the tests: both
// functions must return true on a successful invocation.

#pragma once

#include <cstddef>
#include <cstdint>

namespace tether::m5 {

// kOtaLoraMaxChunkSize is the maximum chunk size the v2
// OTA-LoRa path accepts in a single OtaLoraFeed() call. The
// LoRa fragment size is 200 bytes (research.md §6.1), so
// this is a soft upper bound to keep the v1 build from
// being broken by a runaway v2 caller.
inline constexpr std::size_t kOtaLoraMaxChunkSize = 220;

// OtaLoraBegin is the v2 OTA-LoRa start hook. v1 returns
// false (not enabled); v2 will:
//
//   1. Allocate a PSRAM reassembly buffer.
//   2. Reset the in-component SHA-256 hasher.
//   3. Return true on success.
//
// v1 callers that probe for OTA-LoRa support see a uniform
// "not available" answer; the production daemon falls back
// to USB OTA.
inline bool OtaLoraBegin() {
  // v2: OTA-LoRa. Replace this body with a real
  // implementation that allocates the PSRAM reassembly
  // buffer and resets the SHA-256 hasher.
  return false;
}

// OtaLoraFeed is the v2 OTA-LoRa chunk-accept hook. v1 is
// a no-op: it returns false regardless of input. v2 will:
//
//   1. Append chunk bytes to the PSRAM reassembly buffer.
//   2. Update the running SHA-256.
//   3. Return true if the buffer has room, false if it
//      would overflow (the caller drops the OTA and falls
//      back to USB).
//
// v1 callers see "always false" — the daemon handles this
// by not trying OTA-LoRa again for this M5 session.
inline bool OtaLoraFeed(const uint8_t *chunk, std::size_t len) {
  // v2: OTA-LoRa. Replace this body with a real
  // implementation that copies into the PSRAM buffer
  // and updates the SHA-256.
  (void)chunk;
  (void)len;
  return false;
}

} // namespace tether::m5
