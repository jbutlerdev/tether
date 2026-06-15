// RadioLib stub — minimal vendored header so the M5 firmware builds
// without internet access. Replace with the real jgromes/RadioLib
// component (>= 6.0.0) for production.
//
// This stub exposes only the symbols the M5 lora_sx1262 component
// uses: SX1262 class with begin(), setFrequency(), transmit(),
// receive(), startChannelScan(), sleep(), standby(), and getPacketLength().
#pragma once

#include <cstddef>
#include <cstdint>

#define RADIOLIB_ERR_NONE 0
#define RADIOLIB_ERR_UNKNOWN 1
#define RADIOLIB_PREAMBLE_DETECTED -2

class Module;

class Module {
public:
  Module(int8_t cs, int8_t irq, int8_t rst, int8_t busy)
      : cs_(cs), irq_(irq), rst_(rst), busy_(busy) {}
  int8_t cs_;
  int8_t irq_;
  int8_t rst_;
  int8_t busy_;
};

class SX1262 {
public:
  explicit SX1262(Module *mod) : mod_(mod) {}
  int begin(float bw, int8_t sf, uint8_t cr, uint8_t sync_word, int8_t pwr,
            uint16_t preamble, float tcxo_v, bool ldo);
  int setFrequency(float mhz);
  int transmit(const uint8_t *data, size_t len);
  int16_t receive(uint8_t *buf, size_t len, int flags);
  size_t getPacketLength();
  int16_t startChannelScan();
  int sleep();
  int standby();
  // We don't actually own mod_; the caller's Module outlives us.
  Module *mod_;
};
