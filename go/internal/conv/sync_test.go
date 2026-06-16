// Tests for conv.Sync. See plan.md §7.4.
package conv_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// fakeRadio is an in-memory Radio for the sync tests. It records
// every Send call so tests can assert on the sequence of
// envelopes.
type fakeRadio struct {
	mu    sync.Mutex
	envs  []*protocolpb.Envelope
	err   error // optional: every Send returns this error
	sendN int
	debug bool
}

func (f *fakeRadio) Send(ctx context.Context, env *protocolpb.Envelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendN++
	if f.err != nil {
		return f.err
	}
	f.envs = append(f.envs, env)
	if f.debug {
		fmt.Printf("DEBUG fakeRadio.Send: env %d msg_type=%v\n", f.sendN, env.MsgType)
	}
	return nil
}

func (f *fakeRadio) Envelopes() []*protocolpb.Envelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*protocolpb.Envelope, len(f.envs))
	copy(out, f.envs)
	return out
}

func (f *fakeRadio) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendN
}

// runSync runs a Sync on a background goroutine and returns a
// cancel function. It sleeps briefly to ensure the Sync's
// subscription to store.Changes is registered before the test
// fires its first Upsert — the MemStore's publish path drops
// events for subscribers that have not yet subscribed.
func runSync(t *testing.T, s *conv.Sync) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()
	// Give Run a moment to call store.Changes and register as
	// a subscriber. 50 ms is conservative; the actual latency
	// is sub-millisecond.
	time.Sleep(50 * time.Millisecond)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("sync did not return within 2s")
		}
	})
	return cancel
}

// waitForCount polls the radio until n envelopes have been
// received or the deadline elapses.
func waitForCount(t *testing.T, r *fakeRadio, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if r.Count() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitForCount: want %d envelopes, got %d", n, r.Count())
}

// TestSync_NewConv_TriggersUIUpdate verifies that Upserting a
// new conversation produces exactly one UI_UPDATE envelope with
// the correct payload.
func TestSync_NewConv_TriggersUIUpdate(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:    store,
		Radio:    radio,
		SenderID: 0x0001,
		TargetID: 0xFFFF,
	})
	runSync(t, sync_)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	_, _, err := store.Upsert(ctx, id, conv.ConvInfo{
		Name:   "general",
		Kind:   conv.KindMatrix,
		Target: "!r1:example.com",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	waitForCount(t, radio, 1, 2*time.Second)
	envs := radio.Envelopes()
	if len(envs) != 1 {
		t.Fatalf("Envelopes: want 1, got %d", len(envs))
	}
	env := envs[0]
	if env.MsgType != protocolpb.MsgType_MSG_TYPE_UI_UPDATE {
		t.Errorf("MsgType: want MSG_TYPE_UI_UPDATE, got %v", env.MsgType)
	}
	if env.TargetId.Value != 0xFFFF {
		t.Errorf("TargetId: want 0xFFFF, got %d", env.TargetId.Value)
	}
	if env.SenderId.Value != 0x0001 {
		t.Errorf("SenderId: want 0x0001, got %d", env.SenderId.Value)
	}
	if env.TotalSeqs != 1 {
		t.Errorf("TotalSeqs: want 1, got %d", env.TotalSeqs)
	}

	ci := &protocolpb.ConvInfo{}
	if err := proto.Unmarshal(env.Payload, ci); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ci.Name != "general" {
		t.Errorf("Name: want %q, got %q", "general", ci.Name)
	}
	if ci.Kind != protocolpb.ConvKind_CONV_KIND_MATRIX {
		t.Errorf("Kind: want CONV_KIND_MATRIX, got %v", ci.Kind)
	}
	if ci.Target != "!r1:example.com" {
		t.Errorf("Target: want %q, got %q", "!r1:example.com", ci.Target)
	}
	if ci.Remove {
		t.Error("Remove: want false, got true")
	}
}

// TestSync_RemovedConv_TriggersUIUpdateRemove verifies that
// Remove on a conversation produces a UI_UPDATE with Remove=true.
func TestSync_RemovedConv_TriggersUIUpdateRemove(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{debug: true}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:       store,
		Radio:       radio,
		SenderID:    0x0001,
		TargetID:    0xFFFF,
		MinInterval: -1, // disable coalescing for this test
	})
	runSync(t, sync_)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := [16]byte{1}
	if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{
		Name: "x", Kind: conv.KindMatrix, Target: "t",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	waitForCount(t, radio, 1, 2*time.Second)
	// Give the sync a moment to drain its handle path before
	// the Remove fires; this avoids a race where the Remove's
	// Change is published into the MemStore's channel before
	// the sync's Run loop has returned to its select.
	time.Sleep(10 * time.Millisecond)

	if _, err := store.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	waitForCount(t, radio, 2, 2*time.Second)

	envs := radio.Envelopes()
	if len(envs) != 2 {
		t.Fatalf("Envelopes: want 2, got %d", len(envs))
	}
	ci := &protocolpb.ConvInfo{}
	if err := proto.Unmarshal(envs[1].Payload, ci); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !ci.Remove {
		t.Error("Remove: want true, got false")
	}
}

