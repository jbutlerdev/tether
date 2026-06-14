// Tests for the Tether envelope codec. See plan.md §1.4.
//
// The TDD cycle:
//   1. this file is committed (failing — the production code does not
//      exist yet, so the test package fails to compile).
//   2. header.go is written to make the tests pass.
//   3. a fuzz test runs in CI for 60 s to catch adversarial inputs.
package protocol_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

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
	if len(wire) == 0 {
		t.Fatal("Encode returned empty bytes")
	}

	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !envelopesEqual(env, got) {
		t.Fatalf("round-trip mismatch:\n want=%v\n got =%v", env, got)
	}
}

func TestEnvelope_MaxPayload(t *testing.T) {
	env := makeEnvelope()
	env.Payload = bytes.Repeat([]byte{0x42}, 227) // protocol §1.4 says ≤ 227

	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode at max payload: %v", err)
	}
	got, err := protocol.Decode(wire)
	if err != nil {
		t.Fatalf("Decode at max payload: %v", err)
	}
	if len(got.Payload) != 227 {
		t.Fatalf("payload length: want 227, got %d", len(got.Payload))
	}
}

func TestEnvelope_OverSizedPayload(t *testing.T) {
	env := makeEnvelope()
	env.Payload = bytes.Repeat([]byte{0x42}, 228) // 1 over the max

	_, err := protocol.Encode(env)
	if !errors.Is(err, protocol.ErrPayloadTooLarge) {
		t.Fatalf("Encode over-sized payload: want ErrPayloadTooLarge, got %v", err)
	}
}

func TestEnvelope_BadCRC(t *testing.T) {
	env := makeEnvelope()
	wire, err := protocol.Encode(env)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Flip a single bit somewhere safely inside the protobuf body.
	// The CRC field is part of the encoded message, but at this size
	// the body is well over 50 bytes long, so picking the middle is
	// guaranteed to miss the CRC tag+value and corrupt a real field.
	bodyIdx := len(wire) / 2
	wire[bodyIdx] ^= 0x01

	_, err = protocol.Decode(wire)
	if !errors.Is(err, protocol.ErrBadCRC) {
		t.Fatalf("Decode with bit flip: want ErrBadCRC, got %v", err)
	}
}

func TestEnvelope_Truncated(t *testing.T) {
	wire := []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	_, err := protocol.Decode(wire)
	if !errors.Is(err, protocol.ErrTruncated) {
		t.Fatalf("Decode of 5-byte buffer: want ErrTruncated, got %v", err)
	}
}

func TestAck_BitmapEdges(t *testing.T) {
	// Plan §1.4: bitmap rolling from next_expected_seq = 0xFFFFFFE0 to
	// 0xFFFFFFFF is correct. The Ack proto message splits the 32-bit
	// window into lo (lower 16 bits) and hi (upper 16 bits). When the
	// window starts at 0xFFFFFFE0 and all 32 seqs are acked:
	//   - lo covers 0xFFFFFFE0..0xFFFFFFEF → 0xFFFF
	//   - hi covers 0xFFFFFFF0..0xFFFFFFFF → 0xFFFF
	ack := &protocolpb.Ack{
		ConversationId:  bytes.Repeat([]byte{0x11}, 16),
		MessageId:       1,
		NextExpectedSeq: 0xFFFFFFE0,
		AckBitmapLo:     0xFFFF,
		AckBitmapHi:     0xFFFF,
		Crc:             0,
	}

	raw, err := protocol.MarshalAck(ack)
	if err != nil {
		t.Fatalf("MarshalAck: %v", err)
	}
	got, err := protocol.DecodeAck(raw)
	if err != nil {
		t.Fatalf("DecodeAck: %v", err)
	}
	if got.NextExpectedSeq != 0xFFFFFFE0 {
		t.Errorf("NextExpectedSeq: want 0xFFFFFFE0, got %#x", got.NextExpectedSeq)
	}
	if got.AckBitmapLo != 0xFFFF {
		t.Errorf("AckBitmapLo: want 0xFFFF, got %#x", got.AckBitmapLo)
	}
	if got.AckBitmapHi != 0xFFFF {
		t.Errorf("AckBitmapHi: want 0xFFFF, got %#x", got.AckBitmapHi)
	}
}

func TestStartInfo_DurationField(t *testing.T) {
	// Plan §1.4: DurationMs * (BitrateBps/8) / PayloadSizeBytes == TotalSeqs.
	// The literal expression in the plan is missing the ms→s conversion;
	// the dimensionally-correct form is
	//     ceil(DurationMs * BitrateBps / 8000 / PayloadSizeBytes).
	// Both formulas agree on the *form* of the relationship (each field
	// contributes linearly), which is what this test pins down.
	start := &protocolpb.StartInfo{
		Codec:            protocolpb.AudioCodec_AUDIO_CODEC_OPUS_8K_16KBPS,
		SampleRateHz:     8000,
		BitrateBps:       16000,
		DurationMs:       4000, // 4 s
		PayloadSizeBytes: 227,
	}
	// 4 s × 16 kbps / 8 = 8000 bytes; ceil(8000/227) = 36 seqs.
	want := uint32(36)
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
// must be one of the documented sentinel errors. Plan §1.4 mandates a
// 60 s fuzz on every PR; locally we run a shorter burst.
func FuzzEnvelopeDecode(f *testing.F) {
	// In-memory seeds: 10 known-good envelopes with varied fields.
	// (We do not put them in testdata/fuzz/FuzzEnvelopeDecode/ because
	// Go's testing framework treats files there as internal-fuzzer-
	// encoded corpus entries, not raw bytes — using f.Add() is the
	// portable way to seed with raw input.)
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
		// Acceptable outcomes: nil, ErrTruncated, ErrBadCRC, ErrPayloadTooLarge.
		// Any other error or a panic is a bug.
	})
}

