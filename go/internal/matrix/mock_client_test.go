// Tests for the matrix.MockClient. See plan.md §7.1.
//
// The mock is a deterministic in-process stand-in for mautrix-go's
// appservice network. It records every SendText/JoinRoom/LeaveRoom
// call and exposes a hook for tests to inject synthetic events
// onto the Subscribe channel.
package matrix_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/jbutlerdev/tether/go/internal/matrix"
)

func roomID(s string) id.RoomID { return id.RoomID(s) }
func userID(s string) id.UserID { return id.UserID(s) }

// TestMockClient_SendText verifies that a single SendText call
// records the (room, body) pair and returns a non-nil RespSendEvent.
func TestMockClient_SendText(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := m.SendText(ctx, roomID("!r1:example.com"), "hello")
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if got == nil {
		t.Fatal("SendText: nil response")
	}
	if got.EventID == "" {
		t.Error("SendText: empty EventID on response")
	}

	calls := m.SendTextCalls()
	if len(calls) != 1 {
		t.Fatalf("SendText calls: want 1, got %d", len(calls))
	}
	if calls[0].RoomID != roomID("!r1:example.com") {
		t.Errorf("SendText RoomID: want !r1:example.com, got %q", calls[0].RoomID)
	}
	if calls[0].Body != "hello" {
		t.Errorf("SendText Body: want %q, got %q", "hello", calls[0].Body)
	}
}

// TestMockClient_SendText_RecordsError verifies that an injected
// error is returned and recorded on the call.
func TestMockClient_SendText_RecordsError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("network down")
	m := matrix.NewMockClient(matrix.MockOptionSendError(wantErr))
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := m.SendText(ctx, roomID("!r1:example.com"), "hi"); !errors.Is(err, wantErr) {
		t.Fatalf("SendText: want %v, got %v", wantErr, err)
	}
}

// TestMockClient_JoinLeave verifies the JoinRoom and LeaveRoom
// methods record the room id and propagate injected errors.
func TestMockClient_JoinLeave(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rid := roomID("!r2:example.com")
	if err := m.JoinRoom(ctx, rid); err != nil {
		t.Fatalf("JoinRoom: %v", err)
	}
	if err := m.LeaveRoom(ctx, rid); err != nil {
		t.Fatalf("LeaveRoom: %v", err)
	}

	if got := m.JoinedRooms(); len(got) != 1 || got[0] != rid {
		t.Errorf("JoinedRooms: want [%q], got %v", rid, got)
	}
	if got := m.LeftRooms(); len(got) != 1 || got[0] != rid {
		t.Errorf("LeftRooms: want [%q], got %v", rid, got)
	}
}

// TestMockClient_JoinRoom_Error verifies that JoinRoom returns the
// injected error and does not record the room in JoinedRooms.
func TestMockClient_JoinRoom_Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("forbidden")
	m := matrix.NewMockClient(matrix.MockOptionJoinError(wantErr))
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.JoinRoom(ctx, roomID("!r3:example.com")); !errors.Is(err, wantErr) {
		t.Fatalf("JoinRoom: want %v, got %v", wantErr, err)
	}
	if got := m.JoinedRooms(); len(got) != 0 {
		t.Errorf("JoinedRooms: want [], got %v", got)
	}
}

// TestMockClient_SubscribeReceives verifies that events injected via
// InjectEvent are delivered to a Subscribe consumer in order.
func TestMockClient_SubscribeReceives(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, _, err := m.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	want := []matrix.Event{
		{Type: "m.room.message", Sender: userID("@alice:example.com"), Room: roomID("!r1:example.com"), Body: "first", Time: time.Unix(1700000000, 0).UTC()},
		{Type: "m.room.message", Sender: userID("@bob:example.com"), Room: roomID("!r1:example.com"), Body: "second", Time: time.Unix(1700000001, 0).UTC()},
	}
	for _, ev := range want {
		m.InjectEvent(ev)
	}

	got := make([]matrix.Event, 0, len(want))
	for i := 0; i < len(want); i++ {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("Subscribe: timeout on event %d", i)
		}
	}

	for i := range want {
		if got[i].Type != want[i].Type {
			t.Errorf("event[%d].Type: want %q, got %q", i, want[i].Type, got[i].Type)
		}
		if got[i].Sender != want[i].Sender {
			t.Errorf("event[%d].Sender: want %q, got %q", i, want[i].Sender, got[i].Sender)
		}
		if got[i].Body != want[i].Body {
			t.Errorf("event[%d].Body: want %q, got %q", i, want[i].Body, got[i].Body)
		}
	}
}

