// RadioLib stub implementation.
#include "RadioLib.h"

int SX1262::begin(float /*bw*/, int8_t /*sf*/, uint8_t /*cr*/,
                  uint8_t /*sync_word*/, int8_t /*pwr*/, uint16_t /*preamble*/,
                  float /*tcxo*/, bool /*ldo*/) {
  return RADIOLIB_ERR_NONE;
}
int SX1262::setFrequency(float /*mhz*/) { return RADIOLIB_ERR_NONE; }
int SX1262::transmit(const uint8_t * /*data*/, size_t /*len*/) {
  return RADIOLIB_ERR_NONE;
}
int16_t SX1262::receive(uint8_t * /*buf*/, size_t /*len*/, int /*flags*/) {
  return RADIOLIB_ERR_NONE;
}
size_t SX1262::getPacketLength() { return 0; }
int16_t SX1262::startChannelScan() { return RADIOLIB_PREAMBLE_DETECTED; }
int SX1262::sleep() { return RADIOLIB_ERR_NONE; }
int SX1262::standby() { return RADIOLIB_ERR_NONE; }
