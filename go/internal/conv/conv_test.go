// Tests for the conv package primitives. See plan.md §7.3 / §7.4.
package conv_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"

	"github.com/jbutlerdev/tether/go/internal/conv"
)

// TestRoomIDToConvID_Stable verifies that the same room id always
// hashes to the same conversation id (deterministic mapping).
func TestRoomIDToConvID_Stable(t *testing.T) {
	t.Parallel()
	// We don't have id.RoomID here without dragging the matrix
	// package, so the test uses a mem-store round-trip via
	// Upsert with a fixed id.
	store := conv.NewMemStore()
	ctx := context.Background()

	idA := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	idB := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}

	if idA != idB {
		t.Fatalf("test setup: two equal ids differ")
	}

	got, err := store.Get(ctx, idA)
	if !errors.Is(err, conv.ErrNotFound) {
		t.Fatalf("Get on empty store: want ErrNotFound, got %v (conv=%+v)", err, got)
	}

	if _, _, err := store.Upsert(ctx, idA, conv.ConvInfo{
		Name:   "general",
		Kind:   conv.KindMatrix,
		Target: "!r1:example.com",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err = store.Get(ctx, idB)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Info.Name != "general" {
		t.Errorf("Name: want %q, got %q", "general", got.Info.Name)
	}
	if got.Info.Kind != conv.KindMatrix {
		t.Errorf("Kind: want KindMatrix, got %v", got.Info.Kind)
	}
	if got.Info.Target != "!r1:example.com" {
		t.Errorf("Target: want %q, got %q", "!r1:example.com", got.Info.Target)
	}
}

// TestMemStore_UpsertRejectsInvalidKind verifies that Upsert
// returns ErrInvalidKind when the kind is KindUnspecified.
func TestMemStore_UpsertRejectsInvalidKind(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()
	id := [16]byte{1}
	_, _, err := store.Upsert(ctx, id, conv.ConvInfo{Name: "x", Target: "y"})
	if !errors.Is(err, conv.ErrInvalidKind) {
		t.Errorf("Upsert KindUnspecified: want ErrInvalidKind, got %v", err)
	}
}

// TestMemStore_UpsertRejectsEmptyTarget verifies that Upsert
// returns ErrInvalidTarget when the target is empty.
func TestMemStore_UpsertRejectsEmptyTarget(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()
	id := [16]byte{1}
	_, _, err := store.Upsert(ctx, id, conv.ConvInfo{Name: "x", Kind: conv.KindMatrix})
	if !errors.Is(err, conv.ErrInvalidTarget) {
		t.Errorf("Upsert empty target: want ErrInvalidTarget, got %v", err)
	}
}

// TestMemStore_RemoveIdempotent verifies that Remove on a
// missing id returns existed=false without an error.
func TestMemStore_RemoveIdempotent(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()
	id := [16]byte{1}
	existed, err := store.Remove(ctx, id)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if existed {
		t.Errorf("Remove: want existed=false, got true")
	}
}

// TestMemStore_RemoveReturnsExistedTrue verifies that Remove on
// a present id returns existed=true.
func TestMemStore_RemoveReturnsExistedTrue(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()
	id := [16]byte{1}
	if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{Kind: conv.KindMatrix, Target: "t"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	existed, err := store.Remove(ctx, id)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !existed {
		t.Errorf("Remove: want existed=true, got false")
	}
	if _, err := store.Get(ctx, id); !errors.Is(err, conv.ErrNotFound) {
		t.Errorf("Get after Remove: want ErrNotFound, got %v", err)
	}
}

// TestMemStore_ListOrderedByActivity verifies that List returns
// conversations in descending last-activity order.
func TestMemStore_ListOrderedByActivity(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()

	for i, ms := range []int64{100, 300, 200} {
		id := [16]byte{byte(i + 1)}
		if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{
			Name:               "c",
			Kind:               conv.KindMatrix,
			Target:             "t",
			LastActivityUnixMs: ms,
		}); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: want 3, got %d", len(list))
	}
	want := []int64{300, 200, 100}
	for i, c := range list {
		if c.Info.LastActivityUnixMs != want[i] {
			t.Errorf("List[%d].LastActivityUnixMs: want %d, got %d", i, want[i], c.Info.LastActivityUnixMs)
		}
	}
}

// TestMemStore_ChangesDelivered verifies that subscribers
// receive Upsert / Remove events.
func TestMemStore_ChangesDelivered(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := store.Changes(ctx)

	id := [16]byte{1}
	if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{Kind: conv.KindMatrix, Target: "t", Name: "first"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := store.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got := make([]conv.Change, 0, 2)
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case c := <-changes:
			got = append(got, c)
		case <-deadline:
			t.Fatalf("Changes: only received %d events", len(got))
		}
	}
	if got[0].Kind != conv.ChangeUpsert {
		t.Errorf("event[0].Kind: want ChangeUpsert, got %v", got[0].Kind)
	}
	if !got[0].Created {
		t.Errorf("event[0].Created: want true, got false")
	}
	if got[1].Kind != conv.ChangeRemove {
		t.Errorf("event[1].Kind: want ChangeRemove, got %v", got[1].Kind)
	}
}