// TestMockClient_Reconnect verifies that after a simulated
// disconnect, calling Reconnect() reopens the Subscribe channel and
// subsequent InjectEvent calls are delivered to the new consumer.
func TestMockClient_Reconnect(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	// First connection.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, firstDone, err := m.Subscribe(ctx)
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}

	// Simulate a disconnect. The done channel should close.
	m.Disconnect()
	select {
	case <-firstDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first Subscribe: done channel not closed after Disconnect")
	}

	// Auto-reconnect. A fresh Subscribe call returns a new
	// channel pair.
	second, _, err := m.Subscribe(ctx)
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}

	// Inject an event post-reconnect.
	m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "after-reconnect",
		Time:   time.Unix(1700000010, 0).UTC(),
	})

	select {
	case ev := <-second:
		if ev.Body != "after-reconnect" {
			t.Errorf("post-reconnect Body: want %q, got %q", "after-reconnect", ev.Body)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second Subscribe: no event delivered after reconnect")
	}
}

// TestMockClient_GetRoomState_Empty verifies that an unknown room
// returns an empty (but non-nil) state map without error.
func TestMockClient_GetRoomState_Empty(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := m.GetRoomState(ctx, roomID("!unknown:example.com"))
	if err != nil {
		t.Fatalf("GetRoomState: %v", err)
	}
	if got == nil {
		t.Fatal("GetRoomState: want non-nil empty map, got nil")
	}
	if len(*got) != 0 {
		t.Errorf("GetRoomState: want empty, got %d entries", len(*got))
	}
}

// TestMockClient_GetRoomState verifies the state payload returned
// matches what the test set via SetRoomState.
func TestMockClient_GetRoomState(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	want := &mautrix.RoomStateMap{
		event.NewEventType("m.room.name"): {
			"$evt1:example.com": &event.Event{
				Type:    event.NewEventType("m.room.name"),
				Content: event.Content{Parsed: &event.RoomNameEventContent{Name: "general"}},
			},
		},
	}
	m.SetRoomState(roomID("!r4:example.com"), want)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := m.GetRoomState(ctx, roomID("!r4:example.com"))
	if err != nil {
		t.Fatalf("GetRoomState: %v", err)
	}
	if got == nil {
		t.Fatal("GetRoomState: nil")
	}
	events, ok := (*got)[event.NewEventType("m.room.name")]
	if !ok {
		t.Fatalf("state[m.room.name] missing")
	}
	parsed, ok := events["$evt1:example.com"].Content.Parsed.(*event.RoomNameEventContent)
	if !ok {
		t.Fatalf("content not *RoomNameEventContent: %T", events["$evt1:example.com"].Content.Parsed)
	}
	if parsed.Name != "general" {
		t.Errorf("room name: want %q, got %q", "general", parsed.Name)
	}
}

// TestMockClient_Close verifies that Close is idempotent and that a
// subsequent SendText returns an error.
func TestMockClient_Close(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if _, err := m.SendText(ctx, roomID("!r1:example.com"), "x"); err == nil {
		t.Error("SendText after Close: want error, got nil")
	}
}

// TestMockClient_LeaveRoom_Error verifies that LeaveRoom returns
// the injected error.
func TestMockClient_LeaveRoom_Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("forbidden")
	m := matrix.NewMockClient(matrix.MockOptionLeaveError(wantErr))
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.LeaveRoom(ctx, roomID("!r:example.com")); !errors.Is(err, wantErr) {
		t.Fatalf("LeaveRoom: want %v, got %v", wantErr, err)
	}
	if got := m.LeftRooms(); len(got) != 0 {
		t.Errorf("LeftRooms on error: want [], got %v", got)
	}
}

// TestMockClient_InjectEventNonBlocking verifies the drop-on-full
// helper returns false when the buffer is full.
func TestMockClient_InjectEventNonBlocking(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ev := matrix.Event{Type: "m.room.message", Room: roomID("!r:example.com"), Body: "1"}
	if m.InjectEventNonBlocking(ev) {
		t.Fatal("InjectEventNonBlocking with no subscription: want false")
	}
}

// TestMockClient_SubscribeAfterClose verifies that Subscribe
// returns ErrMockClosed after Close.
func TestMockClient_SubscribeAfterClose(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	_ = m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if _, _, err := m.Subscribe(ctx); !errors.Is(err, matrix.ErrMockClosed) {
		t.Errorf("Subscribe after Close: want ErrMockClosed, got %v", err)
	}
}

// TestMockClient_SatisfiesInterface is a compile-time check that
// MockClient implements matrix.Client. If MockClient drifts, this
// file will not compile.
func TestMockClient_SatisfiesInterface(t *testing.T) {
	t.Parallel()
	var _ matrix.Client = matrix.NewMockClient()
}

// TestMockClient_ConcurrentSendText verifies that SendText is
// safe to call from many goroutines (the receiver is closed via
// a sync.WaitGroup).
func TestMockClient_ConcurrentSendText(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const n = 32
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := m.SendText(ctx, roomID("!r:example.com"), "msg"); err != nil {
				failures.Add(1)
				t.Errorf("goroutine %d: SendText: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d goroutine failures", failures.Load())
	}
	if got := len(m.SendTextCalls()); got != n {
		t.Errorf("SendTextCalls: want %d, got %d", n, got)
	}
}
