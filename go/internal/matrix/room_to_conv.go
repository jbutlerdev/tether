// Room-to-conversation mapping. See plan.md §7.3.
//
// The Mapper translates Matrix events into conv.Store mutations:
// each unique room becomes one conversation whose 16-byte id is
// derived deterministically from the room_id (see
// conv.RoomIDToConvID). The mapper is intentionally simple —
// no priority, no threading, no rate limiting — because all
// concurrency is the conv.Store's problem.
package matrix

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/jbutlerdev/tether/go/internal/conv"
)

// RoomNamer is the interface the Mapper uses to discover a
// conversation's name. Two implementations exist in the package:
//
//   - AppserviceRoomNamer: a thin wrapper over Appservice that
//     returns the alias name set via /tether rename, falling
//     back to GetRoomState's m.room.name event.
//   - tests can plug in their own RoomNamer.
type RoomNamer interface {
	// RoomName returns the human-friendly name for the room, or
	// the empty string if no name is available.
	RoomName(ctx context.Context, roomID id.RoomID) string
}

// Mapper converts Matrix events into conv.Store mutations.
type Mapper struct {
	store  conv.Store
	namer  RoomNamer
	logger *slog.Logger
	// ownUserID is the appservice puppet user_id; events from
	// this sender are ignored (echo prevention).
	ownUserID id.UserID
}

// MapperOption configures a Mapper.
type MapperOption func(*Mapper)

// MapperOptionNamer sets the RoomNamer used to derive a
// conversation's display name.
func MapperOptionNamer(n RoomNamer) MapperOption {
	return func(m *Mapper) { m.namer = n }
}

// MapperOptionLogger sets the structured logger.
func MapperOptionLogger(l *slog.Logger) MapperOption {
	return func(m *Mapper) {
		if l != nil {
			m.logger = l
		}
	}
}

