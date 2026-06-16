// Tests for the matrix.Mapper. See plan.md §7.3.
package matrix_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/matrix"
)

// fixedNamer is a tiny RoomNamer for tests that returns the same
// name for every room unless overridden per-room.
type fixedNamer struct {
	mu    sync.Mutex
	names map[id.RoomID]string
}

func newFixedNamer() *fixedNamer { return &fixedNamer{names: map[id.RoomID]string{}} }

func (f *fixedNamer) set(roomID id.RoomID, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[roomID] = name
}

func (f *fixedNamer) RoomName(_ context.Context, roomID id.RoomID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.names[roomID]
}

// TestMapper_RoomIDStableUUID verifies that two OnRoomMessage
// calls on the same room land in the same conversation row.
// The deterministic id derivation is tested in conv/conv_test.go
// (TestRoomIDToConvID_Stable); here we exercise the mapper
// integration.
func TestMapper_RoomIDStableUUID(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r1:example.com")
	namer.set(rid, "general")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "hello",
		Time:   time.Unix(1700000000, 0).UTC(),
	})
	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "world",
		Time:   time.Unix(1700000001, 0).UTC(),
	})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1 conversation, got %d", len(list))
	}
	if list[0].Info.Name != "general" {
		t.Errorf("Name: want %q, got %q", "general", list[0].Info.Name)
	}
	if list[0].Info.Kind != conv.KindMatrix {
		t.Errorf("Kind: want KindMatrix, got %v", list[0].Info.Kind)
	}
	if list[0].Info.Target != rid.String() {
		t.Errorf("Target: want %q, got %q", rid.String(), list[0].Info.Target)
	}
	if list[0].Info.UnreadCount != 2 {
		t.Errorf("UnreadCount: want 2, got %d", list[0].Info.UnreadCount)
	}
	if list[0].Info.LastActivityUnixMs != int64(1700000001*1000) {
		t.Errorf("LastActivityUnixMs: want %d, got %d", int64(1700000001*1000), list[0].Info.LastActivityUnixMs)
	}
}

// TestMapper_UpdatesRoomName verifies that OnRoomRenamed updates
// the stored name.
func TestMapper_UpdatesRoomName(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r2:example.com")
	namer.set(rid, "old")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "first",
		Time:   time.Unix(1700000000, 0).UTC(),
	})

	mapper.OnRoomRenamed(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "/tether rename newname",
		Time:   time.Unix(1700000001, 0).UTC(),
	}, "newname")

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1 conversation, got %d", len(list))
	}
	if list[0].Info.Name != "newname" {
		t.Errorf("Name after rename: want %q, got %q", "newname", list[0].Info.Name)
	}
}

// TestMapper_IgnoresEmptyMessages verifies that an event with an
// empty body is dropped (no conversation created or updated).
func TestMapper_IgnoresEmptyMessages(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r3:example.com")
	namer.set(rid, "general")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "",
		Time:   time.Now(),
	})
	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "   ",
		Time:   time.Now(),
	})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List: want 0 (empty bodies dropped), got %d", len(list))
	}
}

// TestMapper_IgnoresOwnMessages verifies that an event whose
// sender is the appservice's own user_id is dropped.
func TestMapper_IgnoresOwnMessages(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r4:example.com")
	namer.set(rid, "general")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@tether:example.com"), // our own puppet
		Room:   rid,
		Body:   "echo",
		Time:   time.Now(),
	})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List: want 0 (own messages dropped), got %d", len(list))
	}
}

// TestMapper_OnRoomRemoved verifies that OnRoomRemoved deletes
// the conversation from the store.
func TestMapper_OnRoomRemoved(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r5:example.com")
	namer.set(rid, "general")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "hi",
		Time:   time.Now(),
	})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1, got %d", len(list))
	}

	mapper.OnRoomRemoved(ctx, matrix.Event{
		Type:   "m.room.member",
		Sender: userID("@tether:example.com"),
		Room:   rid,
		Body:   "leave",
		Time:   time.Now(),
	})

	list, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after remove: want 0, got %d", len(list))
	}
}

