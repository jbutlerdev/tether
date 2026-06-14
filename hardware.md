### 1. Handheld Node
 * **Core Microcontroller & Transceiver:** Elecrow ThinkNode M5 LoRa Meshtastic transceiver (features the ESP32-S3 and onboard LoRa module).
 * **Audio Input:** INMP441 I2S MEMS Microphone Module / Adafruit I2S MEMS microphone breakout.
 * **Audio Output:** Adafruit Class D I2S Amplifier paired with a low-profile speaker.
 * **Storage:** MicroSD Card (operating over SPI).
### 2. PC Base Station Bridge
 * **Desktop PC:** Running the custom Go daemon for audio reassembly and routing.
 * **USB LoRa Bridge:** RAKwireless Mini Meshtastic Starter Kit (connected via USB/Serial to intercept the airwaves).
 