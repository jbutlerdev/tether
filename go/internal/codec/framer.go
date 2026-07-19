// Package codec: framer.go — length-delimited Opus frame packaging.
//
// The wire format for Tether audio is a concatenation of Opus frames
// packaged with a 2-byte little-endian length prefix per frame:
//
//	[len0_lo, len0_hi, frame0..., len1_lo, len1_hi, frame1..., ...]
//
// This allows the receiver to split the reassembled blob back into
// individual Opus frames and decode each one. Without the length
// prefix, opus_decode decodes only the first frame in a concatenated
// blob (the Mock codec is an identity transform, so the issue is
// invisible without real Opus — but the production daemon uses
// CgoCodec which calls opus_decode once per call).
//
// The Framer is used by the forge pipeline (TTS encode + incoming
// audio decode), the e2e simulator, and the tetherd daemon. The M5
// firmware implements the same 2-byte length prefix in its
// audio_capture → radio drain path.
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Framer wraps an Opus codec and provides length-delimited
// encode/decode for multi-frame blobs. It is safe for concurrent
// use only if the underlying Opus codec is.
type Framer struct {
	opus Opus
}

// NewFramer creates a Framer wrapping the given Opus codec.
func NewFramer(opus Opus) *Framer {
	return &Framer{opus: opus}
}

// EncodeBlob encodes pcm into a length-delimited blob of Opus frames.
// The pcm is split into FrameSize chunks; each chunk is encoded and
// prefixed with a 2-byte little-endian length. The final partial frame
// is zero-padded. An empty pcm input returns nil (no frames).
func (f *Framer) EncodeBlob(pcm []int16) ([]byte, error) {
	frame := f.opus.FrameSize()
	if frame <= 0 {
		return nil, errors.New("codec: frame size <= 0")
	}
	if len(pcm) == 0 {
		return nil, nil
	}
	var out []byte
	for off := 0; off < len(pcm); off += frame {
		end := off + frame
		var framePCM []int16
		if end > len(pcm) {
			// Pad with zeros for the trailing partial frame.
			pad := make([]int16, end-len(pcm))
			framePCM = append(append([]int16(nil), pcm[off:]...), pad...)
		} else {
			framePCM = pcm[off:end]
		}
		b, err := f.opus.Encode(framePCM)
		if err != nil {
			return nil, fmt.Errorf("codec: encode frame: %w", err)
		}
		var lenBuf [2]byte
		binary.LittleEndian.PutUint16(lenBuf[:], uint16(len(b)))
		out = append(out, lenBuf[:]...)
		out = append(out, b...)
	}
	return out, nil
}

// DecodeBlob decodes a length-delimited blob back into PCM samples.
// Each 2-byte little-endian length prefix indicates the size of the
// next Opus frame. An empty blob returns nil.
func (f *Framer) DecodeBlob(blob []byte) ([]int16, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	var out []int16
	for len(blob) > 0 {
		if len(blob) < 2 {
			return nil, errors.New("codec: truncated length prefix")
		}
		frameLen := int(binary.LittleEndian.Uint16(blob[:2]))
		blob = blob[2:]
		if frameLen == 0 {
			continue
		}
		if len(blob) < frameLen {
			return nil, fmt.Errorf("codec: truncated frame: need %d, have %d", frameLen, len(blob))
		}
		pcm, err := f.opus.Decode(blob[:frameLen])
		if err != nil {
			return nil, fmt.Errorf("codec: decode frame: %w", err)
		}
		out = append(out, pcm...)
		blob = blob[frameLen:]
	}
	return out, nil
}
