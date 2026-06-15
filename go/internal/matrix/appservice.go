// Core appservice event-handling logic. See plan.md §7.2.
//
// The appservice core consumes events from a Client, filters them
// (own user_id, empty body, unsupported types), and dispatches
// OnMessage / OnInvite / OnRemove / OnRename callbacks. It is
// build-tag free so it can be tested in CI with the Mock client.
//
// The real mautrix-go HTTP listener is a build-tag-gated file
// (appservice_mautrix.go, tag `mautrix`) that wires this core to
// maunium.net/go/mautrix/appservice's /transactions handler and
// Intent API. It is NOT compiled into CI — the daemon production
// build sets `-tags mautrix` and cmd/tetherd wires it up. The
// CI path uses the Mock client exclusively.
//
// v2: end-to-end encryption. Today's core is plaintext-only; when
// we add E2EE we will introduce a decrypt step on m.room.encrypted
// events and pass the decrypted Event to the same callbacks.
package matrix

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/id"
)

// AppserviceConfig configures an Appservice at construction time.
type AppserviceConfig struct {
	// Client is the underlying transport. Required.
	Client Client
	// UserID is the appservice puppet user_id. Events from this
	// sender are filtered out.
	UserID id.UserID
	// Localpart is the appservice's localpart (e.g. "tether" for
	// @tether:example.com). Used for log lines and the
	// /_matrix/app/v1/users/ namespace check.
	Localpart string
	// Logger is the structured logger. Defaults to slog.Default().
	Logger *slog.Logger
	// OnMessage fires for m.room.message events with a non-empty
	// body from a non-own sender.
	OnMessage func(ctx context.Context, ev Event)
	// OnInvite fires for m.room.member events with membership
	// "invite" targeting our user_id. The appservice also calls
	// Client.JoinRoom in response.
	OnInvite func(ctx context.Context, ev Event)
	// OnRemove fires for m.room.member events with membership
	// "leave" or "ban" for our own user_id (i.e. we were kicked
	// or removed from a room).
	OnRemove func(ctx context.Context, ev Event)
	// OnRename fires for m.room.message events whose body matches
	// the "/tether rename <name>" command. The room alias is
	// stored in the appservice's internal name map (see
	// Appservice.RoomName) and the callback is invoked with the
	// new name.
	OnRename func(ctx context.Context, ev Event, name string)
}

// Appservice is the Tether Matrix puppet. It owns no I/O state of
// its own; it is a pure event-handling layer in front of a Client.
type Appservice struct {
	client    Client
	userID    id.UserID
	localpart string
	logger    *slog.Logger

	onMessage func(ctx context.Context, ev Event)
	onInvite  func(ctx context.Context, ev Event)
	onRemove  func(ctx context.Context, ev Event)
	onRename  func(ctx context.Context, ev Event, name string)

	// roomNames is an internal alias map: room_id → human name
	// set via "/tether rename <name>". Reads and writes are
	// guarded by roomNamesMu.
	roomNamesMu sync.RWMutex
	roomNames   map[id.RoomID]string
}

// NewAppservice returns a fresh Appservice. Call Run on a dedicated
// goroutine to begin processing events.
func NewAppservice(cfg AppserviceConfig) *Appservice {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Appservice{
		client:    cfg.Client,
		userID:    cfg.UserID,
		localpart: cfg.Localpart,
		logger:    logger,
		onMessage: cfg.OnMessage,
		onInvite:  cfg.OnInvite,
		onRemove:  cfg.OnRemove,
		onRename:  cfg.OnRename,
		roomNames: make(map[id.RoomID]string),
	}
}

// UserID returns the appservice puppet user_id.
func (a *Appservice) UserID() id.UserID { return a.userID }

// Localpart returns the appservice's localpart.
func (a *Appservice) Localpart() string { return a.localpart }

// RoomName returns the human-friendly name set via "/tether rename"
// for the given room, or the empty string if none has been set.
// Read-only; safe to call from any goroutine.
func (a *Appservice) RoomName(roomID id.RoomID) string {
	a.roomNamesMu.RLock()
	defer a.roomNamesMu.RUnlock()
	return a.roomNames[roomID]
}

// setRoomName is the internal write side of RoomName.
func (a *Appservice) setRoomName(roomID id.RoomID, name string) {
	a.roomNamesMu.Lock()
	defer a.roomNamesMu.Unlock()
	if name == "" {
		delete(a.roomNames, roomID)
		return
	}
	a.roomNames[roomID] = name
}

// HandleRenameForTest sets the alias for roomID to name. It is
// exported for the test suite only; production code must drive
// the rename through the OnRename callback. The leading-cap
// “Handle…” follows the Go convention that test-only helpers
// are named distinctly from production APIs to keep the public
// surface small.
func (a *Appservice) HandleRenameForTest(roomID id.RoomID, name string) {
	a.setRoomName(roomID, name)
}

