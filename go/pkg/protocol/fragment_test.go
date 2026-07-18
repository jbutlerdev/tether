// Tests for payload fragmentation and reassembly. See plan.md §2.2.
package protocol_test

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"testing"
	"testing/quick"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// fragmentEnv returns a sample envelope that fragmentation tests can
// override. The fields are set to known-good values so the result of
// Fragment is easy to assert on.
func fragmentEnv() *protocolpb.Envelope {
	return &protocolpb.Envelope{
		ProtocolVersion: 1,
		TargetId:        &protocolpb.NodeId{Value: 0xFFFF},
		SenderId:        &protocolpb.NodeId{Value: 0x0001},
		ConversationId:  bytes.Repeat([]byte{0xCD}, 16),
		MessageId:       42,
		AudioKind:       protocolpb.AudioKind_AUDIO_KIND_MIC,
	}
}

func TestFragment_EmptyPayload(t *testing.T) {
	envs, err := protocol.Fragment([]byte{}, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment empty: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("Fragment empty: want 0 envelopes, got %d", len(envs))
	}
}

func TestFragment_SingleChunk(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAA}, 100)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("Fragment 100 bytes: want 1 envelope, got %d", len(envs))
	}
	if envs[0].TotalSeqs != 1 {
		t.Errorf("TotalSeqs: want 1, got %d", envs[0].TotalSeqs)
	}
	if envs[0].SeqNum != 0 {
		t.Errorf("SeqNum: want 0, got %d", envs[0].SeqNum)
	}
	if !bytes.Equal(envs[0].Payload, payload) {
		t.Errorf("Payload mismatch")
	}
	if envs[0].MessageId != 1 {
		t.Errorf("MessageId: want 1, got %d", envs[0].MessageId)
	}
}

func TestFragment_MultipleChunks(t *testing.T) {
	// 1000 bytes at max chunk size 221 → 5 chunks of 221, 221, 221, 221, 116.
	payload := bytes.Repeat([]byte{0xBB}, 1000)
	envs, err := protocol.Fragment(payload, 7, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	if len(envs) != 5 {
		t.Fatalf("Fragment 1000 bytes: want 5 envelopes, got %d", len(envs))
	}
	wantSizes := []int{221, 221, 221, 221, 116}
	for i, env := range envs {
		if env.TotalSeqs != 5 {
			t.Errorf("env[%d].TotalSeqs: want 5, got %d", i, env.TotalSeqs)
		}
		if env.SeqNum != uint32(i) {
			t.Errorf("env[%d].SeqNum: want %d, got %d", i, i, env.SeqNum)
		}
		if env.MessageId != 7 {
			t.Errorf("env[%d].MessageId: want 7, got %d", i, env.MessageId)
		}
		if len(env.Payload) != wantSizes[i] {
			t.Errorf("env[%d].Payload length: want %d, got %d", i, wantSizes[i], len(env.Payload))
		}
	}
	// Reassemble the payloads and verify equality.
	joined := joinPayloads(envs)
	if !bytes.Equal(joined, payload) {
		t.Errorf("reassembled payload mismatch")
	}
}

func TestFragment_ExactlyMaxPerChunk(t *testing.T) {
	payload := bytes.Repeat([]byte{0xCC}, protocol.MaxPayloadSize) // exactly one chunk
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("Fragment %d bytes: want 1 envelope, got %d", protocol.MaxPayloadSize, len(envs))
	}
	if envs[0].TotalSeqs != 1 {
		t.Errorf("TotalSeqs: want 1, got %d", envs[0].TotalSeqs)
	}
}

func TestFragment_OneOverMax(t *testing.T) {
	// Fragment must never produce a chunk > MaxPayloadSize. We test this
	// by feeding a payload that would produce a chunk > MaxPayloadSize if
	// the implementation uses the wrong chunk size, but at the correct
	// MaxPayloadSize the largest chunk is 221 bytes, so a 222-byte
	// payload produces a 221-byte chunk plus a 1-byte chunk.
	payload := bytes.Repeat([]byte{0xDD}, protocol.MaxPayloadSize+1)
	_, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	// 222 bytes fits in two chunks (221 + 1), so this should succeed.
	if err != nil {
		t.Fatalf("Fragment %d bytes: want nil (splits into 221+1), got %v", protocol.MaxPayloadSize+1, err)
	}
}

func TestReassemble_Happy(t *testing.T) {
	payload := bytes.Repeat([]byte{0xEE}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	got, err := protocol.Reassemble(envs)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Reassemble: payload mismatch (want %d bytes, got %d)", len(payload), len(got))
	}
}

func TestReassemble_OutOfOrder(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAA}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	// Shuffle the first three.
	envs[0], envs[1], envs[2] = envs[2], envs[0], envs[1]

	got, err := protocol.Reassemble(envs)
	if !errors.Is(err, protocol.ErrOutOfOrder) {
		t.Fatalf("Reassemble shuffled: want ErrOutOfOrder, got %v", err)
	}
	if got != nil {
		t.Errorf("Reassemble shuffled: want nil payload, got %d bytes", len(got))
	}
}

