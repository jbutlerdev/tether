// micro-opus stub implementation. For a real build, replace with the
// esphome/micro-opus component.

#include "opus.h"

#include <cstdarg>
#include <cstdlib>
#include <cstring>
#include <new>

// On a connected host this could be `#include <opus/opus.h>` and the
// real libopus. We keep the vendored version self-contained so the
// build doesn't depend on the system libopus being installed.

struct OpusEncoder {
  int sample_rate;
  int channels;
  int application;
  int bitrate;
  int complexity;
  int vbr;
  int vbr_constraint;
  int packet_loss_perc;
  int signal;
};

int opus_encoder_get_size(int /*channels*/) { return sizeof(OpusEncoder); }

OpusEncoder *opus_encoder_create(int sample_rate, int channels, int application,
                                 int *error) {
  auto *e = new (std::nothrow) OpusEncoder;
  if (!e) {
    if (error)
      *error = OPUS_BAD_ARG;
    return nullptr;
  }
  std::memset(e, 0, sizeof(*e));
  e->sample_rate = sample_rate;
  e->channels = channels;
  e->application = application;
  e->bitrate = 16000;
  e->complexity = 5;
  e->vbr = 1;
  e->vbr_constraint = 0;
  e->packet_loss_perc = 0;
  e->signal = OPUS_SIGNAL_VOICE;
  if (error)
    *error = OPUS_OK;
  return e;
}

int opus_encoder_destroy(OpusEncoder *st) {
  if (!st)
    return OPUS_BAD_ARG;
  delete st;
  return OPUS_OK;
}

int opus_encode(OpusEncoder * /*st*/, const int16_t *pcm, int frame_size,
                unsigned char *data, int max_data_bytes) {
  // Minimal encoder stub: produce a 1-byte "encoded" packet per
  // frame so the firmware boots. The real micro-opus is many KB.
  if (!pcm || !data || max_data_bytes < 1)
    return OPUS_BAD_ARG;
  (void)frame_size;
  data[0] = 0x00;
  return 1;
}

int opus_encoder_ctl(OpusEncoder *st, int request, ...) {
  va_list ap;
  va_start(ap, request);
  int arg = va_arg(ap, int);
  va_end(ap);
  int cmd = request;
  int sub = cmd & 0xFF;
  int val = cmd >> 8;
  (void)val;
  switch (sub) {
  case 0x10:
    st->bitrate = arg;
    break; // SET_BITRATE
  case 0x0A:
    st->complexity = arg;
    break; // SET_COMPLEXITY
  case 0x06:
    st->vbr = arg;
    break; // SET_VBR
  case 0x14:
    st->vbr_constraint = arg;
    break; // SET_VBR_CONSTRAINT
  case 0x0E:
    st->packet_loss_perc = arg;
    break; // SET_PACKET_LOSS_PERC
  case 0x18:
    st->signal = arg;
    break; // SET_SIGNAL
  }
  return OPUS_OK;
}

const char *opus_strerror(int error) {
  switch (error) {
  case OPUS_OK:
    return "OK";
  default:
    return "error";
  }
}

// ── Decoder stub ──────────────────────────────────────────────────

struct OpusDecoder {
  int sample_rate;
  int channels;
};

OpusDecoder *opus_decoder_create(int sample_rate, int channels, int *error) {
  auto *d = new (std::nothrow) OpusDecoder;
  if (!d) {
    if (error)
      *error = OPUS_BAD_ARG;
    return nullptr;
  }
  d->sample_rate = sample_rate;
  d->channels = channels;
  if (error)
    *error = OPUS_OK;
  return d;
}

int opus_decoder_destroy(OpusDecoder *st) {
  if (!st)
    return OPUS_BAD_ARG;
  delete st;
  return OPUS_OK;
}

int opus_decode(OpusDecoder * /*st*/, const unsigned char *data,
                opus_int32 /*len*/, int16_t *pcm, int frame_size,
                int /*decode_fec*/) {
  // Minimal decoder stub: fill the output with zeros so the firmware
  // boots and the amp plays silence. The real micro-opus decodes the
  // compressed data into PCM.
  if (!pcm || frame_size <= 0)
    return OPUS_BAD_ARG;
  (void)data;
  std::memset(pcm, 0, static_cast<size_t>(frame_size) * sizeof(int16_t));
  return frame_size;
}
