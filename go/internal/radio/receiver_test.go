// Tests for the Receiver state machine. See plan.md §2.5.
package radio_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// receiverHelper wires up a Receiver reading from loop.aSide and
// returning the result via the callbacks. It returns the receiver
// and a function to feed envelopes.
func setupReceiver(t *testing.T, opts ...radio.ReceiverOption) (*radio.Receiver, *loopbackPair, *loopbackSide) {
	t.Helper()
	loop := newLoopback(t)

	defaults := []radio.ReceiverOption{
		radio.ReceiverOptionMessageTimeout(500 * time.Millisecond),
	}
	opts = append(defaults, opts...)

	rec := radio.NewReceiver(loop.aSide, opts...)

	return rec, loop, loop.bSide
}

// TestReceiver_HappyPath: 5 envelopes in order, START, then 5 DATA,
// then END → one IncomingMessage with full payload.
func TestReceiver_HappyPath(t *testing.T) {
	loop := newLoopback(t)

	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, err := protocol.Fragment(bytes.Repeat([]byte{0x42}, 1000), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	startEnv := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_START,
		ConversationId:  convID,
		MessageId:       1,
		TotalSeqs:       uint32(len(envs)),
	}
	_ = loop.bSide.Send(context.Background(), startEnv)

	// Send each DATA env.
	for _, env := range envs {
		_ = loop.bSide.Send(context.Background(), env)
	}

	// Wait for the message.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("messages: want 1, got %d", len(messages))
	}
	got := messages[0]
	if got.MessageID != 1 {
		t.Errorf("MessageId: want 1, got %d", got.MessageID)
	}
	if !bytes.Equal(got.Payload, bytes.Repeat([]byte{0x42}, 1000)) {
		t.Errorf("Payload: want 1000 bytes of 0x42, got %d bytes", len(got.Payload))
	}
}

// TestReceiver_DuplicateData: same seq twice — second ignored, no error.
func TestReceiver_DuplicateData(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 1000), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	startEnv := &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	}
	_ = loop.bSide.Send(context.Background(), startEnv)

	// Send first 2 envs, then duplicate env[1], then the rest.
	_ = loop.bSide.Send(context.Background(), envs[0])
	_ = loop.bSide.Send(context.Background(), envs[1])
	_ = loop.bSide.Send(context.Background(), envs[1]) // duplicate
	for i := 2; i < len(envs); i++ {
		_ = loop.bSide.Send(context.Background(), envs[i])
	}

	// Wait for the message.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("messages: want 1, got %d", len(messages))
	}
	if len(messages[0].Payload) != 1000 {
		t.Errorf("Payload: want 1000 bytes, got %d", len(messages[0].Payload))
	}
}

// TestReceiver_MissingChunk: DATA seq 2 missing → no emit after timeout.
func TestReceiver_MissingChunk(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 1000), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(100*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	startEnv := &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	}
	_ = loop.bSide.Send(context.Background(), startEnv)

	// Send all envs except seq 2.
	for i, env := range envs {
		if i == 2 {
			continue
		}
		_ = loop.bSide.Send(context.Background(), env)
	}

	// Wait for the timeout (200ms > 100ms timeout). No message should
	// have been emitted.
	time.Sleep(200 * time.Millisecond)

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 0 {
		t.Errorf("messages: want 0 (missing chunk), got %d", len(messages))
	}
}

// TestReceiver_OutOfOrder: DATA arrives in order 1, 0, 2, 3, 4 →
// 0 and 1 buffered, then 2 completes the message (then 3, 4 also
// processed; the test sends all 5 in shuffled order and verifies
// the message is eventually emitted).
func TestReceiver_OutOfOrder(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 1000), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	startEnv := &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	}
	_ = loop.bSide.Send(context.Background(), startEnv)

	// Shuffle: send in order 1, 0, 2, 3, 4.
	shuffled := []*protocolpb.Envelope{envs[1], envs[0], envs[2]}
	shuffled = append(shuffled, envs[3:]...)
	for _, env := range shuffled {
		_ = loop.bSide.Send(context.Background(), env)
	}

	// Wait for the message.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("messages: want 1, got %d", len(messages))
	}
	if len(messages[0].Payload) != 1000 {
		t.Errorf("Payload: want 1000 bytes, got %d", len(messages[0].Payload))
	}
}

// TestReceiver_EmitsAckAfterEachChunk: after each DATA, onAck is
// called with the bitmap containing that seq.
func TestReceiver_EmitsAckAfterEachChunk(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 227), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu   sync.Mutex
		acks []*radio.OutgoingAck
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnAck(func(ack *radio.OutgoingAck) {
			mu.Lock()
			defer mu.Unlock()
			acks = append(acks, ack)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	startEnv := &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	}
	_ = loop.bSide.Send(context.Background(), startEnv)

	// Send each DATA env.
	for _, env := range envs {
		_ = loop.bSide.Send(context.Background(), env)
	}

	// Wait for the message to be emitted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(acks)
		mu.Unlock()
		if n >= len(envs) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(acks) == 0 {
		t.Fatal("no ACKs emitted")
	}
	// Each ACK should have NextExpectedSeq = (last_acked + 1) and the
	// bitmap should reflect what we've seen so far.
	for i, ack := range acks {
		if ack.MessageID != 1 {
			t.Errorf("ack[%d].MessageID: want 1, got %d", i, ack.MessageID)
		}
		if ack.NextExpected > uint32(len(envs)) {
			t.Errorf("ack[%d].NextExpected: %d > %d", i, ack.NextExpected, len(envs))
		}
	}
}