// TestMapper_RenameBeforeMessageInserts verifies that renaming
// a non-existent conversation creates it (rather than erroring).
func TestMapper_RenameBeforeMessageInserts(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid := roomID("!r6:example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomRenamed(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "/tether rename early",
		Time:   time.Now(),
	}, "early")

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1, got %d", len(list))
	}
	if list[0].Info.Name != "early" {
		t.Errorf("Name: want %q, got %q", "early", list[0].Info.Name)
	}
}

// TestMapper_DifferentRoomsDifferentIDs verifies that two
// distinct room_ids map to two distinct conversations.
func TestMapper_DifferentRoomsDifferentIDs(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	namer := newFixedNamer()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"),
		matrix.MapperOptionNamer(namer),
	)
	rid1 := roomID("!r1:example.com")
	rid2 := roomID("!r2:example.com")
	namer.set(rid1, "one")
	namer.set(rid2, "two")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid1,
		Body:   "a",
		Time:   time.Now(),
	})
	mapper.OnRoomMessage(ctx, matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid2,
		Body:   "b",
		Time:   time.Now(),
	})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: want 2, got %d", len(list))
	}
	ids := map[string]bool{list[0].IDString(): true, list[1].IDString(): true}
	if !ids[conv.ConvIDToHex(matrix_roomIDToBytes(t, rid1))] {
		t.Errorf("missing conversation for rid1")
	}
	if !ids[conv.ConvIDToHex(matrix_roomIDToBytes(t, rid2))] {
		t.Errorf("missing conversation for rid2")
	}
	if conv.ConvIDToHex(matrix_roomIDToBytes(t, rid1)) == conv.ConvIDToHex(matrix_roomIDToBytes(t, rid2)) {
		t.Errorf("two distinct rooms derived the same conv id")
	}
}

// matrix_roomIDToBytes returns the 16-byte conversation id for a
// room id, for use in assertions. Re-exposes conv.RoomIDToConvID.
func matrix_roomIDToBytes(t *testing.T, rid id.RoomID) []byte {
	t.Helper()
	id := conv.RoomIDToConvID(rid)
	return id[:]
}

// TestAppserviceRoomNamer_AliasWins verifies that the appservice
// alias takes precedence over the room state.
func TestAppserviceRoomNamer_AliasWins(t *testing.T) {
	t.Parallel()
	appsvc := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    matrix.NewMockClient(),
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
	})
	rid := roomID("!r:example.com")
	// Set the alias via the appservice's setRoomName path: we
	// go through the OnRename handler so the alias map is
	// populated.
	appsvc.HandleRenameForTest(rid, "alias-name")

	namer := matrix.AppserviceRoomNamer{Appsvc: appsvc}
	got := namer.RoomName(context.Background(), rid)
	if got != "alias-name" {
		t.Errorf("RoomName: want %q (alias), got %q", "alias-name", got)
	}
}

// TestAppserviceRoomNamer_NoAliasNoClient verifies that with no
// alias and no client, the namer returns the empty string.
func TestAppserviceRoomNamer_NoAliasNoClient(t *testing.T) {
	t.Parallel()
	appsvc := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    matrix.NewMockClient(),
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
	})
	namer := matrix.AppserviceRoomNamer{Appsvc: appsvc}
	got := namer.RoomName(context.Background(), roomID("!r:example.com"))
	if got != "" {
		t.Errorf("RoomName: want %q, got %q", "", got)
	}
}

// TestAppserviceRoomNamer_FallsBackToRoomState verifies that
// when no alias is set, the namer falls back to GetRoomState.
func TestAppserviceRoomNamer_FallsBackToRoomState(t *testing.T) {
	t.Parallel()
	mc := matrix.NewMockClient()
	defer mc.Close()
	rid := roomID("!rstate:example.com")
	mc.SetRoomState(rid, &mautrix.RoomStateMap{
		event.NewEventType("m.room.name"): {
			"$evt1:example.com": &event.Event{
				Type: event.NewEventType("m.room.name"),
				Content: event.Content{
					Parsed: &event.RoomNameEventContent{Name: "from-state"},
				},
			},
		},
	})
	appsvc := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    mc,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
	})
	namer := matrix.AppserviceRoomNamer{Appsvc: appsvc, Client: mc}
	got := namer.RoomName(context.Background(), rid)
	if got != "from-state" {
		t.Errorf("RoomName: want %q, got %q", "from-state", got)
	}
}
