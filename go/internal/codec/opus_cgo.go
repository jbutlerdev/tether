//go:build opus

// opus_cgo.go — real Opus codec via libopus (cgo).
//
// Build with `-tags opus` and libopus installed (e.g. apt install
// libopus-dev). The default build (no tag) uses the Mock identity
// codec in opus.go; this file provides the production codec.
//
// Settings (research.md §3.2):
//
//	application        = OPUS_APPLICATION_VOIP
//	bitrate            = 16000
//	sampling rate      = 8000
//	frame size         = 20 ms (160 samples)
//	complexity         = 5
//	vbr                = 1
//	vbr_constraint     = 0
//	force_channels     = 1 (mono)
//	use_phase_inversion= 0
package codec

/*
#cgo pkg-config: opus
#include <opus/opus.h>

// Wrapper to work around cgo's inability to pass a pointer to a
// pointer that is itself a struct member.
static int create_encoder(int fs, int channels, int application, OpusEncoder **out) {
    int err;
    *out = opus_encoder_create(fs, channels, application, &err);
    return err;
}
static int create_decoder(int fs, int channels, OpusDecoder **out) {
    int err;
    *out = opus_decoder_create(fs, channels, &err);
    return err;
}

// CTL wrappers — the OPUS_SET_* macros are not simple constants,
// so cgo cannot resolve them directly. These thin wrappers bridge
// the gap.
static int set_bitrate(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_BITRATE(v));
}
static int set_complexity(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_COMPLEXITY(v));
}
static int set_vbr(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_VBR(v));
}
static int set_vbr_constraint(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(v));
}
static int set_force_channels(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_FORCE_CHANNELS(v));
}
static int set_phase_inversion_disabled(OpusEncoder *enc, opus_int32 v) {
    return opus_encoder_ctl(enc, OPUS_SET_PHASE_INVERSION_DISABLED(v));
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// CgoCodec is a real Opus encoder/decoder backed by libopus.
// It implements the Opus interface. Construct with NewCgoCodec.
type CgoCodec struct {
	enc *C.OpusEncoder
	dec *C.OpusDecoder

	// encodeBuf is reused across Encode calls to avoid per-frame
	// allocation. Protected by encMu (Encode is not concurrent).
	encMu     int32 // 0 = free, 1 = in use; simple guard
	encodeBuf []byte
}

// NewCgoCodec creates a real Opus encoder + decoder at 8 kHz / mono /
// VOIP / 16 kbps / complexity 5. Call Close to free the C state.
func NewCgoCodec() (*CgoCodec, error) {
	var enc *C.OpusEncoder
	err := C.create_encoder(C.int(SampleRate), 1, C.OPUS_APPLICATION_VOIP, &enc)
	if err != C.OPUS_OK || enc == nil {
		return nil, fmt.Errorf("codec: opus_encoder_create: %s", C.GoString(C.opus_strerror(err)))
	}

	// Configure per research.md §3.2.
	if r := C.set_bitrate(enc, C.opus_int32(16000)); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set bitrate: %s", C.GoString(C.opus_strerror(r)))
	}
	if r := C.set_complexity(enc, C.opus_int32(5)); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set complexity: %s", C.GoString(C.opus_strerror(r)))
	}
	if r := C.set_vbr(enc, 1); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set vbr: %s", C.GoString(C.opus_strerror(r)))
	}
	if r := C.set_vbr_constraint(enc, 0); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set vbr_constraint: %s", C.GoString(C.opus_strerror(r)))
	}
	if r := C.set_force_channels(enc, 1); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set force_channels: %s", C.GoString(C.opus_strerror(r)))
	}
	if r := C.set_phase_inversion_disabled(enc, 1); r != C.OPUS_OK {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: set phase_inversion: %s", C.GoString(C.opus_strerror(r)))
	}

	var dec *C.OpusDecoder
	derr := C.create_decoder(C.int(SampleRate), 1, &dec)
	if derr != C.OPUS_OK || dec == nil {
		C.opus_encoder_destroy(enc)
		return nil, fmt.Errorf("codec: opus_decoder_create: %s", C.GoString(C.opus_strerror(derr)))
	}

	return &CgoCodec{
		enc:       enc,
		dec:       dec,
		encodeBuf: make([]byte, 4000), // max opus frame size is 1276 bytes; 4 KB is safe
	}, nil
}

// FrameSize returns FrameSize (160 samples = 20 ms at 8 kHz).
func (c *CgoCodec) FrameSize() int { return FrameSize }

// SampleRate returns SampleRate (8000 Hz).
func (c *CgoCodec) SampleRate() int { return SampleRate }

// Encode encodes a PCM frame of exactly FrameSize int16 samples into
// Opus bytes. The output length varies (VBR: typically 10–60 bytes
// per frame at 16 kbps).
func (c *CgoCodec) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != FrameSize {
		return nil, fmt.Errorf("codec: encode: got %d samples, want %d", len(pcm), FrameSize)
	}
	if c.enc == nil {
		return nil, errors.New("codec: encode on closed codec")
	}
	n := C.opus_encode(
		c.enc,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])),
		C.int(FrameSize),
		(*C.uchar)(unsafe.Pointer(&c.encodeBuf[0])),
		C.opus_int32(len(c.encodeBuf)),
	)
	if n < 0 {
		return nil, fmt.Errorf("codec: opus_encode: %s", C.GoString(C.opus_strerror(C.int(n))))
	}
	// Copy out — the caller may outlive encodeBuf.
	out := make([]byte, int(n))
	copy(out, c.encodeBuf[:int(n)])
	return out, nil
}

// Decode decodes Opus bytes back into PCM int16 samples. Returns up
// to FrameSize samples (20 ms at 8 kHz).
func (c *CgoCodec) Decode(opus []byte) ([]int16, error) {
	if len(opus) == 0 {
		return nil, nil
	}
	if c.dec == nil {
		return nil, errors.New("codec: decode on closed codec")
	}
	pcm := make([]int16, FrameSize)
	// opus_decode wants a pointer to the compressed data; if the
	// slice is on the Go heap, the pointer is valid for the duration
	// of the call (cgo pins it).
	n := C.opus_decode(
		c.dec,
		(*C.uchar)(unsafe.Pointer(&opus[0])),
		C.opus_int32(len(opus)),
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])),
		C.int(FrameSize),
		0, // decode_fec = 0
	)
	if n < 0 {
		return nil, fmt.Errorf("codec: opus_decode: %s", C.GoString(C.opus_strerror(C.int(n))))
	}
	return pcm[:int(n)], nil
}

// Close frees the C encoder and decoder state. Idempotent.
func (c *CgoCodec) Close() error {
	if c.enc != nil {
		C.opus_encoder_destroy(c.enc)
		c.enc = nil
	}
	if c.dec != nil {
		C.opus_decoder_destroy(c.dec)
		c.dec = nil
	}
	return nil
}

// Compile-time check.
var _ Opus = (*CgoCodec)(nil)
