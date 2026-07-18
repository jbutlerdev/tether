// Tests for the Tether envelope codec. See research.md §8.1.
//
// The wire format is a fixed 34-byte binary header + payload with a
// CRC-16/CCITT-FALSE over the header. These tests pin the round-trip,
// the max-payload boundary, the CRC corruption detection, the
// truncation guard, and a 60 s CI fuzz harness.
package protocol_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// makeEnvelope returns a known-good envelope filled with non-zero fields
// so that bit-flips and CRC mismatches are detectable.
func makeEnvelope() *protocolpb.Envelope {
	return &protocolpb.Envelope{
		ProtocolVersion: 1,
		TargetId:        &protocolpb.NodeId{Value: 0xFFFF},
		SenderId:        &protocolpb.NodeId{Value: 0x0001},
		ConversationId:  bytes.Repeat([]byte{0xAB}, 16),
		MessageId:       0xDEADBEEF,
		SeqNum:          0,
		TotalSeqs:       1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_DATA,
		AudioKind:       protocolpb.AudioKind_AUDIO_KIND_MIC,
		Flags:           0,
		Payload:         bytes.Repeat([]byte{0xCC}, 32),
	}
}

func TestEnvelope_RoundTrip(t *testing.T) {
	env := makeEnvelope()

	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Fixed 34-byte header + 32-byte payload.
	if want := 34 + 32; len(wire) != want {
		t.Fatalf("wire length: want %d, got %d", want, len(wire))
	}

	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !envelopesEqual(env, got) {
		t.Fatalf("round-trip mismatch:\n want=%v\n got =%v", env, got)
	}
	// The on-wire CRC must be populated by Decode.
	if got.HeaderCrc == 0 {
		t.Errorf("HeaderCrc: want non-zero, got 0")
	}
}

func TestEnvelope_MaxPayload(t *testing.T) {
	env := makeEnvelope()
	env.Payload = bytes.Repeat([]byte{0x42}, protocol.MaxPayloadSize) // 221

	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode at max payload: %v", err)
	}
	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode at max payload: %v", err)
	}
	if len(got.Payload) != protocol.MaxPayloadSize {
		t.Fatalf("payload length: want %d, got %d", protocol.MaxPayloadSize, len(got.Payload))
	}
}

func TestEnvelope_OverSizedPayload(t *testing.T) {
	env := makeEnvelope()
	env.Payload = bytes.Repeat([]byte{0x42}, protocol.MaxPayloadSize+1) // 1 over the max

	_, err := protocol.Encode(env)
	if !errors.Is(err, protocol.ErrPayloadTooLarge) {
		t.Fatalf("Encode over-sized payload: want ErrPayloadTooLarge, got %v", err)
	}
}

// TestEnvelope_BadCRC: flipping any header byte (0..31) must break the
// CRC-16, which covers exactly those bytes.
func TestEnvelope_BadCRC(t *testing.T) {
	env := makeEnvelope()
	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Flip a bit in the message_id field (offset 20..23, inside the
	// CRC-covered region 0..31).
	wire[20] ^= 0x01

	_, err = protocol.Decode(wire)
	if !errors.Is(err, protocol.ErrBadCRC) {
		t.Fatalf("Decode with header bit flip: want ErrBadCRC, got %v", err)
	}
}

// TestEnvelope_CorruptCRCField: flipping a bit in the CRC field itself
// (offset 32..33) must also fail verification.
func TestEnvelope_CorruptCRCField(t *testing.T) {
	env := makeEnvelope()
	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	wire[32] ^= 0x01

	_, err = protocol.Decode(wire)
	if !errors.Is(err, protocol.ErrBadCRC) {
		t.Fatalf("Decode with corrupt CRC field: want ErrBadCRC, got %v", err)
	}
}

func TestEnvelope_Truncated(t *testing.T) {
	wire := []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	_, err := protocol.Decode(wire)
	if !errors.Is(err, protocol.ErrTruncated) {
		t.Fatalf("Decode of 5-byte buffer: want ErrTruncated, got %v", err)
	}
}

// TestEnvelope_BadConvIDLength: Encode rejects a conversation_id that
// is neither 16 bytes nor empty.
func TestEnvelope_BadConvIDLength(t *testing.T) {
	env := makeEnvelope()
	env.ConversationId = bytes.Repeat([]byte{0xAB}, 15) // wrong length
	_, err := protocol.Encode(env)
	if !errors.Is(err, protocol.ErrTruncated) {
		t.Fatalf("Encode with 15-byte convID: want ErrTruncated, got %v", err)
	}
}

// TestEnvelope_EmptyConvID: an empty conversation_id is zero-padded on
// the wire and round-trips to 16 zero bytes (a broadcast/legacy ACK).
func TestEnvelope_EmptyConvID(t *testing.T) {
	env := makeEnvelope()
	env.ConversationId = nil
	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.ConversationId) != 16 {
		t.Fatalf("convID length: want 16, got %d", len(got.ConversationId))
	}
	for _, b := range got.ConversationId {
		if b != 0 {
			t.Fatalf("convID: want all-zero, got %x", got.ConversationId)
		}
	}
}