func TestReassemble_MissingChunk(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAA}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	// Drop seq 2.
	truncated := append([]*protocolpb.Envelope{}, envs[:2]...)
	truncated = append(truncated, envs[3:]...)

	_, err = protocol.Reassemble(truncated)
	if !errors.Is(err, protocol.ErrMissingChunk) {
		t.Fatalf("Reassemble missing chunk: want ErrMissingChunk, got %v", err)
	}
}

func TestReassemble_Duplicate(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAA}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	dup := append([]*protocolpb.Envelope{}, envs[:3]...)
	dup = append(dup, envs[2]) // seq 2 again
	dup = append(dup, envs[3:]...)

	_, err = protocol.Reassemble(dup)
	if !errors.Is(err, protocol.ErrDuplicateSeq) {
		t.Fatalf("Reassemble duplicate: want ErrDuplicateSeq, got %v", err)
	}
}

func TestReassemble_TotalSeqsMismatch(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAA}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	envs[0].TotalSeqs = 6 // lies about the count

	_, err = protocol.Reassemble(envs)
	if !errors.Is(err, protocol.ErrSeqMismatch) {
		t.Fatalf("Reassemble with mismatched TotalSeqs: want ErrSeqMismatch, got %v", err)
	}
}

func TestRoundTrip_RandomSizes(t *testing.T) {
	// Property-style test: many random sizes, fragment → reassemble.
	sizes := []int{1, 100, 220, 221, 222, 442, 500, 663, 1000, 2000, 10000, 100000}
	for _, sz := range sizes {
		sz := sz
		t.Run(fmt.Sprintf("size=%d", sz), func(t *testing.T) {
			payload := bytes.Repeat([]byte{byte(sz & 0xFF)}, sz)
			envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
				protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
			if err != nil {
				t.Fatalf("Fragment: %v", err)
			}
			got, err := protocol.Reassemble(envs)
			if err != nil {
				t.Fatalf("Reassemble: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("round-trip mismatch: want %d bytes, got %d", len(payload), len(got))
			}
		})
	}
}

func TestFragment_StableSeqOrder(t *testing.T) {
	// The output of Fragment must be in seq_num order 0, 1, 2, …, n-1
	// without any gaps or reordering, even though this is also what
	// Reassemble will verify. This is a contract test for the producer.
	payload := bytes.Repeat([]byte{0xFF}, 1000)
	envs, err := protocol.Fragment(payload, 1, bytes.Repeat([]byte{0xAB}, 16),
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	for i, env := range envs {
		if env.SeqNum != uint32(i) {
			t.Errorf("env[%d].SeqNum: want %d, got %d", i, i, env.SeqNum)
		}
	}
}

// TestProperty_FragmentReassemble is a quick.Check-based property test
// for the round-trip identity. Plan §2.2.
func TestProperty_FragmentReassemble(t *testing.T) {
	f := func(payload []byte) bool {
		// Skip empty and oversize — covered by direct tests.
		if len(payload) == 0 || len(payload) > 1<<20 {
			return true
		}
		envs, err := protocol.Fragment(payload, 1, []byte("conv-uuid-1234"),
			protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
		if err != nil {
			t.Logf("Fragment failed at len=%d: %v", len(payload), err)
			return false
		}
		got, err := protocol.Reassemble(envs)
		if err != nil {
			t.Logf("Reassemble failed at len=%d: %v", len(payload), err)
			return false
		}
		if !bytes.Equal(got, payload) {
			t.Logf("payload mismatch at len=%d", len(payload))
			return false
		}
		// Sort by SeqNum and verify order matches insertion order.
		sorted := make([]*protocolpb.Envelope, len(envs))
		copy(sorted, envs)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].SeqNum < sorted[j].SeqNum })
		for i, env := range sorted {
			if env.SeqNum != uint32(i) {
				t.Logf("seq gap at %d", i)
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

// joinPayloads is a test helper for the multi-chunk tests.
func joinPayloads(envs []*protocolpb.Envelope) []byte {
	var out []byte
	for _, e := range envs {
		out = append(out, e.Payload...)
	}
	return out
}

// TestReassemble_Empty covers the "no envelopes" early return.
func TestReassemble_Empty(t *testing.T) {
	got, err := protocol.Reassemble(nil)
	if err != nil {
		t.Fatalf("Reassemble(nil): want nil err, got %v", err)
	}
	if got != nil {
		t.Errorf("Reassemble(nil): want nil payload, got %d bytes", len(got))
	}
}

// TestReassemble_TotalSeqsZero covers the "TotalSeqs not set" error path.
func TestReassemble_TotalSeqsZero(t *testing.T) {
	envs := []*protocolpb.Envelope{
		{SeqNum: 0, TotalSeqs: 0, Payload: []byte{0x01}},
	}
	_, err := protocol.Reassemble(envs)
	if !errors.Is(err, protocol.ErrSeqMismatch) {
		t.Fatalf("Reassemble TotalSeqs=0: want ErrSeqMismatch, got %v", err)
	}
}
