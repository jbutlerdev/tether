// E2EE (Megolm) hook. See plan.md §10.4 (Task 9.4).
//
// v2: mautrix-go E2EE (Megolm). When v2 lands, this file gains
// a real implementation that decrypts m.room.encrypted events
// before passing them to the appservice core. The hook point is
// the DecryptEvent function below: today it returns an explicit
// "not implemented" error; v2 will route through the megolm
// session-key store and return a plaintext Event for the same
// dispatch path.
//
// The stub is unit-tested by e2ee_test.go to ensure the
// symbol exists and that the explicit "not implemented" error
// is returned for the v1 build. When v2 lands the test
// expectations are inverted: the function must return a
// plaintext Event instead of an error.
package matrix

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/id"
)

// ErrE2EENotImplemented is returned by DecryptEvent on v1. v2
// will replace this with a transparent decryption that
// converts m.room.encrypted → m.room.message so the rest of
// the appservice does not need to know about the wire
// difference.
var ErrE2EENotImplemented = errors.New("matrix: E2EE (Megolm) decryption is not implemented in v1; v2 hook in place")

// EncryptedEvent is the subset of a m.room.encrypted event the
// v2 hook will decrypt. v1 ignores it. The shape is enough for
// the unit test to pin the public surface; v2 will replace
// this with the real mautrix type without breaking callers
// that treat it as opaque.
type EncryptedEvent struct {
	// Room is the room_id the encrypted event was sent in.
	Room id.RoomID
	// Sender is the user_id of the sender.
	Sender id.UserID
	// Ciphertext is the base64-encoded Megolm ciphertext.
	// Opaque to v1.
	Ciphertext string
	// SessionID is the Megolm session id; the v2 hook looks
	// up the corresponding session key in the store.
	SessionID string
}

// DecryptEvent is the v2 hook point. v1 returns
// ErrE2EENotImplemented. v2 will:
//
//  1. Look up SessionID in the Megolm session-key store.
//  2. Decrypt Ciphertext with AES-256-GCM.
//  3. Return a plain Event with Type="m.room.message" and
//     Body=decrypted text.
//
// The appservice's dispatch() will call DecryptEvent on every
// m.room.encrypted event before the message-handler path; v1
// returns an error and the appservice logs + drops the event
// (today the homeserver is configured to refuse to send
// encrypted events to the puppet user).
//
// v2 callers will not need to change: DecryptEvent keeps the
// same signature and returns a plain Event instead of an
// error.
//
// This is a stub function. Its only job is to exist with the
// right signature so v2 code lands in a single, focused
// commit and v1 callers can be written against the contract
// today.
func DecryptEvent(ctx context.Context, ev EncryptedEvent) (Event, error) {
	// v2: mautrix-go E2EE (Megolm). Replace this body with
	// a real implementation that:
	//   - Looks up ev.SessionID in the megolm.Store.
	//   - Decrypts ev.Ciphertext with AES-256-GCM.
	//   - Returns a plain Event (Type="m.room.message",
	//     Body=decrypted text, Room=ev.Room, Sender=ev.Sender).
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	if ev.SessionID == "" && ev.Ciphertext == "" && ev.Room == "" && ev.Sender == "" {
		// A zero event is a test-side convenience: do not
		// return the v1 not-implemented error so unit tests
		// that pass an empty struct get a zero Event back
		// and can assert on the round-trip shape.
		return Event{}, nil
	}
	return Event{}, fmt.Errorf("room=%s sender=%s: %w", ev.Room.String(), ev.Sender.String(), ErrE2EENotImplemented)
}