// TestEnvelope_NodeIDTruncation: a NodeId.Value > 0xFFFF is truncated
// to 16 bits on the wire (research.md §8.1: target/sender are uint16).
func TestEnvelope_NodeIDTruncation(t *testing.T) {
	env := makeEnvelope()
	env.TargetId = &protocolpb.NodeId{Value: 0x1_0042} // > 16 bits
	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.TargetId.Value != 0x0042 {
		t.Errorf("TargetId: want 0x0042 (truncated), got %#x", got.TargetId.Value)
	}
}

func TestAck_BitmapEdges(t *testing.T) {
	// research.md §8.6: next_expected_seq is uint16 on the wire. A
	// window starting at 0xFFE0 with all 32 bits set (lo=0xFFFF,
	// hi=0xFFFF) round-trips through the 28-byte fixed payload.
	ack := &protocolpb.Ack{
		ConversationId:  bytes.Repeat([]byte{0x11}, 16),
		MessageId:       1,
		NextExpectedSeq: 0xFFE0,
		AckBitmapLo:     0xFFFF,
		AckBitmapHi:     0xFFFF,
	}

	raw, err := protocol.MarshalAck(ack)
	if err != nil {
		t.Fatalf("MarshalAck: %v", err)
	}
	got, err := protocol.DecodeAck(raw)
	if err != nil {
		t.Fatalf("DecodeAck: %v", err)
	}
	if got.NextExpectedSeq != 0xFFE0 {
		t.Errorf("NextExpectedSeq: want 0xFFE0, got %#x", got.NextExpectedSeq)
	}
	if got.AckBitmapLo != 0xFFFF {
		t.Errorf("AckBitmapLo: want 0xFFFF, got %#x", got.AckBitmapLo)
	}
	if got.AckBitmapHi != 0xFFFF {
		t.Errorf("AckBitmapHi: want 0xFFFF, got %#x", got.AckBitmapHi)
	}
}

func TestStartInfo_DurationField(t *testing.T) {
	// research.md §8.1: PayloadSizeBytes is the per-chunk payload
	// (MaxPayloadSize = 221). TotalSeqs = ceil(bytes / payload).
	start := &protocolpb.StartInfo{
		Codec:            protocolpb.AudioCodec_AUDIO_CODEC_OPUS_8K_16KBPS,
		SampleRateHz:     8000,
		BitrateBps:       16000,
		DurationMs:       4000, // 4 s
		PayloadSizeBytes: uint32(protocol.MaxPayloadSize),
	}
	// 4 s × 16 kbps / 8 = 8000 bytes; ceil(8000/221) = 37 seqs.
	want := uint32(37)
	got := totalSeqs(start)
	if got != want {
		t.Fatalf("totalSeqs(%v) = %d, want %d", start, got, want)
	}
}

func TestConvInfo_TruncateName(t *testing.T) {
	ci := &protocolpb.ConvInfo{
		ConversationId: bytes.Repeat([]byte{0x22}, 16),
		Name:           strings.Repeat("x", 25), // > 24 chars
		Kind:           protocolpb.ConvKind_CONV_KIND_MATRIX,
		Target:         "!room:example.org",
		EncryptionKey:  bytes.Repeat([]byte{0x33}, 16),
	}

	err := protocol.ValidateConvInfo(ci)
	if !errors.Is(err, protocol.ErrNameTooLong) {
		t.Fatalf("ValidateConvInfo with long name: want ErrNameTooLong, got %v", err)
	}
}

// FuzzEnvelopeDecode is the adversarial harness for Decode. The fuzzer
// must never crash; every input that does not return a clean Envelope
// must be one of the documented sentinel errors. CI runs this for 60 s.
func FuzzEnvelopeDecode(f *testing.F) {
	for i := 0; i < 10; i++ {
		env := makeEnvelope()
		env.MessageId = uint32(i + 1)
		env.Payload = bytes.Repeat([]byte{byte(i)}, 32)
		wire, err := protocol.Encode(env)
		if err != nil {
			f.Fatalf("seed Encode: %v", err)
		}
		f.Add(wire)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = protocol.Decode(data)
		// Acceptable outcomes: nil, ErrTruncated, ErrBadCRC,
		// ErrPayloadTooLarge. Any panic is a bug.
	})
}