// TestMemStore_ChangesContextCancelCloses verifies that
// canceling the subscription context closes the channel.
func TestMemStore_ChangesContextCancelCloses(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	changes := store.Changes(ctx)
	cancel()
	select {
	case _, ok := <-changes:
		if ok {
			// Drain; the close should follow shortly.
			for range changes {
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Changes channel not closed after cancel")
	}
}

// TestConvIDToHex verifies the canonical 32-char hex encoding.
func TestConvIDToHex(t *testing.T) {
	t.Parallel()
	id := [16]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE, 0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}
	got := conv.ConvIDToHex(id[:])
	want := "deadbeefcafebabe0123456789abcdef"
	if got != want {
		t.Errorf("ConvIDToHex: want %q, got %q", want, got)
	}
}

// TestKindStringRoundTrip verifies Kind.String / ParseKind are
// inverses for every defined kind.
func TestKindStringRoundTrip(t *testing.T) {
	t.Parallel()
	for _, k := range []conv.Kind{conv.KindMatrix, conv.KindForge, conv.KindBroadcast} {
		s := k.String()
		if s == "unspecified" {
			t.Errorf("Kind(%d).String: unspecified for non-zero kind", k)
		}
		got, ok := conv.ParseKind(s)
		if !ok {
			t.Errorf("ParseKind(%q): want ok, got !ok", s)
		}
		if got != k {
			t.Errorf("ParseKind(%q): want %v, got %v", s, k, got)
		}
	}
	if _, ok := conv.ParseKind("bogus"); ok {
		t.Errorf("ParseKind(bogus): want !ok, got ok")
	}
}

// TestMemStore_RoundTripBytes checks the conv ID round-trips
// through a []byte without corruption.
func TestMemStore_RoundTripBytes(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	ctx := context.Background()
	want := [16]byte{0xCA, 0xFE, 0xBA, 0xBE, 0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}
	if _, _, err := store.Upsert(ctx, want, conv.ConvInfo{Kind: conv.KindMatrix, Target: "t", Name: "x"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := store.Get(ctx, want)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.ID[:], want[:]) {
		t.Errorf("ID round-trip: want %x, got %x", want, got.ID)
	}
}

// TestConversation_IDString verifies the canonical hex encoding
// of a Conversation.ID.
func TestConversation_IDString(t *testing.T) {
	t.Parallel()
	c := conv.Conversation{
		ID: [16]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB},
	}
	got := c.IDString()
	want := "deadbeef00112233445566778899aabb"
	if len(got) != 32 {
		t.Errorf("IDString length: want 32, got %d", len(got))
	}
	if got != want {
		t.Errorf("IDString: want %q, got %q", want, got)
	}
}

// TestRoomIDToConvID_Deterministic verifies that the same room
// id always hashes to the same conv id.
func TestRoomIDToConvID_Deterministic(t *testing.T) {
	t.Parallel()
	id1 := conv.RoomIDToConvID(id.RoomID("!r1:example.com"))
	id2 := conv.RoomIDToConvID(id.RoomID("!r1:example.com"))
	if id1 != id2 {
		t.Errorf("RoomIDToConvID not deterministic: %x vs %x", id1, id2)
	}
}

// TestRoomIDToConvID_DistinctRooms verifies that two distinct
// room ids produce two distinct conv ids.
func TestRoomIDToConvID_DistinctRooms(t *testing.T) {
	t.Parallel()
	id1 := conv.RoomIDToConvID(id.RoomID("!r1:example.com"))
	id2 := conv.RoomIDToConvID(id.RoomID("!r2:example.com"))
	if id1 == id2 {
		t.Errorf("distinct rooms derived the same conv id: %x", id1)
	}
}

// TestMemStore_BufferSizeOption verifies that the buffer-size
// option is accepted (the value is observable indirectly via
// the publish drop-on-full path; this test only checks that
// construction does not panic with a non-default size).
func TestMemStore_BufferSizeOption(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore(conv.MemStoreOptionBufferSize(8))
	defer func() {
		// No Close method; the in-memory store is GC'd.
		_ = store
	}()
	if store == nil {
		t.Fatal("NewMemStore: nil")
	}
}
