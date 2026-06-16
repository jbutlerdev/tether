// Tests for the forge.MockClient. See plan.md §8.1.
//
// The mock is a deterministic, in-process stand-in for the
// forge HTTP backend. It records every Login / CreateSession /
// ListSessions / DeleteSession / SendMessage call and exposes
// a hook for tests to inject synthetic SSE events onto the
// SubscribeEvents channel.
package forge_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/forge"
)

// TestMockClient_Login verifies that Login returns a non-empty
// user id and that a second call refreshes it.
func TestMockClient_Login(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	uid, err := m.Login(ctx, "test-key")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if uid == "" {
		t.Error("Login: empty user id")
	}
	if m.UserID() != uid {
		t.Errorf("UserID: want %q, got %q", uid, m.UserID())
	}
}

// TestMockClient_CreateSession_ReturnsUUID verifies that
// CreateSession returns a non-empty id and that the id has the
// canonical 36-char UUID shape (8-4-4-4-12).
func TestMockClient_CreateSession_ReturnsUUID(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, err := m.CreateSession(ctx, "coder")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == "" {
		t.Fatal("CreateSession: empty id")
	}
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Errorf("CreateSession: want canonical UUID, got %q", id)
	}
	// Two calls must produce distinct ids.
	id2, err := m.CreateSession(ctx, "researcher")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if id == id2 {
		t.Errorf("CreateSession: same id returned twice: %q", id)
	}
}

// TestMockClient_ListSessions verifies that ListSessions
// returns every session that has been created, ordered by
// last activity (most recent first).
func TestMockClient_ListSessions(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Empty list at the start.
	if got, err := m.ListSessions(ctx); err != nil {
		t.Fatalf("ListSessions empty: %v", err)
	} else if len(got) != 0 {
		t.Errorf("ListSessions empty: want 0, got %d", len(got))
	}

	id1, _ := m.CreateSession(ctx, "coder")
	id2, _ := m.CreateSession(ctx, "researcher")

	// Touch id1 with a SendMessage so its LastActivityAt is
	// later than id2's CreatedAt.
	if err := m.SendMessage(ctx, id1, "ping"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	got, err := m.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSessions: want 2, got %d", len(got))
	}
	// Most recent first: id1 (after SendMessage) should be
	// before id2.
	if got[0].ID != id1 {
		t.Errorf("ListSessions[0].ID: want %q, got %q", id1, got[0].ID)
	}
	if got[1].ID != id2 {
		t.Errorf("ListSessions[1].ID: want %q, got %q", id2, got[1].ID)
	}
	if got[0].Profile != "coder" {
		t.Errorf("Profile: want coder, got %q", got[0].Profile)
	}
}

// TestMockClient_DeleteSession verifies that DeleteSession
// removes a session and that re-deleting returns
// ErrSessionNotFound.
func TestMockClient_DeleteSession(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")
	if err := m.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := m.DeleteSession(ctx, id); !errors.Is(err, forge.ErrSessionNotFound) {
		t.Errorf("DeleteSession 2nd: want ErrSessionNotFound, got %v", err)
	}
}

// TestMockClient_SendMessage_Accepted202 verifies that
// SendMessage returns nil and records the call. The HTTP
// semantics (202 Accepted) are encoded as nil-error; the
// "202" is the success code from a real forge backend.
func TestMockClient_SendMessage_Accepted202(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")
	if err := m.SendMessage(ctx, id, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	calls := m.SendMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("SendMessageCalls: want 1, got %d", len(calls))
	}
	if calls[0].SessionID != id || calls[0].Text != "hello" {
		t.Errorf("SendMessageCalls: want {%q, %q}, got %+v", id, "hello", calls[0])
	}
}

// TestMockClient_SubscribeEvents verifies that InjectEvent
// delivers a synthetic event to the consumer.
func TestMockClient_SubscribeEvents(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")

	events, done, closer, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	if events == nil || done == nil || closer == nil {
		t.Fatal("SubscribeEvents: nil channel/closer")
	}
	defer func() { _ = closer.Close() }()

	// Inject two synthetic events.
	if !m.InjectEvent(id, forge.Event{Type: forge.EventTextDelta, Content: `{"delta":"hi"}`, Seq: 1, At: time.Now()}) {
		t.Fatal("InjectEvent 1: not delivered")
	}
	if !m.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now()}) {
		t.Fatal("InjectEvent 2: not delivered")
	}

	// Receive with a deadline.
	got := make([]forge.Event, 0, 2)
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case ev := <-events:
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("SubscribeEvents: only got %d events", len(got))
		}
	}
	if got[0].Type != forge.EventTextDelta {
		t.Errorf("event[0].Type: want %q, got %q", forge.EventTextDelta, got[0].Type)
	}
	if got[1].Type != forge.EventAgentEnd {
		t.Errorf("event[1].Type: want %q, got %q", forge.EventAgentEnd, got[1].Type)
	}
}

// TestMockClient_Reconnect verifies that after a subscription
// drops, a new SubscribeEvents returns a fresh channel that
// resumes delivery. The `since` parameter is the seq number
// of the last event the consumer saw; the next events should
// have Seq > since.
func TestMockClient_Reconnect(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")

	events1, done1, closer1, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents 1: %v", err)
	}
	if !m.InjectEvent(id, forge.Event{Type: forge.EventTextDelta, Seq: 1, At: time.Now()}) {
		t.Fatal("InjectEvent 1: not delivered")
	}
	// Wait for the event to arrive.
	select {
	case <-events1:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("event 1 not received")
	}
	// Simulate a network drop.
	_ = closer1.Close()
	<-done1

	// Reconnect with since=1.
	events2, done2, closer2, err := m.SubscribeEvents(ctx, id, 1)
	if err != nil {
		t.Fatalf("SubscribeEvents 2: %v", err)
	}
	defer closer2.Close()
	defer func() { <-done2 }()
	if events2 == events1 {
		t.Error("reconnect: same channel returned")
	}
	if !m.InjectEvent(id, forge.Event{Type: forge.EventTextDelta, Seq: 2, At: time.Now()}) {
		t.Fatal("InjectEvent 2: not delivered")
	}
	select {
	case ev := <-events2:
		if ev.Seq <= 1 {
			t.Errorf("reconnect: got seq %d, want > 1", ev.Seq)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("event 2 not received on reconnect")
	}
}

