// Tests for the tether-matrix-test CLI. See plan.md §7.5.
//
// The CLI is just a thin wrapper around the in-process
// pipeline; the actual test logic lives in runE2E so the same
// scenarios are exercised by go test (this file) and by the
// built CLI binary.
package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maunium.net/go/mautrix/id"

	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/matrix"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// testHarness wires the in-process pipeline and returns a
// handle for assertions.
type testHarness struct {
	store  conv.Store
	mc     *matrix.MockClient
	as     *matrix.Appservice
	mapper *matrix.Mapper
	stt    *stt.Mock
	tts    *tts.Mock
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mc := matrix.NewMockClient()
	store := conv.NewMemStore()
	mapper := matrix.NewMapper(store, userID("@tether:example.com"))

	ctx, cancel := context.WithCancel(context.Background())
	as := matrix.NewAppservice(matrix.AppserviceConfig{
		Client:    mc,
		UserID:    userID("@tether:example.com"),
		Localpart: "tether",
		Logger:    logger,
		OnMessage: func(ctx context.Context, ev matrix.Event) {
			mapper.OnRoomMessage(ctx, ev)
		},
		OnRemove: func(ctx context.Context, ev matrix.Event) {
			mapper.OnRoomRemoved(ctx, ev)
		},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = as.Run(ctx)
	}()
	// Let the appservice subscribe.
	time.Sleep(20 * time.Millisecond)

	t.Cleanup(func() {
		cancel()
		<-done
		_ = mc.Close()
	})
	return &testHarness{
		store:  store,
		mc:     mc,
		as:     as,
		mapper: mapper,
		stt:    stt.NewMock(),
		tts:    tts.NewMock(),
		cancel: cancel,
		done:   done,
	}
}

// waitForConvTarget polls until a conversation with the given
// target (room id) appears in the store.
func waitForConvTarget(t *testing.T, h *testHarness, rid id.RoomID, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		list, err := h.store.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, c := range list {
			if c.Info.Target == rid.String() {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitForConvTarget: timeout waiting for %q", rid.String())
}

// TestMatrixE2E_VoiceToRoom simulates "M5 voice → STT → Matrix
// room" and verifies a conversation is created.
func TestMatrixE2E_VoiceToRoom(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	transcript, err := h.stt.Transcribe(ctx, []float32{0.1, 0.2, 0.3}, 8000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	rid := id.RoomID("!e2e1:example.com")
	if !h.mc.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   transcript,
		Time:   time.Now(),
	}) {
		t.Fatal("InjectEvent: no subscription")
	}

	waitForConvTarget(t, h, rid, 2*time.Second)
}

// TestMatrixE2E_RoomToVoice simulates "Element sends a message
// → M5 would receive TTS". We synthesise the message and write
// the resulting PCM to a temp WAV file; if Synthesize returns
// audio and the WAV write succeeds, the path is exercised.
func TestMatrixE2E_RoomToVoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rid := id.RoomID("!e2e2:example.com")
	if !h.mc.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@bob:example.com"),
		Room:   rid,
		Body:   "tts this",
		Time:   time.Now(),
	}) {
		t.Fatal("InjectEvent: no subscription")
	}
	waitForConvTarget(t, h, rid, 2*time.Second)

	pcm, sr, err := h.tts.Synthesize(context.Background(), "tts this")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) == 0 {
		t.Fatal("Synthesize: empty PCM")
	}
	if sr == 0 {
		t.Fatal("Synthesize: zero sample rate")
	}

	tmp := filepath.Join(t.TempDir(), "reply.wav")
	if err := writeWAV(tmp, pcm, sr); err != nil {
		t.Fatalf("writeWAV: %v", err)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() < 44 {
		t.Errorf("WAV size: want > 44 (header), got %d", info.Size())
	}
}

// TestMatrixE2E_NewRoomAutoConv verifies that an m.room.member
// invite for a new room, followed by a message in that room,
// produces a conversation in the store.
func TestMatrixE2E_NewRoomAutoConv(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	rid := id.RoomID("!e2e3:example.com")
	// Invite.
	if !h.mc.InjectEvent(matrix.Event{
		Type:   "m.room.member",
		Sender: userID("@alice:example.com"),
		Room:   rid,
		Body:   "invite",
		Time:   time.Now(),
	}) {
		t.Fatal("InjectEvent invite: no subscription")
	}
	// The auto-join should have happened.
	time.Sleep(20 * time.Millisecond)
	if got := h.mc.JoinedRooms(); len(got) != 1 || got[0] != rid {
		t.Errorf("auto-join: want [%q], got %v", rid, got)
	}

	// First message in the new room.
	if !h.mc.InjectEvent(matrix.Event{
		Type:   "m.room.message",
		Sender: userID("@bob:example.com"),
		Room:   rid,
		Body:   "hi",
		Time:   time.Now(),
	}) {
		t.Fatal("InjectEvent message: no subscription")
	}
	waitForConvTarget(t, h, rid, 2*time.Second)
}

// TestRun_EndToEnd exercises the full run() function used by
// the CLI binary. This is the same code path that the binary
// uses, so any regression in the wiring shows up here too.
func TestRun_EndToEnd(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := run(outDir, logger); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Verify the TTS WAV was written.
	matches, err := filepath.Glob(filepath.Join(outDir, "*.wav"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("WAV output: want 1 file in %s, got %d", outDir, len(matches))
	}
}