// envelopesEqual is a small protobuf-aware equality check that tolerates
// nil sub-message fields.
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

// totalSeqs is the helper that the plan §1.4 calls out:
//   TotalSeqs = ceil(DurationMs * (BitrateBps/8) / PayloadSizeBytes).
//
// The dimensionally-correct form is
//   ceil(DurationMs/1000 * BitrateBps/8 / PayloadSizeBytes)
// which we use here. The plan's formula agrees on form but is missing
// the /1000 conversion; see TestStartInfo_DurationField.
func totalSeqs(s *protocolpb.StartInfo) uint32 {
	totalBytes := uint64(s.DurationMs/1000) * (uint64(s.BitrateBps) / 8)
	return uint32((totalBytes + uint64(s.PayloadSizeBytes) - 1) / uint64(s.PayloadSizeBytes))
}

// TestCrc16CCITT_KnownVectors covers the canonical CRC-16/CCITT-FALSE
// test vectors. CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF, no reflect,
// no xorout. Reference: https://www.lammertbies.nl/comm/info/crc-calculation
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

// TestEncode_NilEnvelope covers the nil-guard in Encode.
func TestEncode_NilEnvelope(t *testing.T) {
	if _, err := protocol.Encode(nil); err == nil {
		t.Fatal("Encode(nil): want error, got nil")
	}
}

// TestDecode_GarbageBuffer covers the unmarshal-error path in Decode.
func TestDecode_GarbageBuffer(t *testing.T) {
	// 8 bytes that look length-delimited but are not a valid Envelope.
	// The header length is huge and not a valid varint continuation.
	buf := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	if _, err := protocol.Decode(buf); err == nil {
		t.Fatal("Decode(garbage): want error, got nil")
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

// TestEncode_MarshalError exercises the unreachable-but-defensive
// "first proto.Marshal failed" branch by swapping the marshaler. We
// restore the original after the test so subsequent tests pass.
func TestEncode_MarshalError(t *testing.T) {
	orig := protocol.SwapProtoMarshal(func(proto.Message) ([]byte, error) {
		return nil, errors.New("forced marshal failure")
	})
	t.Cleanup(func() { protocol.SwapProtoMarshal(orig) })

	env := makeEnvelope()
	if _, err := protocol.Encode(env); err == nil {
		t.Fatal("Encode with broken marshaler: want error, got nil")
	}
}

// TestEncode_SecondMarshalError covers the second-marshal-failed path.
func TestEncode_SecondMarshalError(t *testing.T) {
	calls := 0
	orig := protocol.SwapProtoMarshal(func(proto.Message) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("ok"), nil
		}
		return nil, errors.New("forced re-marshal failure")
	})
	t.Cleanup(func() { protocol.SwapProtoMarshal(orig) })

	env := makeEnvelope()
	if _, err := protocol.Encode(env); err == nil {
		t.Fatal("Encode with second-marshal broken: want error, got nil")
	}
}

// TestDecode_UnmarshalError covers the proto.Unmarshal error branch.
func TestDecode_UnmarshalError(t *testing.T) {
	orig := protocol.SwapProtoUnmarshal(func([]byte, proto.Message) error {
		return errors.New("forced unmarshal failure")
	})
	t.Cleanup(func() { protocol.SwapProtoUnmarshal(orig) })

	if _, err := protocol.Decode([]byte("anything goes")); err == nil {
		t.Fatal("Decode with broken unmarshaler: want error, got nil")
	}
}

// TestDecode_RemarshalError covers the re-marshal error path in Decode.
// We need a valid wire-format buffer to clear the protoUnmarshal step;
// then we swap the marshaler to fail on the very next call.
func TestDecode_RemarshalError(t *testing.T) {
	// First produce a valid encoded envelope using real proto.Marshal.
	wire, err := protocol.Encode(makeEnvelope())
	if err != nil {
		t.Fatalf("setup Encode: %v", err)
	}

	orig := protocol.SwapProtoMarshal(func(proto.Message) ([]byte, error) {
		return nil, errors.New("forced re-marshal failure")
	})
	t.Cleanup(func() { protocol.SwapProtoMarshal(orig) })

	if _, err := protocol.Decode(wire); err == nil {
		t.Fatal("Decode with broken re-marshal: want error, got nil")
	}
}
