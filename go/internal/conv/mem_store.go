// In-memory conversation store. The default CI / test backend.
// See plan.md §0.5.
package conv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MemStore is an in-process, thread-safe Store. It is the test
// backend for every conv-using test in the project; the
// production daemon replaces it with a LittleFS-backed
// implementation (lfs_store.go, future).
type MemStore struct {
	mu   sync.RWMutex
	rows map[[16]byte]Conversation

	// subMu protects subscribers (additions, removals) and
	// ensures events are delivered in causal order.
	subMu      sync.Mutex
	subs       []chan Change
	bufferSize int

	// freshKeyFn, if non-nil, is invoked on Upsert to materialise
	// ConvInfo.EncryptionKey when the caller leaves it nil. The
	// result is validated (must be exactly 16 bytes) before being
	// committed to the row. See plan §9.1 — the per-conversation
	// AES-128 link key is HKDF-derived from the master PSK with
	// the convID as the salt.
	freshKeyFn FreshKeyFn
}

// FreshKeyFn derives a 16-byte AES-128 key for a new
// conversation. The function is supplied at MemStore
// construction time; production code wires
// internal/crypto.ConvKey(master, convID) here, tests can plug
// in a deterministic stub.
type FreshKeyFn func(convID [16]byte) (key []byte, err error)

// MemStoreOption configures a MemStore.
type MemStoreOption func(*MemStore)

// MemStoreOptionBufferSize sets the per-subscriber channel
// buffer. Defaults to 32.
func MemStoreOptionBufferSize(n int) MemStoreOption {
	return func(s *MemStore) {
		if n > 0 {
			s.bufferSize = n
		}
	}
}

// MemStoreOptionDeriveKey installs a FreshKeyFn. When set, every
// Upsert that does not supply an explicit ConvInfo.EncryptionKey
// has one derived via fn(id) and the result stored. This is the
// seam the production daemon uses to wire HKDF into the
// conversation store; tests use it to inject deterministic
// keys. See plan §9.1.
func MemStoreOptionDeriveKey(fn FreshKeyFn) MemStoreOption {
	return func(s *MemStore) {
		s.freshKeyFn = fn
	}
}

// NewMemStore returns a fresh MemStore.
func NewMemStore(opts ...MemStoreOption) *MemStore {
	s := &MemStore{
		rows:       make(map[[16]byte]Conversation),
		bufferSize: 32,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ErrInvalidEncryptionKey is returned by Upsert when a fresh key
// is derived (or supplied) with a length other than 16 bytes. The
// SX1262 hardware engine takes exactly 16 bytes; storing a
// shorter or longer key would silently corrupt the link.
var ErrInvalidEncryptionKey = errors.New("conv: encryption key must be 16 bytes")

// Upsert inserts or updates a conversation.
func (s *MemStore) Upsert(ctx context.Context, id [16]byte, info ConvInfo) (Conversation, bool, error) {
	if err := ctx.Err(); err != nil {
		return Conversation{}, false, err
	}
	if info.Kind == KindUnspecified {
		return Conversation{}, false, ErrInvalidKind
	}
	if info.Target == "" {
		return Conversation{}, false, ErrInvalidTarget
	}

	// Derive the per-conversation AES-128 key if the caller
	// didn't supply one. This is the seam from plan §9.1: the
	// production daemon installs a FreshKeyFn that wraps
	// internal/crypto.ConvKey, so every row that lands in the
	// store has a valid 16-byte key.
	if len(info.EncryptionKey) == 0 && s.freshKeyFn != nil {
		key, err := s.freshKeyFn(id)
		if err != nil {
			return Conversation{}, false, fmt.Errorf("derive key for %x: %w", id, err)
		}
		if len(key) != 16 {
			return Conversation{}, false, fmt.Errorf("len=%d: %w", len(key), ErrInvalidEncryptionKey)
		}
		// Defensive copy — the caller may reuse the slice.
		info.EncryptionKey = append([]byte(nil), key...)
	}
	// If the caller *did* supply a key, validate its length so a
	// bad provisioning step fails loudly here rather than on the
	// air.
	if len(info.EncryptionKey) > 0 && len(info.EncryptionKey) != 16 {
		return Conversation{}, false, fmt.Errorf("len=%d: %w", len(info.EncryptionKey), ErrInvalidEncryptionKey)
	}

	s.mu.Lock()
	_, existed := s.rows[id]
	conv := Conversation{ID: id, Info: info}
	s.rows[id] = conv
	s.mu.Unlock()

	s.publish(Change{Kind: ChangeUpsert, ID: id, New: conv, New_: !existed})
	return conv, !existed, nil
}

// Get returns the conversation with the given ID, or
// ErrNotFound.
func (s *MemStore) Get(ctx context.Context, id [16]byte) (Conversation, error) {
	if err := ctx.Err(); err != nil {
		return Conversation{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.rows[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	return c, nil
}

// Remove deletes a conversation. Returns existed=true if the ID
// was present (and removed); existed=false if it was already
// absent.
func (s *MemStore) Remove(ctx context.Context, id [16]byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.Lock()
	_, existed := s.rows[id]
	delete(s.rows, id)
	s.mu.Unlock()

	s.publish(Change{Kind: ChangeRemove, ID: id})
	return existed, nil
}

// List returns a snapshot of all conversations, ordered by
// last-activity (most recent first).
func (s *MemStore) List(ctx context.Context) ([]Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	out := make([]Conversation, 0, len(s.rows))
	for _, c := range s.rows {
		out = append(out, c)
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Info.LastActivityUnixMs > out[j].Info.LastActivityUnixMs
	})
	return out, nil
}

// Changes returns a subscription channel. The channel is closed
// when ctx is canceled. Callers should drain it in a dedicated
// goroutine.
func (s *MemStore) Changes(ctx context.Context) <-chan Change {
	ch := make(chan Change, s.bufferSize)

	s.subMu.Lock()
	s.subs = append(s.subs, ch)
	s.subMu.Unlock()

	go func() {
		<-ctx.Done()
		s.subMu.Lock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				break
			}
		}
		s.subMu.Unlock()
		close(ch)
	}()

	return ch
}

// publish delivers ev to all current subscribers, dropping events
// for subscribers whose channel buffer is full. This matches the
// Store contract: "if a subscriber falls behind, the store drops
// events" (see store.go).
func (s *MemStore) publish(ev Change) {
	s.subMu.Lock()
	subs := make([]chan Change, len(s.subs))
	copy(subs, s.subs)
	s.subMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Drop. The subscriber's recovery path is a
			// re-read via List.
		}
	}
}