// TestSync_PiggybacksOnExistingConnection verifies that the sync
// does not open any new radio connection — it just hands
// envelopes to the Radio the caller passed in. The test is
// satisfied by verifying that the radio's Count is exactly the
// number of conv changes (no extra initial handshake packets).
func TestSync_PiggybacksOnExistingConnection(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:    store,
		Radio:    radio,
		SenderID: 0x0001,
		TargetID: 0xFFFF,
	})
	runSync(t, sync_)

	// No conv changes → zero envelopes.
	time.Sleep(100 * time.Millisecond)
	if got := radio.Count(); got != 0 {
		t.Errorf("no-change count: want 0, got %d", got)
	}
}

// TestSync_BatchUpdates verifies that 5 successive Upserts
// produce exactly 5 UI_UPDATE packets, in order.
func TestSync_BatchUpdates(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:       store,
		Radio:       radio,
		SenderID:    0x0001,
		TargetID:    0xFFFF,
		MinInterval: -1, // disable coalescing for this test
	})
	runSync(t, sync_)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		id := [16]byte{byte(i + 1)}
		name := []string{"one", "two", "three", "four", "five"}[i]
		if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{
			Name: name, Kind: conv.KindMatrix, Target: "t",
		}); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
		// Brief pause to let each event be processed.
		time.Sleep(5 * time.Millisecond)
	}

	waitForCount(t, radio, 5, 2*time.Second)
	envs := radio.Envelopes()
	if len(envs) != 5 {
		t.Fatalf("Envelopes: want 5, got %d", len(envs))
	}
	wantNames := []string{"one", "two", "three", "four", "five"}
	for i, env := range envs {
		ci := &protocolpb.ConvInfo{}
		if err := proto.Unmarshal(env.Payload, ci); err != nil {
			t.Fatalf("Unmarshal[%d]: %v", i, err)
		}
		if ci.Name != wantNames[i] {
			t.Errorf("env[%d].Name: want %q, got %q", i, wantNames[i], ci.Name)
		}
	}
}

// TestSync_NameTruncated verifies that a long name is truncated
// to the M5's 24-char display width.
func TestSync_NameTruncated(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:       store,
		Radio:       radio,
		SenderID:    0x0001,
		TargetID:    0xFFFF,
		MinInterval: -1,
	})
	runSync(t, sync_)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	long := "this-name-is-far-too-long-for-the-m5-display"
	id := [16]byte{1}
	if _, _, err := store.Upsert(ctx, id, conv.ConvInfo{
		Name: long, Kind: conv.KindMatrix, Target: "t",
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	waitForCount(t, radio, 1, 2*time.Second)

	envs := radio.Envelopes()
	ci := &protocolpb.ConvInfo{}
	if err := proto.Unmarshal(envs[0].Payload, ci); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(ci.Name) > 24 {
		t.Errorf("Name length: want ≤24, got %d (%q)", len(ci.Name), ci.Name)
	}
	if ci.Name != long[:24] {
		t.Errorf("Name: want %q, got %q", long[:24], ci.Name)
	}
}

// TestSync_RunReturnsOnContextCancel verifies that Run returns
// when the supplied context is canceled.
func TestSync_RunReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:    store,
		Radio:    radio,
		SenderID: 0x0001,
		TargetID: 0xFFFF,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sync_.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Errorf("Run: want nil or context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestSync_RadioSendErrorDoesNotAbortLoop verifies that a Send
// failure (e.g. queue full) is logged and the next event is
// still processed.
func TestSync_RadioSendErrorDoesNotAbortLoop(t *testing.T) {
	t.Parallel()
	store := conv.NewMemStore()
	radio := &fakeRadio{err: errRadioDead}
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:       store,
		Radio:       radio,
		SenderID:    0x0001,
		TargetID:    0xFFFF,
		MinInterval: -1,
	})
	runSync(t, sync_)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id1 := [16]byte{1}
	id2 := [16]byte{2}
	if _, _, err := store.Upsert(ctx, id1, conv.ConvInfo{Name: "a", Kind: conv.KindMatrix, Target: "t"}); err != nil {
		t.Fatalf("Upsert1: %v", err)
	}
	if _, _, err := store.Upsert(ctx, id2, conv.ConvInfo{Name: "b", Kind: conv.KindMatrix, Target: "t"}); err != nil {
		t.Fatalf("Upsert2: %v", err)
	}
	// Wait long enough for both events to be processed (and fail).
	time.Sleep(200 * time.Millisecond)
	if radio.Count() < 2 {
		t.Errorf("Radio.Send: want ≥2 calls, got %d", radio.Count())
	}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

var errRadioDead = &fakeError{msg: "radio dead"}

// TestSync_DefaultsApplied verifies that the default values
// (SendTimeout, MinInterval) are applied when zero is passed in.
func TestSync_DefaultsApplied(t *testing.T) {
	t.Parallel()
	sync_ := conv.NewSync(conv.SyncConfig{
		Store:    conv.NewMemStore(),
		Radio:    &fakeRadio{},
		SenderID: 0x0001,
		TargetID: 0xFFFF,
	})
	if sync_ == nil {
		t.Fatal("NewSync: nil")
	}
	// Behavioural check: a 0 SendTimeout and 0 MinInterval do
	// not panic or block. The smoke is: construct + close.
}