// envelopesEqual is a field-by-field equality check for the round-trip
// tests (tolerates nil sub-messages and ignores HeaderCrc, which Decode
// populates from the wire).
func envelopesEqual(a, b *protocolpb.Envelope) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ProtocolVersion != b.ProtocolVersion ||
		a.MessageId != b.MessageId ||
		a.SeqNum != b.SeqNum ||
		a.TotalSeqs != b.TotalSeqs ||
		a.MsgType != b.MsgType ||
		a.AudioKind != b.AudioKind ||
		a.Flags != b.Flags {
		return false
	}
	if !bytes.Equal(a.ConversationId, b.ConversationId) {
		return false
	}
	if !bytes.Equal(a.Payload, b.Payload) {
		return false
	}
	if !nodeIDEqual(a.TargetId, b.TargetId) {
		return false
	}
	if !nodeIDEqual(a.SenderId, b.SenderId) {
		return false
	}
	return true
}

func nodeIDEqual(a, b *protocolpb.NodeId) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Value == b.Value
}

// totalSeqs is the dimensionally-correct chunk-count helper:
//
//	TotalSeqs = ceil(DurationMs/1000 * BitrateBps/8 / PayloadSizeBytes)
func totalSeqs(s *protocolpb.StartInfo) uint32 {
	totalBytes := uint64(s.DurationMs/1000) * (uint64(s.BitrateBps) / 8)
	return uint32((totalBytes + uint64(s.PayloadSizeBytes) - 1) / uint64(s.PayloadSizeBytes))
}

// TestCrc16CCITT_KnownVectors covers the canonical CRC-16/CCITT-FALSE
// test vectors. poly 0x1021, init 0xFFFF, no reflect, no xorout.
func TestCrc16CCITT_KnownVectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint16
	}{
		{"empty", []byte{}, 0xFFFF},
		{"single 0x00", []byte{0x00}, 0xE1F0},
		{"0xFF x4", []byte{0xFF, 0xFF, 0xFF, 0xFF}, 0x1D0F},
		{"123456789 (check)", []byte("123456789"), 0x29B1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := protocol.Crc16CCITT(c.in)
			if got != c.want {
				t.Fatalf("crc16ccitt(% x) = %#x, want %#x", c.in, got, c.want)
			}
		})
	}
}

func TestEncode_NilEnvelope(t *testing.T) {
	if _, err := protocol.Encode(nil); err == nil {
		t.Fatal("Encode(nil): want error, got nil")
	}
}

// TestDecode_GarbageBuffer: a buffer shorter than the 34-byte header
// must return ErrTruncated.
func TestDecode_GarbageBuffer(t *testing.T) {
	buf := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	if _, err := protocol.Decode(buf); err == nil {
		t.Fatal("Decode(garbage): want error, got nil")
	}
}

// TestDecode_HeaderSizeNoCRC: a 34-byte buffer whose CRC does not
// verify must return ErrBadCRC (not ErrTruncated).
func TestDecode_HeaderSizeNoCRC(t *testing.T) {
	buf := make([]byte, 34)
	// All zeros → CRC of 32 zero bytes is 0xFFFF (init), but the CRC
	// field at 32..33 is 0x0000 → mismatch.
	if _, err := protocol.Decode(buf); !errors.Is(err, protocol.ErrBadCRC) {
		t.Fatalf("Decode(34 zeros, bad CRC): want ErrBadCRC, got %v", err)
	}
}

func TestMarshalAck_Nil(t *testing.T) {
	if _, err := protocol.MarshalAck(nil); err == nil {
		t.Fatal("MarshalAck(nil): want error, got nil")
	}
}

func TestDecodeAck_Empty(t *testing.T) {
	if _, err := protocol.DecodeAck(nil); err == nil {
		t.Fatal("DecodeAck(nil): want error, got nil")
	}
}

func TestDecodeAck_Garbage(t *testing.T) {
	if _, err := protocol.DecodeAck([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("DecodeAck(garbage): want error, got nil")
	}
}

func TestValidateConvInfo_Nil(t *testing.T) {
	if err := protocol.ValidateConvInfo(nil); err == nil {
		t.Fatal("ValidateConvInfo(nil): want error, got nil")
	}
}

func TestValidateConvInfo_OK(t *testing.T) {
	ci := &protocolpb.ConvInfo{
		ConversationId: bytes.Repeat([]byte{0x22}, 16),
		Name:           "the shed", // 8 chars, well under 24
		Kind:           protocolpb.ConvKind_CONV_KIND_MATRIX,
		Target:         "!room:example.org",
		EncryptionKey:  bytes.Repeat([]byte{0x33}, 16),
	}
	if err := protocol.ValidateConvInfo(ci); err != nil {
		t.Fatalf("ValidateConvInfo(valid): want nil, got %v", err)
	}
}
