// Store interface and helpers. See plan.md §0.5 / §7.3.
//
// The conv.Store is the seam between the data plane (mappers,
// sync) and the storage backend. The production daemon will
// implement a LittleFS-backed store (lfs_store.go, future); the
// default CI / test build uses mem_store.go.
package conv

import "context"

// ChangeKind is how a Store mutation is classified for downstream
// subscribers (see Store.Changes).
type ChangeKind int

const (
	// ChangeUpsert is fired by Upsert. The boolean is true if
	// the conversation is new (did not exist before the call),
	// false if it was updated in place.
	ChangeUpsert ChangeKind = iota
	// ChangeRemove is fired by Remove.
	ChangeRemove
)

// String returns "upsert" or "remove".
func (c ChangeKind) String() string {
	switch c {
	case ChangeUpsert:
		return "upsert"
	case ChangeRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Change is one mutation event emitted on Store.Changes.
type Change struct {
	// Kind is the mutation class.
	Kind ChangeKind
	// ID is the 16-byte conversation ID affected.
	ID [16]byte
	// New is the post-Upsert Conversation. Zero on Remove.
	New Conversation
	// Created is true on Upsert if the conversation did not exist
	// before the call. Always false on Remove.
	Created bool
}

// Store is the abstract conversation store. Implementations:
// MemStore (in-process; default CI) and LfsStore (LittleFS;
// production).
//
// All methods are safe for concurrent use. The store is also
// expected to be cheap to call from the request hot path; the
// LittleFS backend uses a single-writer mutex and an in-memory
// index for reads.
type Store interface {
	// Upsert inserts or updates a conversation. Returns the
	// post-write Conversation and a boolean "new" indicating
	// whether the conversation existed before this call.
	//
	// Errors:
	//   - ErrInvalidKind if info.Kind is KindUnspecified
	//   - ErrInvalidTarget if info.Target is empty
	Upsert(ctx context.Context, id [16]byte, info ConvInfo) (Conversation, bool, error)

	// Get returns the conversation with the given ID, or
	// ErrNotFound.
	Get(ctx context.Context, id [16]byte) (Conversation, error)

	// Remove deletes the conversation with the given ID, or
	// returns ErrNotFound if it does not exist. Removing a
	// non-existent ID is *not* an error; the post-condition is
	// "the ID does not exist". The boolean return distinguishes
	// the two cases for callers that care.
	Remove(ctx context.Context, id [16]byte) (existed bool, err error)

	// List returns a snapshot of all conversations, ordered by
	// last-activity (most recent first). Stable for the
	// duration of the call; subsequent mutations are not
	// reflected in the returned slice.
	List(ctx context.Context) ([]Conversation, error)

	// Changes returns a channel of mutation events. The channel
	// is closed when ctx is canceled. Subscribers can be added
	// at any time and receive only events that occur after the
	// subscription.
	//
	// Each Upsert / Remove produces exactly one event. The
	// channel is buffered; if a subscriber falls behind, the
	// store drops events (the subscriber will see a "gap" in
	// the sequence and is expected to re-read via List).
	Changes(ctx context.Context) <-chan Change
}
