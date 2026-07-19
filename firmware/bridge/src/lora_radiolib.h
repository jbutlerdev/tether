// lora_radiolib.h — factory for the RadioLib SX1262 backend.
//
// This header is only included by main.cpp in the [env:rak4631]
// build (which has <Arduino.h> and <RadioLib.h>). The native host
// test build does not include it. The factory returns a RadioBackend
// shared_ptr so main.cpp can construct a LoRaRadio without seeing
// the RadioLibBackend class (which lives in an anonymous namespace
// in lora_radiolib.cpp).
#pragma once

#include <Arduino.h>
#include <SPI.h>

#include <memory>

#include "lora.h"

namespace tether::bridge {

// MakeRadioLibBackend creates a RadioLib-backed RadioBackend for the
// RAK4631 (SX1262 on hardware SPI). The pins are the WisBlock
// standard: NSS=SS, DIO1=DIO1, RESET=NRST, BUSY=BUSY.
std::shared_ptr<RadioBackend>
MakeRadioLibBackend(uint32_t pin_nss, uint32_t pin_reset, uint32_t pin_dio1,
                    uint32_t pin_busy, SPIClass &spi);

} // namespace tether::bridge
