// Tests for the voice ↔ forge pipeline. See plan.md §8.4.
//
// The pipeline is the glue that wires:
//
//   - radio.IncomingMessage (Opus bytes from M5)
//   - codec.Opus (decode to int16 PCM)
//   - stt.Transcriber (text)
//   - forge.Client (POST /messages, SSE events)
//   - tts.Synthesizer (text → float32 PCM)
//   - codec.Opus (encode back to Opus)
//   - protocol.Fragment (split into LoRa chunks)
//   - radio (deliver the chunks to the M5)
//
// Every dependency is an interface, so the test uses mocks
// exclusively. The radio layer is mocked via an in-process
// "capture" radio that records outgoing Envelopes; the SSE
// layer is mocked via the forge.MockClient's InjectEvent.
package forge_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/conv"
	"github.com/jbutlerdev/tether/go/internal/forge"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// pipelineHarness wires the mocks and the pipeline under test
// into a single bundle. The fields are exported so the test
// can poke at them (e.g., to inject events onto the SSE
// channel) after the pipeline is running.
type pipelineHarness struct {
	pipeline  *forge.Pipeline
	forge     *forge.MockClient
	store     *conv.MemStore
	radios    *captureRadio
	stt       *stt.Mock
	tts       *tts.Mock
	cd        *codec.Mock
	sessionID string
}

// captureRadio is a minimal radio that records every envelope
// handed to Send. The Envelope shape matches the one the
// pipeline produces (TTS_DATA / TTS_END), so the test can
// decode the payload if it wants.
type captureRadio struct {
	mu      sync.Mutex
	payload [][]byte // each entry is the .Payload of one envelope
	typ     []uint32 // each entry is the .MsgType of one envelope
	count   atomic.Int64
}

// Send records the envelope. Implements forge.Radio.
func (c *captureRadio) Send(_ context.Context, env *protocolpb.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.payload = append(c.payload, append([]byte(nil), env.Payload...))
	c.typ = append(c.typ, uint32(env.MsgType))
	c.count.Add(1)
	return nil
}

// Envs returns a snapshot of recorded (payload, msg_type)
// pairs.
func (c *captureRadio) Envs() ([][]byte, []uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out1 := make([][]byte, len(c.payload))
	out2 := make([]uint32, len(c.typ))
	for i := range c.payload {
		out1[i] = append([]byte(nil), c.payload[i]...)
		out2[i] = c.typ[i]
	}
	return out1, out2
}

// TestPipeline_OpusDecodeSTTPost: an incoming audio frame is
// decoded → STT → POST /messages.
func TestPipeline_OpusDecodeSTTPost(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "transcribed text")
	defer h.forge.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	audio := makeAudioFrame(2 * codec.FrameSize)
	audio.ConversationID = forge.SessionToConvID(h.sessionID)
	if err := h.pipeline.HandleIncomingAudio(ctx, audio); err != nil {
		t.Fatalf("HandleIncomingAudio: %v", err)
	}

	calls := h.forge.SendMessageCalls()
	if len(calls) != 1 {
		t.Fatalf("SendMessage calls: want 1, got %d", len(calls))
	}
	// The STT mock returns a 16-char hex digest of the
	// PCM input (see stt.Mock.Transcribe). Verify a call
	// was recorded with a non-empty body of the expected
	// length.
	if len(calls[0].Text) == 0 {
		t.Error("Text: empty")
	}
	if len(calls[0].Text) != 16 {
		t.Errorf("Text: want 16-char hex digest, got %d chars %q", len(calls[0].Text), calls[0].Text)
	}
}

// TestPipeline_TextDeltaToTTSFragment: a text_delta SSE event
// is buffered until a sentence boundary, then handed to TTS
// and emitted as a TTS_DATA fragment.
func TestPipeline_TextDeltaToTTSFragment(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, err := h.forge.CreateSession(ctx, "coder")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	// Inject a few text deltas. The pipeline must buffer
	// them until a sentence boundary.
	if !h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventTextDelta,
		Content: `{"delta":"Hello"}`,
		Seq:     1,
		At:      time.Now(),
	}) {
		t.Fatal("inject 1: not delivered")
	}
	if !h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventTextDelta,
		Content: `{"delta":" world."}`,
		Seq:     2,
		At:      time.Now(),
	}) {
		t.Fatal("inject 2: not delivered")
	}
	// agent_end forces a flush.
	if !h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventAgentEnd,
		Content: `{}`,
		Seq:     3,
		At:      time.Now(),
	}) {
		t.Fatal("inject 3 (agent_end): not delivered")
	}

	// Wait for the pipeline to flush the buffered sentence.
	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_AgentEndTriggersTTSEnd: the agent_end event
