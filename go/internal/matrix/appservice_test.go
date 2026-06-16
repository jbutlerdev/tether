// Tests for the matrix.Appservice core. See plan.md §7.2.
//
// The appservice core is the event-handling layer: it subscribes
// to a Client, filters events (own-user_id, etc.), and dispatches
// OnMessage / OnInvite / OnLeave callbacks. The core is build-tag
// free so it can be exercised in CI with the Mock client; the real
// mautrix-go HTTP listener lives in appservice_mautrix.go (build
// tag `mautrix`).
package matrix_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/matrix"
)

// newMockWithCtx spins up a MockClient and a cancel function.
// The subscription is owned by the appservice (Run calls
// Subscribe); the test injects events via m.InjectEvent.
func newMockWithCtx(t *testing.T) (*matrix.MockClient, <-chan matrix.Event, context.CancelFunc) {
	t.Helper()
	m := matrix.NewMockClient()
	// Returned channel is never used; tests drive the appservice
	// via callbacks. We keep the signature stable for tests that
	// do need direct access to events.
	ch := make(chan matrix.Event)
	cancel := func() { _ = m.Close() }
	t.Cleanup(cancel)
	return m, ch, cancel
}

// drainEvents reads up to n events from ch with a short timeout,
// then returns what was collected. Used to drain events that the
// appservice has already processed but that the test does not
// explicitly assert on.
func drainEvents(ch <-chan matrix.Event, n int, d time.Duration) []matrix.Event {
	out := make([]matrix.Event, 0, n)
	timeout := time.NewTimer(d)
	defer timeout.Stop()
	for i := 0; i < n; i++ {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-timeout.C:
			return out
		}
	}
	return out
}

// TestAppservice_Registers verifies that NewAppservice returns a
// non-nil Appservice that exposes the user_id and registration
// helpers.
func TestAppservice_Registers(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
	})
	if as == nil {
		t.Fatal("NewAppservice: nil")
	}
	if as.UserID() != userID("@tether:example.com") {
		t.Errorf("UserID: want @tether:example.com, got %q", as.UserID())
	}
	if as.Localpart() != "tether" {
		t.Errorf("Localpart: want tether, got %q", as.Localpart())
	}
}

// TestAppservice_ReceivesRoomMessage verifies that an m.room.message
// event with a non-empty body fires the OnMessage callback exactly
// once.
func TestAppservice_ReceivesRoomMessage(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var got atomic.Int32
	var lastBody atomic.Value
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnMessage: func(_ context.Context, ev matrix.Event) {
			got.Add(1)
			lastBody.Store(ev.Body)
		},
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	ev := matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "hello world",
		Time:   time.Now(),
	}
	if !m.InjectEvent(ev) {
		t.Fatal("InjectEvent: no active subscription")
	}

	// Wait for the callback to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Errorf("OnMessage: want 1 call, got %d", got.Load())
	}
	if lb := lastBody.Load(); lb == nil || lb.(string) != "hello world" {
		t.Errorf("OnMessage body: want %q, got %v", "hello world", lb)
	}
}

// TestAppservice_IgnoresOwnMessages verifies that events from the
// appservice's own user_id do NOT fire OnMessage.
func TestAppservice_IgnoresOwnMessages(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var got atomic.Int32
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnMessage: func(_ context.Context, _ matrix.Event) { got.Add(1) },
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	// An event from our own user — should be ignored.
	m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@tether:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "from me",
		Time:   time.Now(),
	})

	// And a follow-up from another user — should be counted.
	m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@bob:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "from bob",
		Time:   time.Now(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Errorf("OnMessage: want 1 call (bob), got %d", got.Load())
	}
}