// NewMapper returns a Mapper. store must be non-nil; namer is
// optional (nil disables name lookup — the conversation is
// inserted with an empty Name).
func NewMapper(store conv.Store, ownUserID id.UserID, opts ...MapperOption) *Mapper {
	m := &Mapper{
		store:     store,
		ownUserID: ownUserID,
		namer:     noopNamer{},
		logger:    slog.Default(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// OnRoomMessage is the entry point used by the appservice core's
// OnMessage callback. It creates or updates the conversation
// that backs the given Matrix room.
//
// The method is idempotent: re-receiving the same message does
// not create a duplicate conversation. It does, however, update
// LastActivityUnixMs and UnreadCount on every call — those are
// the canonical "this conversation is fresh" signals.
func (m *Mapper) OnRoomMessage(ctx context.Context, ev Event) {
	if m.shouldIgnore(ev) {
		return
	}
	convID := conv.RoomIDToConvID(ev.Room)
	name := m.lookupName(ctx, ev.Room)
	nowMs := ev.Time.UnixMilli()
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}

	existing, err := m.store.Get(ctx, convID)
	if err != nil {
		// Not found → insert.
		if _, _, upErr := m.store.Upsert(ctx, convID, conv.ConvInfo{
			Name:               name,
			Kind:               conv.KindMatrix,
			Target:             ev.Room.String(),
			LastActivityUnixMs: nowMs,
			UnreadCount:        1,
		}); upErr != nil {
			m.logger.Warn("mapper: upsert failed",
				"room_id", ev.Room.String(),
				"err", upErr,
			)
		}
		return
	}

	// Update in place. The store's Upsert is the right verb for
	// "create-or-update"; we just bump the activity / unread.
	existing.Info.Name = name
	existing.Info.LastActivityUnixMs = nowMs
	existing.Info.UnreadCount++
	if _, _, upErr := m.store.Upsert(ctx, convID, existing.Info); upErr != nil {
		m.logger.Warn("mapper: upsert update failed",
			"room_id", ev.Room.String(),
			"err", upErr,
		)
	}
}

// OnRoomRemoved is the entry point for OnRemove. It removes the
// conversation from the store so the M5 stops receiving updates
// for it.
func (m *Mapper) OnRoomRemoved(ctx context.Context, ev Event) {
	convID := conv.RoomIDToConvID(ev.Room)
	if _, err := m.store.Remove(ctx, convID); err != nil {
		m.logger.Warn("mapper: remove failed",
			"room_id", ev.Room.String(),
			"err", err,
		)
	}
}

// OnRoomRenamed is the entry point for OnRename. It updates the
// stored name without touching activity / unread.
func (m *Mapper) OnRoomRenamed(ctx context.Context, ev Event, name string) {
	convID := conv.RoomIDToConvID(ev.Room)
	existing, err := m.store.Get(ctx, convID)
	if err != nil {
		// Renaming a non-existent conversation: insert it.
		_, _, upErr := m.store.Upsert(ctx, convID, conv.ConvInfo{
			Name:   name,
			Kind:   conv.KindMatrix,
			Target: ev.Room.String(),
		})
		if upErr != nil {
			m.logger.Warn("mapper: rename-on-missing failed",
				"room_id", ev.Room.String(),
				"err", upErr,
			)
		}
		return
	}
	existing.Info.Name = name
	if _, _, upErr := m.store.Upsert(ctx, convID, existing.Info); upErr != nil {
		m.logger.Warn("mapper: rename failed",
			"room_id", ev.Room.String(),
			"err", upErr,
		)
	}
}

// shouldIgnore is the centralised filter for the mapper. It
// drops:
//
//   - events from our own user_id (echo prevention)
//   - events with no body
//
// The "ignores empty messages" rule in plan.md §7.3 covers the
// m.notice / m.image cases: the appservice core already filters
// to m.room.message events, but in real Matrix a "message" event
// can have an empty body (e.g. an m.image event with no text
// caption). The mapper treats empty-body messages as "not a
// conversation-bumping event" and drops them, which is the same
// rule the appservice itself honours (see
// appservice.handleMessage).
func (m *Mapper) shouldIgnore(ev Event) bool {
	if ev.Sender == m.ownUserID {
		return true
	}
	if strings.TrimSpace(ev.Body) == "" {
		return true
	}
	return false
}

// lookupName asks the namer for the room's display name. A
// failure (namer returns empty string) is fine: the conversation
// is created with an empty Name and the next /tether rename
// will fix it.
func (m *Mapper) lookupName(ctx context.Context, roomID id.RoomID) string {
	if m.namer == nil {
		return ""
	}
	return m.namer.RoomName(ctx, roomID)
}

// noopNamer returns the empty string for every room.
type noopNamer struct{}

func (noopNamer) RoomName(context.Context, id.RoomID) string { return "" }

// AppserviceRoomNamer wraps an Appservice to provide a
// RoomNamer. The /tether rename alias takes precedence; if no
// alias is set, the namer falls back to the room state
// (m.room.name) via the Client.
type AppserviceRoomNamer struct {
	Appsvc *Appservice
	Client Client
}

// RoomName returns the appservice's alias (if any) or falls back
// to the Client.GetRoomState-derived name.
func (a AppserviceRoomNamer) RoomName(ctx context.Context, roomID id.RoomID) string {
	if a.Appsvc != nil {
		if n := a.Appsvc.RoomName(roomID); n != "" {
			return n
		}
	}
	if a.Client == nil {
		return ""
	}
	st, err := a.Client.GetRoomState(ctx, roomID)
	if err != nil || st == nil {
		return ""
	}
	// Look for m.room.name in the state map. The map type is
	// map[event.Type]map[string]*event.Event; we only need the
	// first event's name content.
	for _, events := range *st {
		for _, ev := range events {
			if ev == nil {
				continue
			}
			if rnc, ok := ev.Content.Parsed.(*event.RoomNameEventContent); ok && rnc.Name != "" {
				return rnc.Name
			}
		}
	}
	return ""
}
