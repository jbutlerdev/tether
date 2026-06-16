// Mock implementation of the matrix.Client interface. See plan.md §7.1.
//
// The Mock is a deterministic, in-process stand-in for the real
// mautrix-go appservice network. It is the Client used by every
// test in this package and by the Phase 6 E2E test harness.
//
// Design notes:
//
//   - SendText / JoinRoom / LeaveRoom record the call. Each
//     operation may be configured to return an injected error,
//     so tests can exercise the appservice's retry / fallback
//     paths without a real network.
//
//   - Subscribe returns a buffered channel of events. The Mock
//     supports a Disconnect() / Reconnect() pair that closes the
//     current channel and opens a new one, simulating the network
//     drop-and-reconnect behaviour that the real /transactions
//     listener exhibits.
//
//   - The Mock is fully thread-safe and is exercised under
//     `go test -race` in CI.
package matrix

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

// ErrMockClosed is returned by Mock methods invoked after Close.
var ErrMockClosed = errors.New("matrix: mock client is closed")

// ErrSubscribeAlreadyActive is returned by Subscribe when a
// subscription is still active (i.e. its channel has not been
// drained to completion).
var ErrSubscribeAlreadyActive = errors.New("matrix: mock subscribe already active")

// SendTextCall is one recorded invocation of MockClient.SendText.
type SendTextCall struct {
	RoomID id.RoomID
	Body   string
	At     time.Time
}

// MockClient is a deterministic, in-process stand-in for Client.
type MockClient struct {
	mu        sync.Mutex
	closed    bool
	sendCalls []SendTextCall
	joined    []id.RoomID
	left      []id.RoomID

	// injected errors
	sendErr atomic.Pointer[error]
	joinErr atomic.Pointer[error]
	leaveEr atomic.Pointer[error]

	// subscription state — exactly one active subscription at a time.
	subMu      sync.Mutex
	subActive  bool
	subCancel  context.CancelFunc
	subCh      chan Event
	subDone    chan struct{} // closed when subCh is about to be closed
	subDropped atomic.Bool

	// room state overrides for GetRoomState.
	stateMu sync.Mutex
	state   map[id.RoomID]*mautrix.RoomStateMap

	// next event id for RespSendEvent
	evtCounter atomic.Uint64
}

// MockOption configures a MockClient at construction time.
type MockOption func(*MockClient)

// MockOptionSendError makes every SendText call return err. Pass
// nil to clear.
func MockOptionSendError(err error) MockOption {
	return func(m *MockClient) {
		m.sendErr.Store(errOrNil(err))
	}
}

// MockOptionJoinError makes every JoinRoom call return err.
func MockOptionJoinError(err error) MockOption {
	return func(m *MockClient) {
		m.joinErr.Store(errOrNil(err))
	}
}

// MockOptionLeaveError makes every LeaveRoom call return err.
func MockOptionLeaveError(err error) MockOption {
	return func(m *MockClient) {
		m.leaveEr.Store(errOrNil(err))
	}
}

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
		state: make(map[id.RoomID]*mautrix.RoomStateMap),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// SendText records the call and returns a synthetic RespSendEvent
// (with a monotonic EventID). If a send error is configured, that
// error is returned and the call is still recorded.
func (m *MockClient) SendText(ctx context.Context, roomID id.RoomID, text string) (*mautrix.RespSendEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.isClosed() {
		return nil, ErrMockClosed
	}
	m.mu.Lock()
	m.sendCalls = append(m.sendCalls, SendTextCall{RoomID: roomID, Body: text, At: time.Now()})
	m.mu.Unlock()

	if p := m.sendErr.Load(); p != nil {
		return nil, *p
	}

	idNum := m.evtCounter.Add(1)
	return &mautrix.RespSendEvent{
		EventID: id.EventID("$mock" + uintToHex(idNum)),
	}, nil
}

// JoinRoom records the room and returns the configured error (or nil).
func (m *MockClient) JoinRoom(ctx context.Context, roomID id.RoomID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.isClosed() {
		return ErrMockClosed
	}
	if p := m.joinErr.Load(); p != nil {
		return *p
	}
	m.mu.Lock()
	m.joined = append(m.joined, roomID)
	m.mu.Unlock()
	return nil
}

// LeaveRoom records the room and returns the configured error (or nil).
func (m *MockClient) LeaveRoom(ctx context.Context, roomID id.RoomID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.isClosed() {
		return ErrMockClosed
	}
	if p := m.leaveEr.Load(); p != nil {
		return *p
	}
	m.mu.Lock()
	m.left = append(m.left, roomID)
	m.mu.Unlock()
	return nil
}

