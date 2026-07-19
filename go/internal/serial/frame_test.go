package serial

import (
	"bytes"
	"testing"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		typ     FrameType
		payload []byte
	}{
		{"empty payload", FrameSetConfig, nil},
		{"short payload", FrameAck, []byte{0x01, 0x02, 0x03}},
		{"rx packet", FrameRxPacket, make([]byte, 255)},
		{"log line", FrameLog, []byte("tether bridge boot")},
		{"cad result", FrameCadResult, []byte{0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodeFrame(Frame{Type: tt.typ, Payload: tt.payload})
			if err != nil {
				t.Fatalf("EncodeFrame: %v", err)
			}
			dec := NewFrameDecoder()
			dec.Feed(encoded)
			got, ok := dec.Next()
			if !ok {
				t.Fatal("decoder returned no frame")
			}
			if got.Type != tt.typ {
				t.Errorf("type: got %d, want %d", got.Type, tt.typ)
			}
			if !bytes.Equal(got.Payload, tt.payload) {
				t.Errorf("payload: got %d bytes, want %d bytes", len(got.Payload), len(tt.payload))
			}
		})
	}
}

func TestDecodeStreaming(t *testing.T) {
	// Feed the encoded frame one byte at a time to verify the state
	// machine works across partial reads.
	payload := []byte("hello bridge")
	encoded, err := EncodeFrame(Frame{Type: FrameAck, Payload: payload})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	dec := NewFrameDecoder()
	var got Frame
	for i, b := range encoded {
		dec.Feed([]byte{b})
		f, ok := dec.Next()
		if ok {
			if i != len(encoded)-1 {
				t.Fatalf("got frame at byte %d, expected only at %d", i, len(encoded)-1)
			}
			got = f
		}
	}
	if got.Type != FrameAck || !bytes.Equal(got.Payload, payload) {
		t.Errorf("got %+v, want type=%d payload=%q", got, FrameAck, payload)
	}
}

func TestDecodeTwoFramesInOneFeed(t *testing.T) {
	f1, _ := EncodeFrame(Frame{Type: FrameAck, Payload: []byte("first")})
	f2, _ := EncodeFrame(Frame{Type: FrameRxPacket, Payload: []byte("second")})
	dec := NewFrameDecoder()
	dec.Feed(append(f1, f2...))
	got1, ok := dec.Next()
	if !ok || got1.Type != FrameAck || string(got1.Payload) != "first" {
		t.Fatalf("first frame: got %+v ok=%v", got1, ok)
	}
	got2, ok := dec.Next()
	if !ok || got2.Type != FrameRxPacket || string(got2.Payload) != "second" {
		t.Fatalf("second frame: got %+v ok=%v", got2, ok)
	}
}

func TestDecodeBadCRC(t *testing.T) {
	encoded, _ := EncodeFrame(Frame{Type: FrameAck, Payload: []byte("corrupt me")})
	// Flip a payload byte to corrupt the CRC.
	encoded[5] ^= 0xFF
	dec := NewFrameDecoder()
	dec.Feed(encoded)
	if _, ok := dec.Next(); ok {
		t.Fatal("decoder accepted a frame with bad CRC")
	}
}

func TestDecodeResyncAfterGarbage(t *testing.T) {
	// Garbage before a valid frame — the decoder should resync on
	// the magic bytes.
	valid, _ := EncodeFrame(Frame{Type: FrameLog, Payload: []byte("ok")})
	garbage := []byte{0x00, 0x11, 0x22, 0xAA, 0x00, 0x55}
	dec := NewFrameDecoder()
	dec.Feed(append(garbage, valid...))
	// Keep pulling until we get the valid frame.
	for {
		f, ok := dec.Next()
		if ok {
			if f.Type != FrameLog || string(f.Payload) != "ok" {
				t.Errorf("got %+v after garbage, want Log/ok", f)
			}
			return
		}
		// No frame yet; feed nothing and retry (the decoder needs
		// more bytes or is mid-resync). Since we already fed everything,
		// if no frame comes out we fail below.
		break
	}
	// The decoder consumed everything; the valid frame should be in
	// there. Feed nothing and try once more.
	dec.Feed(nil)
	f, ok := dec.Next()
	if !ok {
		t.Fatal("decoder failed to resync after garbage")
	}
	if f.Type != FrameLog || string(f.Payload) != "ok" {
		t.Errorf("got %+v, want Log/ok", f)
	}
}

func TestCRCCoversTypeLenPayload(t *testing.T) {
	// The CRC covers type + len + payload, not the magic bytes.
	// Verify by computing it manually.
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	encoded, _ := EncodeFrame(Frame{Type: FrameAck, Payload: payload})
	// encoded = [AA 55] [type] [lenLo lenHi] [payload...] [crcLo crcHi]
	crcInput := encoded[2 : len(encoded)-2]
	want := protocol.Crc16CCITT(crcInput)
	gotLo := encoded[len(encoded)-2]
	gotHi := encoded[len(encoded)-1]
	got := uint16(gotLo) | uint16(gotHi)<<8
	if got != want {
		t.Errorf("CRC: got %04X, want %04X", got, want)
	}
}