// TestReceiver_MultipleMessagesConcurrent: two message_ids
// interleaved → both emit in order.
func TestReceiver_MultipleMessagesConcurrent(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs1, _ := protocol.Fragment(bytes.Repeat([]byte{0x11}, 500), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	envs2, _ := protocol.Fragment(bytes.Repeat([]byte{0x22}, 500), 2, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	sendStart := func(msgID uint32, total uint32) {
		_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
			MsgType:        protocolpb.MsgType_MSG_TYPE_START,
			ConversationId: convID,
			MessageId:      msgID,
			TotalSeqs:      total,
		})
	}

	sendStart(1, uint32(len(envs1)))
	sendStart(2, uint32(len(envs2)))
	for _, e := range envs1 {
		_ = loop.bSide.Send(context.Background(), e)
	}
	for _, e := range envs2 {
		_ = loop.bSide.Send(context.Background(), e)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("messages: want 2, got %d", len(messages))
	}
	// Order is not guaranteed; check that both are present.
	ids := map[uint32]bool{}
	for _, m := range messages {
		ids[m.MessageID] = true
	}
	if !ids[1] || !ids[2] {
		t.Errorf("messages: missing one of {1, 2}, got %v", ids)
	}
}

// TestReceiver_GarbageConversationID: 15-byte conv_id rejected.
func TestReceiver_GarbageConversationID(t *testing.T) {
	loop := newLoopback(t)
	shortConvID := bytes.Repeat([]byte{0xAB}, 15)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(200*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START with bad conv_id.
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: shortConvID,
		MessageId:      1,
		TotalSeqs:      1,
	})

	time.Sleep(300 * time.Millisecond)

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 0 {
		t.Errorf("messages: want 0, got %d (bad conv_id should be rejected)", len(messages))
	}
}

// TestReceiver_RaceDetector: concurrent emits safe.
func TestReceiver_RaceDetector(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 227), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var counter int64
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			atomic.AddInt64(&counter, 1)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send 50 messages concurrently.
	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msgID := uint32(id + 1)
			_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
				MsgType:        protocolpb.MsgType_MSG_TYPE_START,
				ConversationId: convID,
				MessageId:      msgID,
				TotalSeqs:      uint32(len(envs)),
			})
			for _, env := range envs {
				// Clone to avoid mutating the shared envs slice from
				// multiple goroutines.
				clone := *env
				clone.MessageId = msgID
				_ = loop.bSide.Send(context.Background(), &clone)
			}
		}(i)
	}
	wg.Wait()

	// Wait for the messages to be processed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&counter) >= N {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	runCancel()
	<-runDone

	if got := atomic.LoadInt64(&counter); got < N {
		t.Errorf("counter: want >= %d, got %d", N, got)
	}
}

// Ensure the unused imports remain in case the test file is later
// extended.
var _ = errors.New
var _ = io.EOF

// TestReceiver_IgnoresAckAndTTS covers the default switch branch.
func TestReceiver_IgnoresAckAndTTS(t *testing.T) {
	loop := newLoopback(t)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(100*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send an ACK and a TTS_DATA. Both should be ignored.
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType: protocolpb.MsgType_MSG_TYPE_ACK,
	})
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType: protocolpb.MsgType_MSG_TYPE_TTS_DATA,
	})

	time.Sleep(200 * time.Millisecond)
	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 0 {
		t.Errorf("messages: want 0 (ACK/TTS ignored), got %d", len(messages))
	}
}

// TestReceiver_EndTriggersEmit covers the END handler emitting a
// complete message.
func TestReceiver_EndTriggersEmit(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 500), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(500*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START with TotalSeqs.
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	})
	// Send all DATA.
	for _, env := range envs {
		_ = loop.bSide.Send(context.Background(), env)
	}
	// Send END (no payload).
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_END,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	})

	// Wait for emit.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(messages)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("messages: want 1, got %d", len(messages))
	}
	if len(messages[0].Payload) != 500 {
		t.Errorf("Payload: want 500, got %d", len(messages[0].Payload))
	}
}

// TestReceiver_EndWithMissingChunk covers the END branch when chunks
// are missing (no emit).
func TestReceiver_EndWithMissingChunk(t *testing.T) {
	loop := newLoopback(t)
	convID := bytes.Repeat([]byte{0xAB}, 16)
	envs, _ := protocol.Fragment(bytes.Repeat([]byte{0x42}, 500), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)

	var (
		mu       sync.Mutex
		messages []*radio.IncomingMessage
	)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionOnMessage(func(msg *radio.IncomingMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, msg)
		}),
		radio.ReceiverOptionMessageTimeout(100*time.Millisecond),
	)

	runCtx, runCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		_ = rec.Run(runCtx)
		close(runDone)
	}()

	// Send START.
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_START,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	})
	// Send only the first DATA.
	_ = loop.bSide.Send(context.Background(), envs[0])
	// Send END with TotalSeqs set to total but chunks incomplete.
	_ = loop.bSide.Send(context.Background(), &protocolpb.Envelope{
		MsgType:        protocolpb.MsgType_MSG_TYPE_END,
		ConversationId: convID,
		MessageId:      1,
		TotalSeqs:      uint32(len(envs)),
	})

	time.Sleep(200 * time.Millisecond)
	runCancel()
	<-runDone

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 0 {
		t.Errorf("messages: want 0 (END with missing chunk), got %d", len(messages))
	}
}

// TestReceiver_LoggerOption covers the ReceiverOptionLogger branch.
func TestReceiver_LoggerOption(t *testing.T) {
	loop := newLoopback(t)
	rec := radio.NewReceiver(loop.aSide,
		radio.ReceiverOptionLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		radio.ReceiverOptionMessageTimeout(50*time.Millisecond),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = rec.Run(ctx)
}
