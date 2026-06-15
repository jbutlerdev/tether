// e2ee_test.go — v2 hook tests for matrix.DecryptEvent. See
// plan.md §10.4 (Task 9.4).
//
// These tests pin the v2 hook surface. They assert:
//
//   - DecryptEvent is a callable function in the matrix package.
//   - The v1 build returns ErrE2EENotImplemented for any
//     non-zero event, with a useful error message that includes
//     the room and sender for log correlation.
//   - A zero event returns a zero Event with no error (so v1
//     callers can no-op the hook without inspecting every
//     call site for an error).
//   - ctx cancellation is honoured.
//
// When v2 lands, the "non-zero event" test is inverted: the
// function must return a real Event, not an error.
package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"

	"maunium.net/go/mautrix/id"
)

// TestV2Hook_E2EE_StubExists asserts the v2 hook point is
// callable in v1. The test does not care what DecryptEvent
// returns — it just verifies the symbol exists with the
// expected signature.
func TestV2Hook_E2EE_StubExists(t *testing.T) {
	var fn func(ctx context.Context, ev EncryptedEvent) (Event, error) = DecryptEvent
	if fn == nil {
		t.Fatal("DecryptEvent is nil; v2 hook missing")
	}
}

// TestV2Hook_E2EE_NonZeroEventReturnsNotImplemented asserts
// that v1 explicitly signals "not implemented" so v2 callers
// do not silently treat encrypted events as plaintext. v2
// will replace this with a real decryption; the test will be
// inverted to assert the opposite.
func TestV2Hook_E2EE_NonZeroEventReturnsNotImplemented(t *testing.T) {
	ev := EncryptedEvent{
		Room:       id.RoomID("!room:matrix.org"),
		Sender:     id.UserID("@alice:matrix.org"),
		Ciphertext: "AwgZEsKwL9hXdGqM7yP3gw...",
		SessionID:  "m.megolm.v1.abc123",
	}
	_, err := DecryptEvent(context.Background(), ev)
	if err == nil {
		t.Fatal("DecryptEvent on non-zero event returned nil; v1 must return ErrE2EENotImplemented")
	}
	if !errors.Is(err, ErrE2EENotImplemented) {
		t.Fatalf("DecryptEvent err = %v, want ErrE2EENotImplemented", err)
	}
	// The error message must include the room and sender
	// for log correlation.
	if !strings.Contains(err.Error(), "!room:matrix.org") {
		t.Errorf("error message missing room: %v", err)
	}
	if !strings.Contains(err.Error(), "@alice:matrix.org") {
		t.Errorf("error message missing sender: %v", err)
	}
}

// TestV2Hook_E2EE_ZeroEventNoop asserts the v1 stub is a
// no-op for the zero event so production code that calls
// DecryptEvent on every event can short-circuit without
// checking errors.
func TestV2Hook_E2EE_ZeroEventNoop(t *testing.T) {
	got, err := DecryptEvent(context.Background(), EncryptedEvent{})
	if err != nil {
		t.Fatalf("DecryptEvent on zero event returned error: %v", err)
	}
	if got.Type != "" || got.Body != "" || got.Room != "" || got.Sender != "" {
		t.Errorf("zero event returned non-zero Event: %+v", got)
	}
}

// TestV2Hook_E2EE_CtxCancel asserts the v1 hook honours
// context cancellation, so a slow v2 decryption cannot
// block a request past its deadline.
func TestV2Hook_E2EE_CtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ev := EncryptedEvent{
		Room:       id.RoomID("!room:matrix.org"),
		Sender:     id.UserID("@alice:matrix.org"),
		Ciphertext: "x",
		SessionID:  "s",
	}
	_, err := DecryptEvent(ctx, ev)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DecryptEvent on cancelled ctx returned %v, want context.Canceled", err)
	}
}
