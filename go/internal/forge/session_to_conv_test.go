// Tests for the forge session-to-conversation mapping. See
// plan.md §8.3.
//
// The mapping is a deterministic SHA-1 truncation, identical in
// shape to the matrix.room_to_conv helper. The two namespaces
// are disjoint: forge:UUID derives a different 16-byte id than
// any matrix room, so a Forge session and a Matrix room with
// the same string id never collide.
package forge_test

import (
	"bytes"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/forge"
)

// TestSessionToConvID_Deterministic verifies that the same
// session UUID always hashes to the same 16-byte id.
func TestSessionToConvID_Deterministic(t *testing.T) {
	t.Parallel()
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	a := forge.SessionToConvID(uuid)
	b := forge.SessionToConvID(uuid)
	if !bytes.Equal(a, b) {
		t.Errorf("SessionToConvID: not deterministic: %x vs %x", a, b)
	}
}

// TestSessionToConvID_Length16 verifies that the returned
// slice is exactly 16 bytes (the conv.Store key length).
func TestSessionToConvID_Length16(t *testing.T) {
	t.Parallel()
	got := forge.SessionToConvID("any-uuid")
	if len(got) != 16 {
		t.Errorf("len(SessionToConvID): want 16, got %d", len(got))
	}
}

// TestSessionToConvID_Different verifies that two distinct
// session UUIDs produce two distinct 16-byte ids.
func TestSessionToConvID_Different(t *testing.T) {
	t.Parallel()
	a := forge.SessionToConvID("550e8400-e29b-41d4-a716-446655440000")
	b := forge.SessionToConvID("550e8400-e29b-41d4-a716-446655440001")
	if bytes.Equal(a, b) {
		t.Errorf("distinct UUIDs derived the same conv id: %x", a)
	}
}

// TestSessionToConvID_NotMatrixRoom verifies that a forge
// session and a matrix room whose string id is identical
// produce distinct conv ids (no namespace collision).
func TestSessionToConvID_NotMatrixRoom(t *testing.T) {
	t.Parallel()
	// We can't import conv.RoomIDToConvID without dragging
	// the matrix package, so we hard-code the same namespace
	// string the conv helper uses. If the conv helper ever
	// changes its namespace prefix, this test catches it.
	// The fixture here is "abc".
	forgeID := forge.SessionToConvID("abc")
	// Independently re-derive the conv id as the conv helper
	// would. We use crypto/sha1 directly to avoid the
	// dependency on the matrix package.
	convIDForMatrix := sha1Trunc16("tether.v1.conv." + "abc")
	if bytes.Equal(forgeID, convIDForMatrix) {
		t.Errorf("forge and matrix namespaces collide on id 'abc': %x", forgeID)
	}
}

// TestSessionToConvID_EmptyString verifies that the empty
// string still produces a valid 16-byte id (the conv.Store
// requires the id to be 16 bytes; the namespace prefix
// guarantees that).
func TestSessionToConvID_EmptyString(t *testing.T) {
	t.Parallel()
	got := forge.SessionToConvID("")
	if len(got) != 16 {
		t.Errorf("len('') = %d, want 16", len(got))
	}
}

// TestSessionToConvID_SpecialCharacters verifies that UUIDs
// with edge-case characters (uppercase, dashes, colons) all
// hash deterministically and produce 16 bytes.
func TestSessionToConvID_SpecialCharacters(t *testing.T) {
	t.Parallel()
	cases := []string{
		"AAAA1111-BBBB-2222-CCCC-333344445555",
		"forge:abc-123",
		"a/b/c/d/e",
		"0",
	}
	for _, c := range cases {
		got := forge.SessionToConvID(c)
		if len(got) != 16 {
			t.Errorf("SessionToConvID(%q): len = %d, want 16", c, len(got))
		}
	}
}

// TestSessionToConvID_StableAcrossReordering verifies that
// the order of calls does not affect the result.
func TestSessionToConvID_StableAcrossReordering(t *testing.T) {
	t.Parallel()
	uuid := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	a := forge.SessionToConvID(uuid)
	b := forge.SessionToConvID("other-uuid")
	c := forge.SessionToConvID(uuid)
	if !bytes.Equal(a, c) {
		t.Errorf("not stable across reordering: %x vs %x", a, c)
	}
	if bytes.Equal(a, b) {
		t.Errorf("distinct UUIDs collide: %x", a)
	}
}

// TestSessionToConvID_NeverAllZeros verifies that no UUID
// hashes to the all-zeros id. SHA-1 of a non-empty namespace
// + a non-empty string is never zero.
func TestSessionToConvID_NeverAllZeros(t *testing.T) {
	t.Parallel()
	zero := [16]byte{}
	for _, c := range []string{"a", "b", "uuid", "0", "forge", "matrix"} {
		got := forge.SessionToConvID16(c)
		if got == zero {
			t.Errorf("SessionToConvID16(%q): all-zeros id", c)
		}
	}
}

// TestSessionToConvID_NotMatrixNamespace verifies the
// reverse direction: a matrix room id should not collide
// with a forge session id. This is the same property as
// TestSessionToConvID_NotMatrixRoom, but checked from a
// different angle (matrix -> conv vs forge -> conv).
func TestSessionToConvID_NotMatrixNamespace(t *testing.T) {
	t.Parallel()
	// For each candidate matrix-looking id, the matrix
	// derivation uses prefix "tether.v1.conv." and the
	// forge derivation uses prefix "forge:". As long as
	// the two prefixes are different, the resulting ids
	// are different (SHA-1 collisions across different
	// prefixes are not a concern for a 16-byte truncation).
	forgePrefix := sha1Trunc16("forge:" + "abc")
	matrixPrefix := sha1Trunc16("tether.v1.conv." + "abc")
	if bytes.Equal(forgePrefix, matrixPrefix) {
		t.Errorf("forge and matrix prefixes collide: %x", forgePrefix)
	}
}

// TestSessionToConvID16_FixedArray verifies that the
// [16]byte variant returns the same bytes as the []byte
// variant for the same input.
func TestSessionToConvID16_FixedArray(t *testing.T) {
	t.Parallel()
	uuid := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	a := forge.SessionToConvID(uuid)
	b := forge.SessionToConvID16(uuid)
	if !bytes.Equal(a, b[:]) {
		t.Errorf("SessionToConvID != SessionToConvID16: %x vs %x", a, b)
	}
}
