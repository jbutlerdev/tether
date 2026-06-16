// Package matrix is Tether's Matrix appservice integration. See plan.md §7.
//
// The package is split across three commits:
//
//  1. Client interface + Mock (this file + mock_client.go).
//     The Mock is the in-process test double used by all unit tests
//     and the CI pipeline. It is the only Client implementation
//     compiled into CI by default.
//
//  2. Appservice core (appservice.go) — consumes events from a
//     Client, dispatches on-message / on-invite / on-leave callbacks,
//     and is testable in-process with the Mock.
//
//  3. Real mautrix-go HTTP listener (appservice_mautrix.go, build
//     tag `mautrix`) — wraps the core with a /transactions listener
//     and the Intent API. Only built when explicitly requested; not
//     on the CI path.
//
// v2: end-to-end encryption (E2EE) is explicitly deferred. The
// package's public API is shaped so that adding megolm support
// later does not break v1 callers — encryption keys are passed
// through as opaque bytes today.
package matrix

import (
	"context"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

// Event is a single Matrix event delivered to a Subscribe consumer.
//
// It is a deliberately small surface — enough for the appservice
// core to make routing decisions (which room? from whom? body?)
// without leaking the full mautrix event type, which is large and
// has many fields that we do not consume.
type Event struct {
	// Type is the Matrix event type (e.g. "m.room.message",
	// "m.room.member", "m.room.name").
	Type string
	// Sender is the user_id of the sender (the Appservice's own
	// user_id is filtered by the appservice core, see
	// appservice.go).
	Sender id.UserID
	// Room is the room_id the event was sent in.
	Room id.RoomID
	// Body is the textual body for m.room.message events. Empty
	// for events that have no body.
	Body string
	// Time is the origin_server_ts of the event.
	Time time.Time
}

// Client is the abstract Matrix client. Implementations: Mock (in-
// process test double, mock_client.go) and Appservice (real
// mautrix-go appservice, appservice.go + appservice_mautrix.go).
//
// All methods take a context.Context for cancellation. The real
// implementation maps ctx cancel to a request abort; the Mock does
// not perform I/O so cancellation is observed only on the
// Subscribe channel.
type Client interface {
	// SendText posts a text message to a room. Returns the
	// mautrix response so the caller can correlate with the
	// event_id (used by tests; the appservice core does not
	// require it).
	SendText(ctx context.Context, roomID id.RoomID, text string) (*mautrix.RespSendEvent, error)

	// JoinRoom makes the appservice puppet user join a room.
	JoinRoom(ctx context.Context, roomID id.RoomID) error

	// LeaveRoom makes the appservice puppet user leave a room.
	LeaveRoom(ctx context.Context, roomID id.RoomID) error

	// Subscribe returns a channel of events from the underlying
	// transport, plus a "done" channel that is closed when the
	// subscription ends (network drop, ctx cancel, etc.). The
	// Mock client supports reconnection (see MockClient.Reconnect).
	// The real client is built on mautrix-go's /transactions
	// listener, which auto-reconnects with backoff.
	//
	// The event channel is NOT closed when the subscription
	// ends; the done channel is the canonical "stop reading"
	// signal. This avoids a send-vs-close race with concurrent
	// InjectEvent calls (in the Mock) and with the network
	// teardown path (in the real client).
	//
	// Subscribe MUST NOT be called more than once concurrently
	// per Client. The Mock and the real appservice both
	// guarantee that a second Subscribe call gets its own
	// channel (after the first drops), so callers should track
	// subscriptions themselves.
	Subscribe(ctx context.Context) (<-chan Event, <-chan struct{}, error)

	// GetRoomState returns the current state of a room (name,
	// topic, members, …). Used by the room-to-conversation
	// mapper to derive a conversation name from the canonical
	// room name when /tether rename has not been issued.
	GetRoomState(ctx context.Context, roomID id.RoomID) (*mautrix.RoomStateMap, error)

	// Close releases any resources held by the client. Idempotent.
	Close() error
}