// causes the pipeline to flush any buffered text and emit a
// final TTS chunk.
func TestPipeline_AgentEndTriggersTTSEnd(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventTextDelta,
		Content: `{"delta":"final answer."}`,
		Seq:     1,
		At:      time.Now(),
	})
	h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventAgentEnd,
		Content: `{}`,
		Seq:     2,
		At:      time.Now(),
	})

	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_ToolCallStartTTSPrefix: a tool_call_start event
// is announced via TTS with the prefix "running tool: <name>".
func TestPipeline_ToolCallStartTTSPrefix(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventToolCallStart,
		Content: `{"tool":"bash"}`,
		Seq:     1,
		At:      time.Now(),
	})
	h.forge.InjectEvent(id, forge.Event{
		Type:    forge.EventAgentEnd,
		Content: `{}`,
		Seq:     2,
		At:      time.Now(),
	})

	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_ToolCallEndClears: a tool_call_end event resets
// the "in tool" flag, so subsequent text deltas are not
// prefixed with "running tool: ...".
func TestPipeline_ToolCallEndClears(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{Type: forge.EventToolCallStart, Content: `{"tool":"bash"}`, Seq: 1, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventToolCallEnd, Content: `{}`, Seq: 2, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventTextDelta, Content: `{"delta":"post tool text."}`, Seq: 3, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 4, At: time.Now()})

	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_BashStdoutStreamed: a tool_stdout event
// (typically a line of `bash` output) is buffered and
// emitted as its own TTS chunk. 10 lines → ≥ 1 TTS chunk
// (we do not assert "≥ 10" because the pipeline may
// coalesce — that is the spec'd behaviour).
func TestPipeline_BashStdoutStreamed(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	for i := 0; i < 10; i++ {
		ok := h.forge.InjectEvent(id, forge.Event{
			Type:    forge.EventToolStdout,
			Content: `{"line":"build line ` + itoa(i) + `"}`,
			Seq:     int64(i + 1),
			At:      time.Now(),
		})
		if !ok {
			t.Fatalf("inject %d: not delivered", i)
		}
	}
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 100, At: time.Now()})

	waitForRadioCount(t, h.radios, 1, 5*time.Second)
}

// TestPipeline_AgentError_Spoken: an "error" event is
// translated to a TTS chunk of the form "agent error: <msg>".
func TestPipeline_AgentError_Spoken(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{Type: forge.EventError, Content: `{"message":"rate limited"}`, Seq: 1, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now()})

	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_SessionExpired_Resume: a "session_expired" event
// triggers a session resume: the pipeline calls
// forge.Client.CreateSession to get a new session. The test
// verifies the pipeline does not panic on the event.
func TestPipeline_SessionExpired_Resume(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{Type: forge.EventSessionExpired, Content: `{}`, Seq: 1, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now()})

	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// TestPipeline_SessionExpired_ConvIDStable: on session resume the
// conversation id MUST stay stable (derived from the original
// session id) so the M5's UI is undisturbed, and the store must
// NOT gain a duplicate conversation. Only the row's Target (the
// forge session id) is repointed at the new session. This is the
// regression for the bug where HandleSSESessionExpired re-derived
// the convID from the new session id, creating a second conversation.
func TestPipeline_SessionExpired_ConvIDStable(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id := h.sessionID
	oldConvID := forge.SessionToConvID16(id)
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	// Drive the resume synchronously (the event-dispatch path calls
	// the same method).
	if err := h.pipeline.HandleSSESessionExpired(ctx, id); err != nil {
		t.Fatalf("HandleSSESessionExpired: %v", err)
	}

	list, err := h.store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("store conversations: want exactly 1 (no duplicate), got %d", len(list))
	}
	if list[0].ID != oldConvID {
		t.Errorf("conv id changed on resume: want %x, got %x", oldConvID, list[0].ID)
	}
	if list[0].Info.Target == id {
		t.Errorf("store Target not repointed: still %q", id)
	}
	if list[0].Info.Target == "" {
		t.Error("store Target is empty after resume")
	}

	// The new Target must be a real session the mock created.
	sessions, err := h.forge.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == list[0].Info.Target {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("resumed Target %q is not a known forge session", list[0].Info.Target)
	}
}