// TestAppservice_HandlesRoomInvite verifies that an
// m.room.member event with membership=invite fires OnInvite and
// triggers an auto-join via the underlying Client.
func TestAppservice_HandlesRoomInvite(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var gotInvite atomic.Int32
	var lastRoom atomic.Value
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnInvite: func(_ context.Context, ev matrix.Event) {
			gotInvite.Add(1)
			lastRoom.Store(ev.Room)
		},
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	rid := roomID("!r2:example.com")
	m.InjectEvent(matrix.Event{
		Type:   "m.room.member",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "invite",
		Time:   time.Now(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && gotInvite.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if gotInvite.Load() != 1 {
		t.Errorf("OnInvite: want 1 call, got %d", gotInvite.Load())
	}
	if lr := lastRoom.Load(); lr != rid {
		t.Errorf("OnInvite room: want %q, got %v", rid, lr)
	}

	// Verify auto-join happened.
	joined := m.JoinedRooms()
	if len(joined) != 1 || joined[0] != rid {
		t.Errorf("auto-join: want [%q], got %v", rid, joined)
	}
}

// TestAppservice_HandlesRoomLeave verifies that an m.room.member
// event with membership=leave for our own user_id sets a "removed"
// marker (the OnRemove callback is invoked; the appservice
// itself does not call LeaveRoom — that would be the user's
// choice, not ours).
func TestAppservice_HandlesRoomLeave(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var gotRemove atomic.Int32
	var lastRoom atomic.Value
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnRemove:  func(_ context.Context, ev matrix.Event) { gotRemove.Add(1); lastRoom.Store(ev.Room) },
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	rid := roomID("!r3:example.com")
	m.InjectEvent(matrix.Event{
		Type:   "m.room.member",
		Sender: userID("@tether:example.com"), // our own user
		Room:   rid,
		Body:   "leave",
		Time:   time.Now(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && gotRemove.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if gotRemove.Load() != 1 {
		t.Errorf("OnRemove: want 1 call, got %d", gotRemove.Load())
	}
	if lr := lastRoom.Load(); lr != rid {
		t.Errorf("OnRemove room: want %q, got %v", rid, lr)
	}
}

// TestAppservice_ReconnectOnError verifies that if the underlying
// Subscribe channel drops (e.g. a network blip), the appservice
// transparently resubscribes and resumes processing events.
func TestAppservice_ReconnectOnError(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	var got atomic.Int32
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client: m,
		UserID: userID("@tether:example.com"),
		OnMessage: func(_ context.Context, _ matrix.Event) {
			got.Add(1)
		},
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	// First event — should be received.
	if !m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "first",
		Time:   time.Now(),
	}) {
		t.Fatal("InjectEvent: no subscription after first subscribe")
	}

	// Wait for the first OnMessage.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() != 1 {
		t.Fatalf("OnMessage: want 1 call before disconnect, got %d", got.Load())
	}

	// Simulate a disconnect: the appservice should resubscribe.
	m.Disconnect()

	// Wait for the appservice to resubscribe. InjectEvent returns
	// false while no subscription is active; we retry until it
	// returns true (or timeout).
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.InjectEvent(matrix.Event{
			Type:   "m.room.message",
			Sender: userID("@alice:example.com"),
			Room:   roomID("!r1:example.com"),
			Body:   "second",
			Time:   time.Now(),
		}) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for the second OnMessage.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() != 2 {
		t.Errorf("OnMessage: want 2 calls (one pre-, one post-reconnect), got %d", got.Load())
	}
}

// TestAppservice_IgnoresEmptyBody verifies that an m.room.message
// event with an empty body does NOT fire OnMessage (we use empty
// body as the "no text content" signal — see room_to_conv.go §7.3
// for the parallel rule there).
func TestAppservice_IgnoresEmptyBody(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var got atomic.Int32
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnMessage: func(_ context.Context, _ matrix.Event) { got.Add(1) },
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   roomID("!r1:example.com"),
		Body:   "",
		Time:   time.Now(),
	})

	time.Sleep(200 * time.Millisecond)
	if got.Load() != 0 {
		t.Errorf("OnMessage for empty body: want 0 calls, got %d", got.Load())
	}
}

// TestAppservice_HandlesRenameCommand verifies that a
// /tether rename <name> message updates the room alias mapping
// (the OnRename callback fires; the appservice itself stores
// the new name in its internal alias map).
func TestAppservice_HandlesRenameCommand(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var gotRename atomic.Int32
	var lastName atomic.Value
	var lastRoom atomic.Value
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    m,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		OnRename: func(_ context.Context, ev matrix.Event, name string) {
			gotRename.Add(1)
			lastName.Store(name)
			lastRoom.Store(ev.Room)
		},
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	rid := roomID("!r4:example.com")
	m.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "/tether rename Foo",
		Time:   time.Now(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && gotRename.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if gotRename.Load() != 1 {
		t.Errorf("OnRename: want 1 call, got %d", gotRename.Load())
	}
	if ln := lastName.Load(); ln != "Foo" {
		t.Errorf("OnRename name: want %q, got %v", "Foo", ln)
	}
	if lr := lastRoom.Load(); lr != rid {
		t.Errorf("OnRename room: want %q, got %v", rid, lr)
	}
	if as.RoomName(rid) != "Foo" {
		t.Errorf("RoomName: want %q, got %q", "Foo", as.RoomName(rid))
	}
}

// TestAppservice_RunReturnsOnContextCancel verifies that Run
// returns nil when the supplied context is canceled.
func TestAppservice_RunReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	m := matrix.NewMockClient()
	defer m.Close()

	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client: m,
		UserID: userID("@tether:example.com"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- as.Run(ctx) }()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run: want nil or context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run: did not return after ctx cancel")
	}
}

// TestAppservice_ConcurrentInvariants is a race-detector smoke
// test that fires many events from many goroutines while the
// appservice is running. Coverage and -race together catch any
// shared-state bugs in the dispatcher.
func TestAppservice_ConcurrentInvariants(t *testing.T) {
	t.Parallel()
	m, _, cancel := newMockWithCtx(t)
	defer cancel()

	var got atomic.Int32
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client: m,
		UserID: userID("@tether:example.com"),
		OnMessage: func(_ context.Context, _ matrix.Event) {
			got.Add(1)
		},
	})

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(runCtx)
	}()

	// Wait for the appservice to subscribe (no fixed sleep — race-free).
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if !m.WaitForSubscription(waitCtx) {
		waitCancel()
		t.Fatal("appservice did not subscribe within 2s")
	}
	waitCancel()

	var wg sync.WaitGroup
	const n = 64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.InjectEvent(matrix.Event{
				Type:   "m.room.message",
				Sender: userID("@alice:example.com"),
				Room:   roomID("!r1:example.com"),
				Body:   "msg",
				Time:   time.Now(),
			})
		}(i)
	}
	wg.Wait()

	// Wait for processing to catch up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && got.Load() < n {
		time.Sleep(10 * time.Millisecond)
	}
	if got.Load() < n {
		t.Errorf("OnMessage: want at least %d calls, got %d", n, got.Load())
	}
}
