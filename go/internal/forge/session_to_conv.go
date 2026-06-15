// Session-UUID → conversation_id mapping. See plan.md §8.3.
//
// The mapping is a deterministic SHA-1 truncation: the first
// 16 bytes of SHA-1("forge:" || sessionUUID). The "forge:"
// namespace prefix is disjoint from conv.RoomIDToConvID's
// "tether.v1.conv." prefix, so a Forge session and a Matrix
// room with the same string id never collide.
//
// The output is exactly 16 bytes — the conv.Store key length —
// and the mapping is collision-resistant for the foreseeable
// future (the input is a UUID, so the SHA-1 collision space is
// 2^128 in the worst case).
package forge

import "crypto/sha1"

// SessionToConvID derives a stable 16-byte conversation id
// from a Forge session UUID. The result is suitable as a
// conv.Store key and as the ConversationId field of a
// protocolpb.Envelope.
func SessionToConvID(sessionUUID string) []byte {
	const ns = "forge:"
	h := sha1.Sum([]byte(ns + sessionUUID))
	out := make([]byte, 16)
	copy(out, h[:16])
	return out
}

// SessionToConvID16 is a fixed-size-array variant of
// SessionToConvID. Useful when the caller has a [16]byte
// variable rather than a []byte.
func SessionToConvID16(sessionUUID string) [16]byte {
	const ns = "forge:"
	h := sha1.Sum([]byte(ns + sessionUUID))
	var out [16]byte
	copy(out[:], h[:16])
	return out
}