// Run consumes events from the Client's Subscribe channel and
// dispatches them. It returns when ctx is canceled. If the
// subscription channel drops (e.g. network blip), Run transparently
// resubscribes with a small backoff. The returned error is the
// context error on graceful shutdown, or nil.
func (a *Appservice) Run(ctx context.Context) error {
	a.logger.Info("appservice: run starting",
		"user_id", a.userID.String(),
		"localpart", a.localpart,
	)
	defer a.logger.Info("appservice: run stopped")

	backoff := newAppserviceBackoff()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Subscribe with the caller's context directly so that
		// when ctx is canceled, the subscription's done channel
		// closes and the pump exits.
		events, done, err := a.client.Subscribe(ctx)
		if err != nil {
			// A transient subscribe failure: back off and retry.
			if errors.Is(err, context.Canceled) {
				return ctx.Err()
			}
			a.logger.Warn("appservice: subscribe failed; backing off", "err", err)
			if !backoff.Sleep(ctx) {
				return ctx.Err()
			}
			continue
		}
		backoff.Reset()

		// Drain the event channel until the subscription ends
		// (done channel closes) or ctx is canceled.
		a.pump(ctx, events, done)
		if err := ctx.Err(); err != nil {
			return err
		}
		a.logger.Warn("appservice: subscription dropped; resubscribing")
	}
}

// pump reads events from ch and dispatches them. Returns when
// the done channel is closed or ctx is canceled.
func (a *Appservice) pump(ctx context.Context, ch <-chan Event, done <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case ev := <-ch:
			a.dispatch(ctx, ev)
		}
	}
}

// dispatch routes one event to the right handler (or drops it).
// The decision tree is:
//
//   - m.room.member (always processed, regardless of sender):
//   - membership=invite → OnInvite + auto-join
//   - membership=leave|ban → OnRemove
//   - m.room.message with non-empty body and non-own sender:
//   - "/tether rename <name>" → setRoomName + OnRename
//   - otherwise → OnMessage
//   - anything else: dropped
//
// Echo prevention (own-sender filter) applies only to
// m.room.message events: m.room.member events are state events
// and must be observed even when our own user is the sender (for
// example, "/leave" produces an m.room.member with sender=our
// user_id and membership=leave).
//
// Membership is encoded in the event Body in this trimmed-down
// view of the world (the real mautrix event has a structured
// content.membership field). The m.room.member dispatcher
// therefore inspects the Body string. This is the convention
// chosen by the room_to_conv mapper (see room_to_conv_test.go
// for the "ignores empty messages" rule that the appservice
// itself honours).
func (a *Appservice) dispatch(ctx context.Context, ev Event) {
	switch ev.Type {
	case "m.room.member":
		a.handleMember(ctx, ev)
	case "m.room.message":
		if ev.Sender == a.userID {
			return
		}
		a.handleMessage(ctx, ev)
	default:
		// Unknown event types are dropped.
	}
}

func (a *Appservice) handleMember(ctx context.Context, ev Event) {
	switch strings.ToLower(strings.TrimSpace(ev.Body)) {
	case "invite":
		// Auto-join the room.
		if err := a.client.JoinRoom(ctx, ev.Room); err != nil {
			a.logger.Warn("appservice: auto-join failed",
				"room_id", ev.Room.String(),
				"err", err,
			)
		}
		if a.onInvite != nil {
			a.onInvite(ctx, ev)
		}
	case "leave", "ban":
		if a.onRemove != nil {
			a.onRemove(ctx, ev)
		}
	}
}

func (a *Appservice) handleMessage(ctx context.Context, ev Event) {
	if ev.Body == "" {
		return
	}
	// /tether rename <name> — set the room's human name and
	// notify the consumer.
	const prefix = "/tether rename "
	if strings.HasPrefix(ev.Body, prefix) {
		name := strings.TrimSpace(ev.Body[len(prefix):])
		a.setRoomName(ev.Room, name)
		if a.onRename != nil {
			a.onRename(ctx, ev, name)
		}
		return
	}
	if a.onMessage != nil {
		a.onMessage(ctx, ev)
	}
}

// newAppserviceBackoff is a tiny helper that produces a fresh
// backoff state. Pulled out so it can be swapped in tests.
var newAppserviceBackoff = func() appserviceBackoff { return defaultBackoff{} }

// appserviceBackoff is the interface the Run loop uses for retry
// pacing. defaultBackoff is a constant 100ms; production would
// back off exponentially.
type appserviceBackoff interface {
	Sleep(ctx context.Context) bool // false ⇒ ctx canceled
	Reset()
}

type defaultBackoff struct{}

func (defaultBackoff) Sleep(ctx context.Context) bool {
	t := time.NewTimer(100 * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func (defaultBackoff) Reset() {}