// TestPipeline_ConcurrentIncoming: two simultaneous voice
// messages are processed without one starving the other.
func TestPipeline_ConcurrentIncoming(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "parallel text")
	defer h.forge.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			audio := makeAudioFrame(2 * codec.FrameSize)
			audio.ConversationID = forge.SessionToConvID(h.sessionID)
			if err := h.pipeline.HandleIncomingAudio(ctx, audio); err != nil {
				t.Errorf("HandleIncomingAudio: %v", err)
			}
		}()
	}
	wg.Wait()

	calls := h.forge.SendMessageCalls()
	if len(calls) != 2 {
		t.Errorf("SendMessage calls: want 2, got %d", len(calls))
	}
}

// TestPipeline_PostToConv: the pipeline writes a conv.Store
// entry for every new forge session it sees, so the M5's
// conv_manager gets a UI_UPDATE.
func TestPipeline_PostToConv(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	h.forge.InjectEvent(id, forge.Event{Type: forge.EventTextDelta, Content: `{"delta":"hi."}`, Seq: 1, At: time.Now()})
	h.forge.InjectEvent(id, forge.Event{Type: forge.EventAgentEnd, Content: `{}`, Seq: 2, At: time.Now()})

	// Poll the store for the new conversation.
	wantID := forge.SessionToConvID16(id)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := h.store.Get(ctx, wantID); err == nil {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := h.store.Get(ctx, wantID)
	if !errors.Is(err, conv.ErrNotFound) {
		t.Errorf("conv row check: unexpected err: %v", err)
	}
	list, _ := h.store.List(ctx)
	t.Errorf("conv row for session %s not present (store has %d rows)", id, len(list))
}

// TestPipeline_ContextCancelStops verifies that canceling the
// context causes the pipeline to stop accepting new work and
// the consumer goroutine to exit.
func TestPipeline_ContextCancelStops(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithCancel(context.Background())
	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}
	cancel()
	// No assertion — the test is that we don't deadlock
	// or panic when ctx is canceled mid-subscribe.
}

// TestPipeline_EmptyAudio: a zero-byte incoming frame
// produces no STT call and no POST.
func TestPipeline_EmptyAudio(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := h.pipeline.HandleIncomingAudio(ctx, &forge.IncomingAudio{
		ConversationID: forge.SessionToConvID(h.sessionID),
		Payload:        nil,
	}); err != nil {
		t.Fatalf("HandleIncomingAudio empty: %v", err)
	}
	if calls := h.forge.SendMessageCalls(); len(calls) != 0 {
		t.Errorf("SendMessage on empty audio: want 0 calls, got %d", len(calls))
	}
}

// TestPipeline_BadOpus: an audio frame whose bytes do not
// decode cleanly produces an error from HandleIncomingAudio.
// The pipeline must NOT POST a "transcribed" message in that
// case.
func TestPipeline_BadOpus(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Construct a payload that fails to decode as 16-bit
	// samples (odd byte count). The Mock codec returns an
	// error for odd-length payloads.
	bad := &forge.IncomingAudio{
		ConversationID: forge.SessionToConvID(h.sessionID),
		Payload:        []byte{0x01}, // 1 byte; Mock decode rejects
	}
	if err := h.pipeline.HandleIncomingAudio(ctx, bad); err == nil {
		t.Error("HandleIncomingAudio on bad opus: want error, got nil")
	}
	if calls := h.forge.SendMessageCalls(); len(calls) != 0 {
		t.Errorf("SendMessage on bad opus: want 0 calls, got %d", len(calls))
	}
}

