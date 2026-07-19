//go:build forge

// http.go — real forge HTTP + SSE client (net/http).
//
// Build with `-tags forge` to link this into the daemon. The default
// build (no tag) uses the MockClient (mock_client.go); this file
// provides the production client that talks to a real forge backend
// (../forge, Rust + PostgreSQL + pi-mono).
//
// The forge API (see crates/forge-api/src/api/mod.rs) uses:
//
//	POST   /sessions            {"profile_id": "<uuid>"}  → 201 {"session": {...}}
//	GET    /sessions                                        → 200 [...]
//	DELETE /sessions/:id                                     → 204
//	POST   /messages           {"session_id", "content"}   → 200 {"message": {...}}
//	GET    /sessions/:id/events?since=<seq>                → SSE stream
//
// Auth: the `X-API-Key: <key>` header on every request. The key is
// created out-of-band (POST /api-keys in the forge web UI or CLI).
// The `Login` method validates the key with a lightweight GET
// /sessions and stores it for subsequent calls.
package forge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTPClient is the production forge client (net/http). It implements
// the Client interface. Construct with NewHTTPClient.
type HTTPClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	userID     string

	mu     sync.Mutex
	closed bool
}

// NewHTTPClient creates a real forge HTTP client. `baseURL` is the
// forge API root (e.g. "http://localhost:8080"). The API key is set
// by Login.
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// Don't follow redirects — forge doesn't use them.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Login validates the API key against the forge backend (a GET
// /sessions call) and stores it for subsequent requests. Returns a
// synthetic user_id (the forge API doesn't return one from a
// key-only auth; the key itself is the identity).
func (c *HTTPClient) Login(ctx context.Context, apiKey string) (string, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", ErrClosed
	}
	c.apiKey = apiKey
	c.mu.Unlock()

	// Validate by listing sessions (any 2xx means the key works).
	req, err := c.newRequest(ctx, http.MethodGet, "/sessions", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("forge: login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", errors.New("forge: invalid API key")
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("forge: login: status %d: %s", resp.StatusCode, body)
	}

	// The user_id is synthetic — forge identifies users by API key.
	c.mu.Lock()
	c.userID = "key-" + apiKey[:min(8, len(apiKey))]
	uid := c.userID
	c.mu.Unlock()
	return uid, nil
}

// CreateSession opens a new agent session. `profile` is a profile
// UUID string.
func (c *HTTPClient) CreateSession(ctx context.Context, profile string) (string, error) {
	body := map[string]string{"profile_id": profile}
	resp, err := c.do(ctx, http.MethodPost, "/sessions", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("forge: create session: status %d: %s", resp.StatusCode, b)
	}
	var result struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("forge: create session: decode: %w", err)
	}
	if result.Session.ID == "" {
		return "", errors.New("forge: create session: empty session id")
	}
	return result.Session.ID, nil
}

// ListSessions returns all sessions for the authenticated user.
func (c *HTTPClient) ListSessions(ctx context.Context) ([]Session, error) {
	resp, err := c.do(ctx, http.MethodGet, "/sessions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("forge: list sessions: status %d: %s", resp.StatusCode, b)
	}
	// The forge API may return a bare array or a wrapper object.
	// Try array first, then {"sessions": [...]}.
	var sessions []Session
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &sessions); err != nil {
		var wrapper struct {
			Sessions []Session `json:"sessions"`
		}
		if err2 := json.Unmarshal(raw, &wrapper); err2 != nil {
			return nil, fmt.Errorf("forge: list sessions: decode: %w", err)
		}
		sessions = wrapper.Sessions
	}
	return sessions, nil
}

// DeleteSession terminates a session. Idempotent.
func (c *HTTPClient) DeleteSession(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil // idempotent
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forge: delete session: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// SendMessage posts a user message to the given session.
func (c *HTTPClient) SendMessage(ctx context.Context, sessionID, text string) error {
	body := map[string]string{"session_id": sessionID, "content": text}
	resp, err := c.do(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forge: send message: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// SubscribeEvents opens an SSE stream on the session. The returned
// event channel receives Event values; the done channel is closed
// when the stream ends (network drop, ctx cancel, or Close). The
// closer releases the HTTP body.
func (c *HTTPClient) SubscribeEvents(ctx context.Context, sessionID string, since int64) (<-chan Event, <-chan struct{}, io.Closer, error) {
	path := fmt.Sprintf("/sessions/%s/events?since=%d", url.PathEscape(sessionID), since)
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	// Use a separate client with no timeout — SSE is long-lived.
	sseClient := &http.Client{}
	resp, err := sseClient.Do(req)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("forge: subscribe: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, nil, nil, fmt.Errorf("forge: subscribe: status %d: %s", resp.StatusCode, b)
	}

	events := make(chan Event, 64)
	done := make(chan struct{})
	body := resp.Body

	go func() {
		defer close(done)
		defer close(events)
		defer body.Close()

		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var ev Event
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				ev.Type = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				ev.Content = strings.TrimPrefix(line, "data: ")
				ev.Seq = parseSeq(ev.Content)
			} else if line == "" && ev.Type != "" {
				ev.At = time.Now()
				select {
				case events <- ev:
				case <-ctx.Done():
					return
				}
				ev = Event{}
			}
			// Parse seq from data if present (the forge SSE includes
			// a `seq` field in the JSON payload).
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			// Stream ended with an error; the done channel signals stop.
		}
	}()

	return events, done, body, nil
}

// Close releases resources. Idempotent.
func (c *HTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// ── helpers ──────────────────────────────────────────────────────

func (c *HTTPClient) newRequest(ctx context.Context, method, path string, body interface{}) (*http.Request, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	url := c.baseURL + path
	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("forge: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	c.mu.Unlock()
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("forge: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Compile-time check.
var _ Client = (*HTTPClient)(nil)

// parseSeq extracts the seq field from an SSE data payload, if present.
func parseSeq(data string) int64 {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return 0
	}
	if raw, ok := m["seq"]; ok {
		var n int64
		if json.Unmarshal(raw, &n) == nil {
			return n
		}
		// seq might be a string.
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}
