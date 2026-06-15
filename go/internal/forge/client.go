// Package forge is Tether's Forge agent-session integration. See plan.md §8.
//
// The package is split across several commits:
//
//  1. Client interface + Mock (this file + mock_client.go).
//     The Mock is the in-process test double used by every
//     unit test and the CI pipeline. It is the only Client
//     implementation compiled into CI by default.
//
//  2. SSE consumer (sse.go) — a self-contained parser of the
//     SSE wire format that turns the body's `event:` / `data:`
//     pairs into forge.Event values. Tested in CI without any
//     network involvement.
//
//  3. session_to_conv.go (plan §8.3) — deterministic mapping
//     from a Forge session UUID to the 16-byte conversation_id
//     the M5 uses as its lookup key.
//
//  4. pipeline.go (plan §8.4) — the voice ↔ forge glue:
//     incoming audio → STT → POST /messages → SSE → TTS →
//     radio. All external dependencies are interface-typed so
//     the pipeline is testable end-to-end with mocks.
//
//  5. Real HTTP client (http.go, build tag `forge`) — wraps
//     net/http for production. Not compiled into CI; integration
//     tests under the same build tag are skipped on a stock
//     `go test ./...` run.
//
//  6. tether-forge CLI (go/cmd/tether-forge/) — the operator
//     front-end for managing forge sessions.
package forge

import (
	"context"
	"errors"
	"io"
	"time"
)

// Sentinel errors returned by Client methods and the pipeline.
var (
	// ErrClosed is returned by methods invoked after the client
	// has been Close()d.
	ErrClosed = errors.New("forge: client is closed")
	// ErrSessionNotFound is returned by ListSessions / DeleteSession
	// when the requested id is not present.
	ErrSessionNotFound = errors.New("forge: session not found")
	// ErrSubscribeActive is returned by SubscribeEvents when a
	// subscription is already active on the same client.
	ErrSubscribeActive = errors.New("forge: subscribe already active")
)

// Event is a single Forge event delivered to a SubscribeEvents
// consumer. The Type field is one of the constants below; Content
// is the raw JSON body. Seq is the per-session monotonic sequence
// number; it is the value the consumer passes back as the `since`
// parameter on reconnect.
type Event struct {
	// Type is the SSE event name (e.g. "text_delta",
	// "tool_call_start", "tool_call_end", "agent_end", "error").
	Type string
	// Content is the JSON payload as a string. The receiver
	// parses it into a Type-specific struct.
	Content string
	// Seq is the per-session sequence number. Monotonic per
	// session; the SSE consumer hands it through verbatim.
	Seq int64
	// At is the wall-clock time the consumer first saw the
	// event. Populated by Mock.InjectEvent; for the real
	// client it is the local time at the moment the parser
	// finished a frame.
	At time.Time
}

// Event-type constants. SSE `event:` field is a single token; the
// Forge agent emits one of these. New types are additive: the
// pipeline treats anything it does not recognise as no-op.
const (
	// EventTextDelta is a partial agent text reply.
	EventTextDelta = "text_delta"
	// EventToolCallStart fires when the agent invokes a tool.
	EventToolCallStart = "tool_call_start"
	// EventToolCallEnd fires when the tool returns.
	EventToolCallEnd = "tool_call_end"
	// EventToolStdout is one line of a streaming tool's stdout
	// (currently only emitted by the bash tool). Each line is
	// its own event.
	EventToolStdout = "tool_stdout"
	// EventAgentEnd fires when the agent turn terminates. After
	// this event no further events for the turn are sent.
	EventAgentEnd = "agent_end"
	// EventError is an unrecoverable agent error; the consumer
	// should announce it to the user (TTS) and the session may
	// need to be resumed.
	EventError = "error"
	// EventSessionExpired is fired by the SSE consumer when the
	// forge backend signals that the session's TTL has elapsed
	// (HTTP 410 from the resume endpoint). The pipeline uses
	// it as a trigger for a transparent resume.
	EventSessionExpired = "session_expired"
)

// Session describes a single Forge agent session. The CLI uses
// it to render the `tether forge list` output.
type Session struct {
	// ID is the canonical session UUID; the path component of
	// the SSE URL and the conversation_id suffix.
	ID string
	// Profile is the agent profile (e.g. "coder", "researcher").
	Profile string
	// CreatedAt is the wall-clock creation time, UTC.
	CreatedAt time.Time
	// LastActivityAt is the most recent agent_end time, UTC.
	LastActivityAt time.Time
}

// Client is the abstract Forge client. Implementations: Mock
// (in-process test double, mock_client.go) and HTTPClient
// (real net/http, http.go, build tag `forge`).
//
// All methods take a context.Context for cancellation. The HTTP
// implementation maps ctx cancel to a request abort; the Mock
// does not perform I/O so cancellation is observed only on the
// SubscribeEvents channel.
//
// SubscribeEvents MUST NOT be called more than once concurrently
// per Client. The Mock returns ErrSubscribeActive; the HTTP
// client simply holds the previous subscription open and
// callers should track subscriptions themselves.
type Client interface {
	// Login authenticates with the forge backend using an API
	// key. Returns the user_id; subsequent calls refresh the
	// internal token.
	Login(ctx context.Context, apiKey string) (userID string, err error)

	// CreateSession opens a new agent session with the given
	// profile. Returns the session id (a UUID string).
	CreateSession(ctx context.Context, profile string) (sessionID string, err error)

	// ListSessions returns every session the authenticated
	// user owns, ordered by most-recent activity.
	ListSessions(ctx context.Context) ([]Session, error)

	// DeleteSession terminates a session. Idempotent: deleting
	// an unknown id returns ErrSessionNotFound.
	DeleteSession(ctx context.Context, id string) error

	// SendMessage posts a user message to the given session.
	// The forge backend returns 202 Accepted on success; the
	// reply arrives on the SSE stream.
	SendMessage(ctx context.Context, sessionID, text string) error

	// SubscribeEvents opens an SSE stream on the session,
	// starting at `since` (use 0 to start from the latest
	// event). Returns a channel of events plus a "done" channel
	// that is closed when the subscription ends (network drop,
	// ctx cancel, or Close).
	//
	// The event channel is NOT closed when the subscription
	// ends; the done channel is the canonical "stop reading"
	// signal. This matches the matrix.Mock convention and
	// avoids a send-vs-close race with concurrent
	// InjectEvent/InjectError calls.
	//
	// The `closer` returned alongside the channels releases the
	// underlying HTTP body and is a no-op for the Mock. Callers
	// should defer closer.Close() in the consumer goroutine.
	SubscribeEvents(ctx context.Context, sessionID string, since int64) (<-chan Event, <-chan struct{}, io.Closer, error)

	// Close releases any resources held by the client.
	// Idempotent. After Close, every method returns ErrClosed.
	Close() error
}
