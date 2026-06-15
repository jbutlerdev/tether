// frequency_hopping.h — v2 frequency hopping hook. See plan.md
// §10.4 (Task 9.4).
//
// v2: frequency hopping. When v2 lands, the radio will hop
// across the US915 channel set between transmissions to
// reduce the impact of narrow-band interference. The hook
// point is ChooseNextChannel: today it returns the input
// channel unchanged; v2 will compute the next hop from a
// shared secret and a frame counter.
//
// The stub is unit-tested by test_frequency_hopping.cpp on the
// host to assert the symbol exists and that v1 returns the
// input unchanged. v2 will invert the test: the function must
// return a different channel for most (channel, counter)
// pairs.

#pragma once

#include <cstdint>

namespace tether::m5 {

// kUs915NumChannels mirrors the kUs915NumChannels in
// lora_sx1262.h. It is re-declared here so callers that only
// need the frequency-hopping hook do not have to include the
// full radio header.
inline constexpr uint8_t kHopNumChannels = 64;

// ChooseNextChannel is the v2 frequency-hopping hook. v1
// returns `current_channel` unchanged. v2 will compute:
//
//   next = (current_channel + hop_sequence(counter)) mod
//          kHopNumChannels
//
// where hop_sequence is a stream cipher seeded by the
// per-conversation HKDF output (see internal/crypto/hkdf.go on
// the Go side and components/aes_link on the M5 side). The
// counter is the per-conversation monotonic message id; both
// ends increment it on every transmit so the hop sequence is
// in lockstep.
//
// The function is pure: it does not touch SPI or the radio
// backend. The radio task calls ChooseNextChannel and then
// SetChannel(chosen) to actually move the carrier.
//
// v2 callers will not need to change: ChooseNextChannel
// keeps the same signature; the v2 build returns a different
// channel for the same input.
inline uint8_t ChooseNextChannel(uint8_t current_channel, uint32_t counter) {
  // v2: frequency hopping. Replace this body with a real
  // stream-cipher-driven hop:
  //   - Derive a one-byte offset from HKDF(master_key, "tether.v2.hop").
  //   - XOR with the low byte of the counter.
  //   - Add to current_channel mod kHopNumChannels.
  (void)counter;
  return current_channel;
}

} // namespace tether::m5
