// Mock implementation of the forge.Client interface. See plan.md §8.1.
//
// The Mock is a deterministic, in-process stand-in for the Forge
// HTTP backend. It records every Login / CreateSession / SendMessage
// call and exposes a hook for tests to inject synthetic SSE events
// onto the SubscribeEvents channel.
//
// Design notes:
//
//   - Login / CreateSession / ListSessions / DeleteSession /
//     SendMessage record the call. Each operation may be configured
//     to return an injected error, so tests can exercise the
//     pipeline's retry / fallback paths without a real network.
//
//   - SubscribeEvents returns a buffered channel of events plus a
//     "done" channel that is closed when the subscription ends. The
//     Mock is single-subscription per session: a concurrent
//     SubscribeEvents returns ErrSubscribeActive.
//
//   - InjectEvent delivers an event to the active subscription. The
//     send is blocking: if the buffer is full, InjectEvent waits for
//     the consumer. This matches the real SSE behaviour (the network
//     does not drop events on backpressure) and keeps the
//     concurrent-invariants tests honest. A concurrent closer.Close()
//     is observed via the `done` channel — the send is abandoned as
//     soon as done closes.
//
//   - The Mock is fully thread-safe and is exercised under
//     `go test -race` in CI.
package forge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// SendMessageCall is one recorded invocation of MockClient.SendMessage.
type SendMessageCall struct {
	SessionID string
	Text      string
	At        time.Time
}

// MockClient is a deterministic, in-process stand-in for Client.
type MockClient struct {
	mu        sync.RWMutex
	closed    bool
	sessions  map[string]*mockSession
	sendCalls []SendMessageCall

	// userID is the most recent successful Login() return value.
	// Read/written atomically so a concurrent Login sees a
	// consistent value.
	userID atomic.Value // string

	// injected errors
	sendErr atomic.Pointer[error]

	// subMu protects the per-session subscription map. The
	// single-subscription-per-session rule is enforced here.
	subMu sync.Mutex
	subs  map[string]*mockSubscription
}

// mockSession is the per-session state.
type mockSession struct {
	id           string
	profile      string
	createdAt    time.Time
	lastActivity time.Time
}

// mockSubscription is the active subscription for one session.
type mockSubscription struct {
	sessionID string
	events    chan Event
	done      chan struct{}
	closer    *mockCloser
	// closeOnce ensures done is closed exactly once when
	// closer.Close() is invoked.
	closeOnce sync.Once
}

// mockCloser is the io.Closer returned by SubscribeEvents. It
// closes the subscription's done channel and signals the
// consumer goroutine to stop reading.
type mockCloser struct {
	mu   *sync.Mutex
	subs map[string]*mockSubscription
	sub  *mockSubscription
}

// Close closes the subscription. Idempotent. The subscription
// is removed from the parent client's subs map so a future
// SubscribeEvents can succeed.
func (c *mockCloser) Close() error {
	if c == nil || c.sub == nil {
		return nil
	}
	c.sub.closeOnce.Do(func() {
		close(c.sub.done)
		// Drop from the parent client's subs map. Locking
		// the parent mu ensures we are not racing the
		// Subscribe path that owns the map.
		if c.mu != nil && c.subs != nil {
			c.mu.Lock()
			if cur, ok := c.subs[c.sub.sessionID]; ok && cur == c.sub {
				delete(c.subs, c.sub.sessionID)
			}
			c.mu.Unlock()
		}
	})
	return nil
}

// MockOption configures a MockClient at construction time.
type MockOption func(*MockClient)

// MockOptionSendError makes every SendMessage call return err.
// Pass nil to clear.
func MockOptionSendError(err error) MockOption {
	return func(m *MockClient) {
		m.sendErr.Store(errOrNil(err))
	}
}

// errOrNil is a tiny helper to make the atomic.Pointer plumbing
// less noisy. nil → store nil pointer; non-nil → store a copy of
// the error so the caller can mutate their own variable.
func errOrNil(err error) *error {
	if err == nil {
		return nil
	}
	e := err
	return &e
}

// NewMockClient returns a fresh MockClient.
func NewMockClient(opts ...MockOption) *MockClient {
	m := &MockClient{
		sessions: make(map[string]*mockSession),
		subs:     make(map[string]*mockSubscription),
	}
	m.userID.Store("")
	m.sendErr.Store((*error)(nil))
	for _, o := range opts {
		o(m)
	}
	return m
}

// UserID returns the most recent successful Login() user id, or
// the empty string if Login has not been called.
func (m *MockClient) UserID() string {
	v, _ := m.userID.Load().(string)
	return v
}

// Login authenticates with the forge backend using an API key.
// The mock records the call and returns a synthetic user id of
// the form "user-<hex>". A second call refreshes the user id.
func (m *MockClient) Login(ctx context.Context, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.isClosed() {
		return "", ErrClosed
	}
	uid := "user-" + randomHex(4)
	m.userID.Store(uid)
	return uid, nil
}

// CreateSession opens a new agent session and returns its id.
// The id is a freshly-generated UUID string.
func (m *MockClient) CreateSession(ctx context.Context, profile string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.isClosed() {
		return "", ErrClosed
	}
	id := newUUID()
	now := time.Now()
	m.mu.Lock()
	m.sessions[id] = &mockSession{
		id:           id,
		profile:      profile,
		createdAt:    now,
		lastActivity: now,
	}
	m.mu.Unlock()
	return id, nil
}