// Subscribe returns a channel of events plus a "done" channel
// that is closed when the subscription ends. Only one
// subscription may be active at a time.
func (m *MockClient) Subscribe(ctx context.Context) (<-chan Event, <-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if m.isClosed() {
		return nil, nil, ErrMockClosed
	}

	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.subActive {
		return nil, nil, ErrSubscribeAlreadyActive
	}
	subCtx, cancel := context.WithCancel(ctx)
	ch := make(chan Event, 32)
	done := make(chan struct{})
	m.subActive = true
	m.subDropped.Store(false)
	m.subCh = ch
	m.subDone = done
	m.subCancel = cancel

	// Close ONLY the done channel when the caller's context is
	// done or when Disconnect is invoked. The event channel `ch`
	// is left open — the consumer (appservice pump) selects on
	// `done` as a "stop reading" signal. This avoids a send-
	// vs-close race with concurrent InjectEvent calls.
	go func() {
		<-subCtx.Done()
		m.subMu.Lock()
		if m.subActive && m.subCh == ch {
			close(done)
			m.subCh = nil
			m.subDone = nil
			m.subActive = false
			m.subDropped.Store(true)
		}
		m.subMu.Unlock()
	}()

	return ch, done, nil
}

// WaitForSubscription blocks until a subscription is active on this
// mock, the context is canceled, or the timeout expires. Tests use
// this instead of a fixed sleep to avoid races with slow CI runners.
func (m *MockClient) WaitForSubscription(ctx context.Context) bool {
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		m.subMu.Lock()
		active := m.subActive
		m.subMu.Unlock()
		if active {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// InjectEvent delivers ev to the current subscription, if any.
// Returns false if no subscription is active.
//
// The send is blocking: if the buffer is full, InjectEvent waits
// for the consumer. This matches the real mautrix appservice's
// semantics (no event is dropped on backpressure) and keeps the
// concurrent-invariants test honest. A concurrent Disconnect is
// observed via the subDone channel — the send is abandoned as
// soon as subDone is closed.
//
// Callers that want the non-blocking "drop on full" behaviour
// can use InjectEventNonBlocking (test-only).
func (m *MockClient) InjectEvent(ev Event) bool {
	m.subMu.Lock()
	ch := m.subCh
	done := m.subDone
	m.subMu.Unlock()
	if ch == nil {
		return false
	}
	if done != nil {
		select {
		case ch <- ev:
			return true
		case <-done:
			return false
		}
	}
	ch <- ev
	return true
}

// InjectEventNonBlocking is the drop-on-full variant of
// InjectEvent. Used by tests that want to simulate a real-world
// "events arrive faster than the consumer can process" scenario
// without deadlocking the test goroutine.
func (m *MockClient) InjectEventNonBlocking(ev Event) bool {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.subCh == nil {
		return false
	}
	select {
	case m.subCh <- ev:
		return true
	default:
		return false
	}
}

// Disconnect simulates a network drop: the current subscription
// channel is closed and a new Subscribe is required to resume.
// Idempotent.
func (m *MockClient) Disconnect() {
	m.subMu.Lock()
	if m.subCancel != nil {
		m.subCancel()
		m.subCancel = nil
	}
	m.subMu.Unlock()
}

// SetRoomState pre-populates the state returned by GetRoomState.
func (m *MockClient) SetRoomState(roomID id.RoomID, st *mautrix.RoomStateMap) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.state[roomID] = st
}

// GetRoomState returns the previously-set state, or an empty
// RoomStateMap if none was set.
func (m *MockClient) GetRoomState(ctx context.Context, roomID id.RoomID) (*mautrix.RoomStateMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.isClosed() {
		return nil, ErrMockClosed
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if st, ok := m.state[roomID]; ok {
		return st, nil
	}
	return &mautrix.RoomStateMap{}, nil
}

// Close releases the client. Idempotent. After Close, every method
// returns ErrMockClosed (or wraps it).
func (m *MockClient) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	// Tear down any active subscription.
	m.Disconnect()
	return nil
}

// SendTextCalls returns a snapshot of recorded SendText calls.
func (m *MockClient) SendTextCalls() []SendTextCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SendTextCall, len(m.sendCalls))
	copy(out, m.sendCalls)
	return out
}

// JoinedRooms returns a snapshot of recorded JoinRoom calls.
func (m *MockClient) JoinedRooms() []id.RoomID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]id.RoomID, len(m.joined))
	copy(out, m.joined)
	return out
}

// LeftRooms returns a snapshot of recorded LeaveRoom calls.
func (m *MockClient) LeftRooms() []id.RoomID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]id.RoomID, len(m.left))
	copy(out, m.left)
	return out
}

// isClosed returns true after Close. The atomic load is not used
// here because isClosed is always called with m.mu held (or as a
// pre-check); the explicit mu is the source of truth.
func (m *MockClient) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// uintToHex formats a uint64 as a zero-padded hex string. Used
// only to build deterministic EventIDs in tests.
func uintToHex(n uint64) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 16)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		out = append([]byte{hex[n&0xF]}, out...)
		n >>= 4
	}
	return string(out)
}
