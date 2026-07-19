// micro-opus stub — minimal vendored header so the M5 firmware
// builds without the Espressif component registry. The real
// micro-opus component is a fork of libopus optimized for ESP32.
//
// On a connected build host, replace this stub with the real
// esphome/micro-opus component by re-enabling the dependency in
// main/idf_component.yml.
//
// This stub exposes the libopus API subset that opus_enc.cpp uses:
// opus_encoder_create, opus_encoder_destroy, opus_encode, and
// opus_encoder_ctl. It links against system libopus when the host
// toolchain supports it, or returns stubbed errors otherwise.
#pragma once

#include <cstddef>
#include <cstdint>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct OpusEncoder OpusEncoder;
typedef struct OpusDecoder OpusDecoder;

#define OPUS_OK 0
#define OPUS_BAD_ARG -1
#define OPUS_APPLICATION_VOIP 2048
#define OPUS_SET_BITRATE(x) (4000 + ((x)&0xFFFF))
#define OPUS_SET_COMPLEXITY(x) (4010 + ((x)&0xFF))
#define OPUS_SET_VBR(x) (4006 + ((x)&0xFF))
#define OPUS_SET_VBR_CONSTRAINT(x) (4020 + ((x)&0xFF))
#define OPUS_SET_PACKET_LOSS_PERC(x) (4014 + ((x)&0xFF))
#define OPUS_SET_SIGNAL(x) (4024 + ((x)&0xFF))
#define OPUS_SIGNAL_VOICE 3001

int opus_encoder_get_size(int channels);
OpusEncoder *opus_encoder_create(int sample_rate, int channels, int application,
                                 int *error);
int opus_encoder_destroy(OpusEncoder *st);
int opus_encode(OpusEncoder *st, const int16_t *pcm, int frame_size,
                unsigned char *data, int max_data_bytes);
int opus_encoder_ctl(OpusEncoder *st, int request, ...);
const char *opus_strerror(int error);

OpusDecoder *opus_decoder_create(int sample_rate, int channels, int *error);
int opus_decoder_destroy(OpusDecoder *st);
int opus_decode(OpusDecoder *st, const unsigned char *data, opus_int32 len,
                int16_t *pcm, int frame_size, int decode_fec);

#ifdef __cplusplus
}
#endif