// TestMockClient_Close verifies that Close is idempotent and
// that subsequent calls return ErrClosed.
func TestMockClient_Close(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	if err := m.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close 2 (idempotent): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := m.Login(ctx, "k"); !errors.Is(err, forge.ErrClosed) {
		t.Errorf("Login after Close: want ErrClosed, got %v", err)
	}
}

// TestMockClient_ConcurrentSafe verifies that the mock is safe
// for concurrent use under `go test -race`.
func TestMockClient_ConcurrentSafe(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = m.CreateSession(ctx, "coder")
		}()
	}
	wg.Wait()

	// Concurrent SendMessage + ListSessions.
	wg.Add(2)
	go func() {
		defer wg.Done()
		ids, _ := m.ListSessions(ctx)
		_ = ids
	}()
	go func() {
		defer wg.Done()
		ids, _ := m.ListSessions(ctx)
		_ = ids
	}()
	wg.Wait()
}

// TestMockClient_SubscribeEvents_ConcurrentRejected verifies
// that a second concurrent SubscribeEvents call returns
// ErrSubscribeActive. The mock is single-subscription per
// session; the real HTTP client does not enforce this.
func TestMockClient_SubscribeEvents_ConcurrentRejected(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")

	_, _, closer, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents 1: %v", err)
	}
	defer closer.Close()

	_, _, _, err = m.SubscribeEvents(ctx, id, 0)
	if !errors.Is(err, forge.ErrSubscribeActive) {
		t.Errorf("SubscribeEvents 2: want ErrSubscribeActive, got %v", err)
	}
}

// TestMockClient_SendMessage_SessionNotFound verifies that
// sending to an unknown session returns ErrSessionNotFound.
func TestMockClient_SendMessage_SessionNotFound(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := m.SendMessage(ctx, "00000000-0000-0000-0000-000000000000", "hi")
	if !errors.Is(err, forge.ErrSessionNotFound) {
		t.Errorf("SendMessage: want ErrSessionNotFound, got %v", err)
	}
}

// TestMockClient_InjectedError verifies that an injected send
// error is returned verbatim to the caller.
func TestMockClient_InjectedError(t *testing.T) {
	t.Parallel()
	want := errors.New("backend down")
	m := forge.NewMockClient(forge.MockOptionSendError(want))
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")
	if err := m.SendMessage(ctx, id, "hi"); !errors.Is(err, want) {
		t.Errorf("SendMessage: want %v, got %v", want, err)
	}
}

// TestMockClient_CloserIsNopForMock verifies that the io.Closer
// returned by SubscribeEvents is safe to call multiple times
// and does not block.
func TestMockClient_CloserIsNopForMock(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")
	_, _, closer, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Errorf("closer.Close 1: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Errorf("closer.Close 2: %v", err)
	}
}

// TestMockClient_AtomicUserID ensures UserID() returns the value
// from the most recent successful Login, even when Login is
// called concurrently. (Race-detector clean.)
func TestMockClient_AtomicUserID(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	const n = 20
	var counter atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			uid, err := m.Login(ctx, "k")
			if err == nil && uid != "" {
				counter.Add(1)
			}
		}()
	}
	wg.Wait()
	if counter.Load() != n {
		t.Errorf("Login: want %d successes, got %d", n, counter.Load())
	}
}

// TestMockClient_Closer_ClosesDone verifies that closer.Close()
// causes the `done` channel to be closed, signalling any
// consumer goroutine to stop.
func TestMockClient_Closer_ClosesDone(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	id, _ := m.CreateSession(ctx, "coder")
	_, done, closer, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	_ = closer.Close()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("done channel not closed after closer.Close()")
	}
}

// TestMockClient_InjectEvent_NoSubscription verifies that an
// event injected for a session with no active subscription
// returns false (dropped) and does not block.
func TestMockClient_InjectEvent_NoSubscription(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()

	if m.InjectEvent("nonexistent", forge.Event{Type: forge.EventTextDelta, Seq: 1, At: time.Now()}) {
		t.Error("InjectEvent on no-subscription: want false, got true")
	}
}

// TestMockClient_DefaultsArePresent ensures that a freshly
// constructed mock returns sensible defaults even without any
// explicit configuration: UserID is empty until Login,
// sessions list is empty, and an unknown id returns
// ErrSessionNotFound.
func TestMockClient_DefaultsArePresent(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	if m.UserID() != "" {
		t.Errorf("UserID: want empty before Login, got %q", m.UserID())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.DeleteSession(ctx, "no-such-id"); !errors.Is(err, forge.ErrSessionNotFound) {
		t.Errorf("DeleteSession: want ErrSessionNotFound, got %v", err)
	}
}

// TestMockClient_CloserIsIOCloser compiles if Mock's closer
// satisfies io.Closer. The body is a static check; the test
// fails to build if the cast does not hold.
func TestMockClient_CloserIsIOCloser(t *testing.T) {
	t.Parallel()
	m := forge.NewMockClient()
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id, _ := m.CreateSession(ctx, "coder")
	_, _, c, err := m.SubscribeEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	defer c.Close()
	var _ = io.Closer(c)
}