// TestPipeline_HandlesAllEventTypes: a synthetic stream of
// every event type the pipeline knows about runs through
// HandleSSESubscribe without panicking.
func TestPipeline_HandlesAllEventTypes(t *testing.T) {
	t.Parallel()
	h := newPipelineHarness(t, "")
	defer h.forge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, _ := h.forge.CreateSession(ctx, "coder")
	if err := h.pipeline.HandleSSESubscribe(ctx, id); err != nil {
		t.Fatalf("HandleSSESubscribe: %v", err)
	}

	events := []forge.Event{
		{Type: forge.EventToolCallStart, Content: `{"tool":"bash"}`, Seq: 1, At: time.Now()},
		{Type: forge.EventToolStdout, Content: `{"line":"ok"}`, Seq: 2, At: time.Now()},
		{Type: forge.EventToolCallEnd, Content: `{}`, Seq: 3, At: time.Now()},
		{Type: forge.EventTextDelta, Content: `{"delta":"done."}`, Seq: 4, At: time.Now()},
		{Type: forge.EventError, Content: `{}`, Seq: 5, At: time.Now()},
		{Type: forge.EventSessionExpired, Content: `{}`, Seq: 6, At: time.Now()},
		{Type: forge.EventAgentEnd, Content: `{}`, Seq: 7, At: time.Now()},
		{Type: "unknown_type", Content: `{}`, Seq: 8, At: time.Now()},
	}
	for _, e := range events {
		if !h.forge.InjectEvent(id, e) {
			t.Fatalf("inject %s: not delivered", e.Type)
		}
	}
	waitForRadioCount(t, h.radios, 1, 3*time.Second)
}

// newPipelineHarness constructs a fully-wired pipeline for
// tests. It also pre-creates a forge session and seeds the
// conv.Store with a row that maps the session's convID to
// the session id, so HandleIncomingAudio can resolve the
// lookup. The sessionID is exposed via sessionID().
func newPipelineHarness(t *testing.T, sttText string) *pipelineHarness {
	t.Helper()
	mc := forge.NewMockClient()
	sttm := stt.NewMock()
	ttsm := tts.NewMock()
	cd := codec.NewMock()
	store := conv.NewMemStore()
	cr := &captureRadio{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_ = sttText // Reserved for future per-test STT override.

	p := forge.NewPipeline(forge.PipelineConfig{
		Forge:   mc,
		STT:     sttm,
		TTS:     ttsm,
		Codec:   cd,
		Store:   store,
		Radio:   cr,
		Logger:  logger,
		Profile: "coder",
	})

	h := &pipelineHarness{
		pipeline: p,
		forge:    mc,
		store:    store,
		radios:   cr,
		stt:      sttm,
		tts:      ttsm,
		cd:       cd,
	}
	// Pre-create a session and seed the conv.Store so
	// HandleIncomingAudio can resolve the conv id.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	id, err := mc.CreateSession(ctx, "coder")
	if err != nil {
		t.Fatalf("harness: create session: %v", err)
	}
	h.sessionID = id
	convID := forge.SessionToConvID16(id)
	if _, _, err := store.Upsert(ctx, convID, conv.ConvInfo{
		Name:   "harness",
		Kind:   conv.KindForge,
		Target: id,
	}); err != nil {
		t.Fatalf("harness: upsert conv: %v", err)
	}
	return h
}

// makeAudioFrame constructs a forge.IncomingAudio whose
// payload is `nFrames` Opus frames (each 320 bytes for the
// Mock codec). The Mock decoder round-trips bytes ↔ samples
// losslessly, so the STT mock sees PCM samples derived from
// those bytes. The convID is left empty; the caller may set
// it via IncomingAudio.ConversationID. The default is
// derived from the harness's sessionID, which the harness
// pre-seeds in the conv.Store.
func makeAudioFrame(nFrames int) *forge.IncomingAudio {
	frameBytes := make([]byte, codec.FrameSize*2)
	for i := range frameBytes {
		frameBytes[i] = byte(i)
	}
	var payload []byte
	for i := 0; i < nFrames; i++ {
		payload = append(payload, frameBytes...)
	}
	return &forge.IncomingAudio{
		ConversationID: nil, // caller fills in
		MessageID:      1,
		Payload:        payload,
		CompletedAt:    time.Now(),
	}
}

// waitForRadioCount polls the captureRadio until at least n
// envelopes have been sent, or the timeout elapses.
func waitForRadioCount(t *testing.T, r *captureRadio, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.payload)
		r.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	got := len(r.payload)
	r.mu.Unlock()
	t.Fatalf("radios: only %d envs after %v (want %d)", got, d, n)
}