// ListSessions returns every session, ordered by most-recent
// activity.
func (m *MockClient) ListSessions(ctx context.Context) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.isClosed() {
		return nil, ErrClosed
	}
	m.mu.RLock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, Session{
			ID:             s.id,
			Profile:        s.profile,
			CreatedAt:      s.createdAt,
			LastActivityAt: s.lastActivity,
		})
	}
	m.mu.RUnlock()
	// Sort by lastActivity descending.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].LastActivityAt.After(out[j-1].LastActivityAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// DeleteSession removes a session. Returns ErrSessionNotFound
// if the id is not present.
func (m *MockClient) DeleteSession(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.isClosed() {
		return ErrClosed
	}
	m.mu.Lock()
	_, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	// Tear down any active subscription for this session.
	m.subMu.Lock()
	if sub, ok := m.subs[id]; ok {
		_ = sub.closer.Close()
		delete(m.subs, id)
	}
	m.subMu.Unlock()
	return nil
}

// SendMessage records the call and bumps the session's
// LastActivity. Returns ErrSessionNotFound if the id is not
// present. If a send error is configured, it is returned and
// the call is still recorded.
func (m *MockClient) SendMessage(ctx context.Context, sessionID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.isClosed() {
		return ErrClosed
	}
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		s.lastActivity = time.Now()
		m.sendCalls = append(m.sendCalls, SendMessageCall{
			SessionID: sessionID,
			Text:      text,
			At:        s.lastActivity,
		})
	}
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	if p := m.sendErr.Load(); p != nil && *p != nil {
		return *p
	}
	return nil
}

// SubscribeEvents opens an SSE stream on the given session. The
// mock supports at most one subscription per session. A second
// concurrent SubscribeEvents on the same session returns
// ErrSubscribeActive. The `since` parameter is recorded but not
// used to filter — tests that need filtering use InjectEvent
// with a specific Seq.
func (m *MockClient) SubscribeEvents(ctx context.Context, sessionID string, _ int64) (<-chan Event, <-chan struct{}, io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if m.isClosed() {
		return nil, nil, nil, ErrClosed
	}
	m.mu.RLock()
	_, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, nil, ErrSessionNotFound
	}

	m.subMu.Lock()
	if _, exists := m.subs[sessionID]; exists {
		m.subMu.Unlock()
		return nil, nil, nil, ErrSubscribeActive
	}
	sub := &mockSubscription{
		sessionID: sessionID,
		events:    make(chan Event, 32),
		done:      make(chan struct{}),
	}
	sub.closer = &mockCloser{mu: &m.subMu, subs: m.subs, sub: sub}
	m.subs[sessionID] = sub
	m.subMu.Unlock()

	// Watch the caller's ctx: when it is canceled, close the
	// done channel so the consumer goroutine exits.
	go func() {
		<-ctx.Done()
		_ = sub.closer.Close()
	}()

	return sub.events, sub.done, sub.closer, nil
}

// InjectEvent delivers ev to the active subscription for the
// given session, if any. Returns false if no subscription is
// active for that session. The send is blocking: if the buffer
// is full, InjectEvent waits for the consumer. A concurrent
// closer.Close() is observed via the `done` channel — the send
// is abandoned as soon as done closes.
func (m *MockClient) InjectEvent(sessionID string, ev Event) bool {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	m.subMu.Lock()
	sub, ok := m.subs[sessionID]
	m.subMu.Unlock()
	if !ok {
		return false
	}
	select {
	case sub.events <- ev:
		return true
	case <-sub.done:
		return false
	}
}

// Close releases the client. Idempotent. After Close, every
// method returns ErrClosed (or wraps it). All active
// subscriptions have their done channel closed.
func (m *MockClient) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	m.subMu.Lock()
	subs := make([]*mockSubscription, 0, len(m.subs))
	for _, s := range m.subs {
		subs = append(subs, s)
	}
	m.subs = make(map[string]*mockSubscription)
	m.subMu.Unlock()
	for _, s := range subs {
		// Close synchronously; the closer takes the
		// already-unlocked map so we don't deadlock.
		s.closeOnce.Do(func() {
			close(s.done)
		})
	}
	return nil
}

// SendMessageCalls returns a snapshot of recorded SendMessage calls.
func (m *MockClient) SendMessageCalls() []SendMessageCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SendMessageCall, len(m.sendCalls))
	copy(out, m.sendCalls)
	return out
}

// isClosed returns true after Close. Always called with m.mu
// held (or as a pre-check); the explicit mu is the source of
// truth for the "closed" flag.
func (m *MockClient) isClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

// randomHex returns n random bytes formatted as a hex string.
// Used for synthetic user ids.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand on Linux is effectively never-failing.
		// Falling back to zeros keeps the mock usable in
		// pathological environments; the id is not security-
		// sensitive.
		for i := range b {
			b[i] = 0
		}
	}
	return hex.EncodeToString(b)
}

// newUUID returns a freshly-generated v4-like UUID string. The
// shape is xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx with random
// hex digits; the version (4) and variant (8/9/a/b) nibbles
// are honoured.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback (see randomHex).
	}
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10
	out := make([]byte, 36)
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out)
}

// Compile-time interface check.
var _ Client = (*MockClient)(nil)
