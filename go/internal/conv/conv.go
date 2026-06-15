// Package conv is the conversation store and lifecycle for the
// Go daemon. See plan.md §5 / §7.3 / §7.4.
//
// A Conversation is a single channel on the M5 (a Matrix room, a
// Forge session, or a broadcast group). The store is the source
// of truth for what conversations exist and their current state;
// the room_to_conv mapper (matrix/room_to_conv.go) and the
// session_to_conv mapper (forge/session_to_conv.go) write into
// it, and the sync package (conv/sync.go) reads it to push
// UI_UPDATE packets to the M5.
//
// Storage backends:
//
//   - MemStore (mem_store.go) — in-process map, used by tests
//     and the default CI build.
//   - LfsStore (lfs_store.go, future) — LittleFS-backed, used by
//     the production daemon so state survives a reboot.
//
// v2: end-to-end encryption. When E2EE lands, each conversation
// will carry an opaque 16-byte encryption_key (HKDF-derived, see
// internal/crypto/hkdf.go) that the M5 uses to encrypt TTS audio
// back to the base station. The store carries that key as an
// opaque []byte today; no code in this package inspects its
// contents.
package conv

import (
	"errors"
	"strings"

	"maunium.net/go/mautrix/id"
)

// Kind identifies the source of a conversation.
type Kind int

const (
	// KindUnspecified is the zero value; treated as an error
	// when present in Upsert.
	KindUnspecified Kind = iota
	// KindMatrix — a Matrix room (the only kind Phase 6 wires
	// up; Phase 7 adds KindForge).
	KindMatrix
	// KindForge — a Forge agent session. Reserved for Phase 7.
	KindForge
	// KindBroadcast — a one-to-many broadcast group. Reserved
	// for v2.
	KindBroadcast
)

// String returns the human-readable name of the kind.
func (k Kind) String() string {
	switch k {
	case KindMatrix:
		return "matrix"
	case KindForge:
		return "forge"
	case KindBroadcast:
		return "broadcast"
	default:
		return "unspecified"
	}
}

// ParseKind is the inverse of String. Returns KindUnspecified and
// false for unknown values.
func ParseKind(s string) (Kind, bool) {
	switch strings.ToLower(s) {
	case "matrix":
		return KindMatrix, true
	case "forge":
		return KindForge, true
	case "broadcast":
		return KindBroadcast, true
	default:
		return KindUnspecified, false
	}
}

// ConvInfo is the stored metadata for a single conversation. It
// mirrors the ConvInfo protobuf in pkg/protocol/protocolpb but is
// shaped for in-process use (no protobuf dependency).
//
// matches the protobuf definition and is the dominant external
// reference; renaming would obscure the mapping.
//
//nolint:revive // "ConvInfo" stutters as conv.ConvInfo but the name
type ConvInfo struct {
	// Name is the human-friendly name shown on the M5 EPD
	// (≤ 24 chars; the store does not enforce this — the sync
	// layer truncates before sending to the M5).
	Name string
	// Kind is the source (Matrix / Forge / broadcast).
	Kind Kind
	// Target is the kind-specific identifier:
	//   - KindMatrix → room_id (e.g. "!abc:matrix.org")
	//   - KindForge  → session UUID string
	//   - KindBroadcast → group ID
	Target string
	// EncryptionKey is the opaque 16-byte HKDF output. nil
	// means "no key set yet" (the next Apply will populate it).
	// v2: when E2EE lands, this is the megolm session key.
	EncryptionKey []byte
	// LastActivityUnixMs is the wall-clock time of the most
	// recent activity in this conversation, in milliseconds
	// since the Unix epoch.
	LastActivityUnixMs int64
	// UnreadCount is the number of unread items.
	UnreadCount uint32
}

// Conversation is the store row: a stable ID plus the metadata.
// ConversationID is a 16-byte UUID (encoded as a Go []byte) that
// the M5 uses as the lookup key on its LittleFS DB.
type Conversation struct {
	ID   [16]byte
	Info ConvInfo
}

// IDString returns the conversation ID as a 32-char hex string
// (the canonical encoding used on the wire and in URLs).
func (c Conversation) IDString() string {
	return idToHex(c.ID[:])
}

// Sentinel errors.
var (
	// ErrNotFound is returned by Get / Remove when the ID does
	// not exist.
	ErrNotFound = errors.New("conv: conversation not found")
	// ErrInvalidKind is returned by Upsert when ConvInfo.Kind
	// is KindUnspecified.
	ErrInvalidKind = errors.New("conv: conversation kind is unspecified")
	// ErrInvalidTarget is returned by Upsert when ConvInfo.Target
	// is empty.
	ErrInvalidTarget = errors.New("conv: conversation target is empty")
	// ErrInvalidID is returned when a 16-byte ID is required but
	// a different length is supplied.
	ErrInvalidID = errors.New("conv: conversation ID must be 16 bytes")
)

// idToHex returns the 32-char lowercase hex encoding of a byte
// slice. Exported as ConvIDToHex so other packages can format IDs
// consistently.
func idToHex(b []byte) string {
	const hex = "0123456789abcdef"
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[2*i] = hex[v>>4]
		out[2*i+1] = hex[v&0x0F]
	}
	return string(out)
}

// ConvIDToHex returns the 32-char lowercase hex encoding of a
// 16-byte conversation ID.
//
// public API and is referenced from many packages.
//
//nolint:revive // Stutters as conv.ConvIDToHex; the name is the
func ConvIDToHex(b []byte) string { return idToHex(b) }

// RoomIDToConvID derives a stable 16-byte conversation ID from a
// Matrix room ID. The derivation is:
//
//	SHA-1("tether.v1.conv." || roomID)[:16]
//
// The namespace prefix is fixed so the derivation cannot collide
// with any other SHA-1-based UUID the system uses. The [:16]
// truncation matches UUID v4 in length and is the standard "first
// 16 bytes of a SHA-1" trick from research.md §9.2.
func RoomIDToConvID(roomID id.RoomID) [16]byte {
	return roomIDToConvID(roomID)
}

// roomIDToConvID is the var-aliased implementation that tests
// can stub.
var roomIDToConvID = func(roomID id.RoomID) [16]byte {
	const ns = "tether.v1.conv."
	h := sha1Of([]byte(ns + roomID.String()))
	var out [16]byte
	copy(out[:], h[:16])
	return out
}
