// frame.go — Go mirror of the RAK4631 bridge frame protocol.
//
// The bridge speaks a line-framed binary protocol over USB-Serial at
// 921 600 baud (firmware/bridge/src/frame.h). Every frame is:
//
//	0xAA 0x55  <type:1>  <len_lo:1>  <len_hi:1>  <payload:len>  <crc_lo:1>  <crc_hi:1>
//
// The CRC is CRC-16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no
// reflection, no XOR-out) over the bytes from <type> through the
// last payload byte — the same algorithm as the LoRa wire header
// (protocol.Crc16CCITT). We reuse that function so there is exactly
// one CRC implementation in the codebase.
//
// Frame types (bridge → Go are RX-side; Go → bridge are TX-side):
//
//	kTxDone    0x01  bridge→Go   a LoRa TX completed
//	kRxPacket  0x02  bridge→Go   a LoRa packet was received (payload = raw air bytes)
//	kAck       0x03  Go→bridge   transmit this payload over LoRa (payload = raw air bytes)
//	kCadResult 0x04  bridge→Go   CAD result (payload = 1 byte: 0x01=busy, 0x00=clear)
//	kSetConfig 0x10  Go→bridge   configure the LoRa radio (payload = [sf,bw,cr,power,sync])
//	kLog       0x80  bridge→Go   a log line from the bridge (payload = UTF-8 text)
//	kError     0xFF  bridge→Go   an error from the bridge
//
// The bridge is a pass-through: it forwards whatever bytes the Go
// side sends as kAck frames directly to the SX1262 TX FIFO, and
// forwards whatever the SX1262 RX FIFO produces as kRxPacket frames.
// The Go side serializes Envelopes with protocol.Encode (the 34-byte
// fixed header) before wrapping them in a kAck frame; the M5
// firmware decodes the same 34-byte header on the other end.
package serial

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
)

// Magic bytes mark the start of every frame.
const (
	Magic0 = 0xAA
	Magic1 = 0x55
)

// MaxFrameSize matches the bridge's kMaxFrameSize (256 bytes). A
// full LoRa packet (255 bytes) + 7 bytes of framing = 262, but the
// bridge caps at 256; the 34-byte header + 221-byte payload = 255
// fits with room to spare.
const MaxFrameSize = 256

// FrameType is the 1-byte type field.
type FrameType uint8

const (
	FrameTxDone    FrameType = 0x01
	FrameRxPacket  FrameType = 0x02
	FrameAck       FrameType = 0x03 // Go→bridge: TX this payload over LoRa
	FrameCadResult FrameType = 0x04
	FrameSetConfig FrameType = 0x10
	FrameLog       FrameType = 0x80
	FrameError     FrameType = 0xFF
)

// Frame is a decoded bridge frame.
type Frame struct {
	Type    FrameType
	Payload []byte
}

// EncodeFrame serializes a Frame to bytes for transmission over the
// serial port. Returns an error if the payload exceeds 65535 bytes
// (the 2-byte LE length field's max).
func EncodeFrame(f Frame) ([]byte, error) {
	if len(f.Payload) > 0xFFFF {
		return nil, fmt.Errorf("serial: frame payload too large (%d bytes)", len(f.Payload))
	}
	// Header: magic(2) + type(1) + len(2) = 5 bytes.
	// Trailer: crc(2) = 2 bytes.
	out := make([]byte, 0, 5+len(f.Payload)+2)
	out = append(out, Magic0, Magic1)
	out = append(out, byte(f.Type))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(f.Payload)))
	out = append(out, f.Payload...)
	// CRC covers type + len + payload (bytes 2..4+len, i.e. everything
	// after the magic and before the CRC).
	crcStart := 2 // skip magic
	crcEnd := len(out)
	crc := protocol.Crc16CCITT(out[crcStart:crcEnd])
	out = binary.LittleEndian.AppendUint16(out, crc)
	return out, nil
}

// FrameDecoder is a streaming decoder that accumulates bytes from the
// serial port and emits complete frames. It mirrors the bridge's
// FrameDecoder state machine.
type FrameDecoder struct {
	state    decodeState
	frame    Frame
	wantLen  int  // remaining payload bytes to read
	crcLo    byte // low byte of the received CRC (saved across 2 states)
	scratch  []byte
}

type decodeState uint8

const (
	stateMagic0 decodeState = iota
	stateMagic1
	stateType
	stateLenLo
	stateLenHi
	statePayload
	stateCRCLo
	stateCRCHi
)

// NewFrameDecoder returns a fresh streaming decoder.
func NewFrameDecoder() *FrameDecoder {
	return &FrameDecoder{state: stateMagic0}
}

// Feed accumulates bytes. Call Next() repeatedly after Feed to pull
// out any complete frames.
func (d *FrameDecoder) Feed(buf []byte) {
	d.scratch = append(d.scratch, buf...)
}

// Next returns the next complete frame, or (Frame{}, false) if no
// full frame is available yet. A frame with a bad CRC is silently
// dropped (the decoder resyncs on the next magic sequence).
func (d *FrameDecoder) Next() (Frame, bool) {
	for len(d.scratch) > 0 {
		b := d.scratch[0]
		d.scratch = d.scratch[1:]
		switch d.state {
		case stateMagic0:
			if b == Magic0 {
				d.state = stateMagic1
			}
		case stateMagic1:
			if b == Magic1 {
				d.state = stateType
			} else if b == Magic0 {
				// Stay in stateMagic1 (re-sync: 0xAA 0xAA 0x55).
			} else {
				d.state = stateMagic0
			}
		case stateType:
			d.frame = Frame{Type: FrameType(b)}
			d.state = stateLenLo
		case stateLenLo:
			d.wantLen = int(b)
			d.state = stateLenHi
		case stateLenHi:
			d.wantLen |= int(b) << 8
			if d.wantLen == 0 {
				d.state = stateCRCLo
			} else if d.wantLen > MaxFrameSize {
				// Oversized frame — resync.
				d.state = stateMagic0
			} else {
				d.frame.Payload = make([]byte, 0, d.wantLen)
				d.state = statePayload
			}
		case statePayload:
			d.frame.Payload = append(d.frame.Payload, b)
			d.wantLen--
			if d.wantLen == 0 {
				d.state = stateCRCLo
			}
		case stateCRCLo:
			d.crcLo = b
			d.state = stateCRCHi
		case stateCRCHi:
			got := binary.LittleEndian.Uint16([]byte{d.crcLo, b})
			// Recompute the CRC over type+len+payload.
			crcInput := make([]byte, 0, 3+len(d.frame.Payload))
			crcInput = append(crcInput, byte(d.frame.Type))
			crcInput = binary.LittleEndian.AppendUint16(crcInput, uint16(len(d.frame.Payload)))
			crcInput = append(crcInput, d.frame.Payload...)
			want := protocol.Crc16CCITT(crcInput)
			d.state = stateMagic0
			if got == want {
				return d.frame, true
			}
			// Bad CRC — drop the frame and resync.
		}
	}
	return Frame{}, false
}

// ErrSerialClosed is returned by Transport.Receive when the
// transport's serial port is closed.
var ErrSerialClosed = errors.New("serial: transport closed")
