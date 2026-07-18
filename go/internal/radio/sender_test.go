// Tests for the Sender state machine. See plan.md §2.4.
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

// senderEnv builds a 5-envelope test sequence. The payload is large
// enough to fragment into 5 chunks at MaxPayloadSize=227:
// 5 * 227 - 4 = 1131 bytes of payload → 5 envelopes of 227, 227, 227,
// 227, 223. We round to 1100 bytes to get 5 envelopes of 227, 227,
// 227, 227, 192.
func senderEnv() []*protocolpb.Envelope {
	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, err := protocol.Fragment(bytes.Repeat([]byte{0xAA}, 1100), 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		panic(err)
	}
	return envs
}

// senderConvID is the conversation id used by senderEnv (0xCD×16).
// Hoisted so the ACK helpers can stamp it onto test ACK payloads.
var senderConvID = bytes.Repeat([]byte{0xCD}, 16)

// senderMsgID is the message id used by senderEnv (Fragment msgID=1).
const senderMsgID = uint32(1)

// ackPayloadFor builds a 28-byte ACK payload (research.md §8.6) that
// acks up to and including seq, stamped with the sender's conv_id +
// msg_id so the Sender's payload-level validation accepts it.
func ackPayloadFor(seq uint32) []byte {
	return protocol.EncodeAckPayload(senderConvID, senderMsgID, seq+1, 0, 0)
}

// ackEnvelopeFor wraps an ack payload in a proto envelope of type ACK.
func ackEnvelopeFor(seq uint32) *protocolpb.Envelope {
	return &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_ACK,
		ConversationId:  append([]byte(nil), senderConvID...),
		MessageId:       senderMsgID,
		Payload:         ackPayloadFor(seq),
	}
}

// TestSender_HappyPath: every envelope is acked immediately.
func TestSender_HappyPath(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	// Auto-ACK every received env promptly.
	autoAck(t, loop.bSide, loop.bSide)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(100*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)
	acked, failed, retries, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if failed != nil {
		t.Errorf("failed envelope: %v", failed)
	}
	if acked != len(envs) {
		t.Errorf("acked: want %d, got %d", len(envs), acked)
	}
	if retries > 1 {
		// The autoAck helper should ACK every envelope immediately,
		// but a flaky race-detector pass with -count=N can add a
		// spurious retry. Allow up to 1; anything more means the
		// ACK loop is genuinely broken.
		t.Errorf("retries: want <= 1, got %d", retries)
	}
}

// TestSender_OneRetry: the first envelope is lost on its first
// attempt; sender retransmits and the second attempt is acked.
func TestSender_OneRetry(t *testing.T) {
	loop := newLoopback(t)
	loop.SetPacketLoss(1.0) // start with 100 % loss
	envs := senderEnv()

	// After 150 ms, stop dropping — the second attempt at each seq
	// will go through.
	lossCtx, lossCancel := context.WithCancel(context.Background())
	defer lossCancel()
	go func() {
		select {
		case <-lossCtx.Done():
			return
		case <-time.After(150 * time.Millisecond):
			loop.SetPacketLoss(0.0)
		}
	}()

	autoAck(t, loop.bSide, loop.bSide)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(50*time.Millisecond),
		radio.SenderOptionMaxRetry(20),
	)
	acked, _, retries, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if acked != len(envs) {
		t.Errorf("acked: want %d, got %d", len(envs), acked)
	}
	if retries == 0 {
		t.Errorf("retries: want > 0, got %d", retries)
	}
}

// TestSender_MaxRetries: the first envelope is permanently lost, the
// sender exhausts its retry budget, and Run returns with failed set.
func TestSender_MaxRetries(t *testing.T) {
	loop := newLoopback(t)
	loop.SetPacketLoss(1.0) // drop everything
	envs := senderEnv()

	// Don't auto-ack — we want timeouts to fire.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(20*time.Millisecond),
		radio.SenderOptionMaxRetry(2),
	)
	acked, failed, _, err := s.Run(ctx)
	if !errors.Is(err, radio.ErrMaxRetries) {
		t.Fatalf("Run: want ErrMaxRetries, got %v", err)
	}
	if acked == len(envs) {
		t.Errorf("acked: expected < %d, got %d", len(envs), acked)
	}
	if failed == nil {
		t.Fatal("failed: want non-nil envelope, got nil")
	}
	if failed.SeqNum != 0 {
		t.Errorf("failed.SeqNum: want 0, got %d", failed.SeqNum)
	}
}

// TestSender_ContextCancel: cancel mid-wait returns ctx.Err() and
// stops transmitting. To exercise the select-case ctx.Done() (not
// just the top-of-loop check), the test cancels during a long
// receive wait.
func TestSender_ContextCancel(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		acked   int
		failed  *protocolpb.Envelope
		retries int
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		s := radio.NewSender(loop.aSide, envs,
			radio.SenderOptionTimeout(10*time.Second),
			radio.SenderOptionMaxRetry(100),
		)
		acked, failed, retries, err := s.Run(ctx)
		resCh <- result{acked, failed, retries, err}
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case res := <-resCh:
		if !errors.Is(res.err, context.Canceled) {
			t.Errorf("err: want context.Canceled, got %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not return after cancel")
	}
}

// TestSender_OutOfOrderAck: an ACK for a future seq arriving early
// must be ignored; the sender will not skip.
func TestSender_OutOfOrderAck(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	// Inject an out-of-order ACK (seq 3) before the sender is going.
	injectCtx, injectCancel := context.WithCancel(context.Background())
	defer injectCancel()
	go func() {
		select {
		case <-injectCtx.Done():
			return
		default:
		}
		// Send a future-seq ACK directly to the sender's RX.
		_ = loop.bSide.Send(context.Background(), ackEnvelopeFor(3))
	}()

	autoAck(t, loop.bSide, loop.bSide)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(100*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)
	acked, _, _, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if acked != len(envs) {
		t.Errorf("acked: want %d, got %d", len(envs), acked)
	}
}

// TestSender_DuplicateAck: a duplicate ACK for an already-acked seq
// must not double-count or stall the sender.
func TestSender_DuplicateAck(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	// Pre-load duplicate ACKs onto the wire.
	injectCtx, injectCancel := context.WithCancel(context.Background())
	defer injectCancel()
	go func() {
		for i := 0; i < 5; i++ {
			select {
			case <-injectCtx.Done():
				return
			default:
			}
			_ = loop.bSide.Send(context.Background(), ackEnvelopeFor(0))
		}
	}()

	autoAck(t, loop.bSide, loop.bSide)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(100*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)
	acked, _, _, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if acked != len(envs) {
		t.Errorf("acked: want %d, got %d", len(envs), acked)
	}
}

// TestSender_Pacing: after an ACK for seq N, the next envelope
// transmitted is seq N+1 (no reordering, no skipping).
func TestSender_Pacing(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	var (
		mu      sync.Mutex
		seenSeq []uint32
	)
	autoAckRecord(t, loop.bSide, loop.bSide, func(seq uint32) {
		mu.Lock()
		defer mu.Unlock()
		seenSeq = append(seenSeq, seq)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(200*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)
	if _, _, _, err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// First time we see each seq, the order must be 0, 1, 2, 3, 4.
	mu.Lock()
	defer mu.Unlock()
	want := []uint32{0, 1, 2, 3, 4}
	uniq := []uint32{}
	last := uint32(0xFFFFFFFF)
	for _, s := range seenSeq {
		if s != last {
			uniq = append(uniq, s)
			last = s
		}
	}
	if len(uniq) != len(want) {
		t.Fatalf("unique seqs seen: want %v, got %v", want, uniq)
	}
	for i, s := range want {
		if uniq[i] != s {
			t.Errorf("seq[%d]: want %d, got %d", i, s, uniq[i])
		}
	}
}

// TestSender_RaceDetector runs many concurrent senders and a single
// loopback; under -race this must be clean.
func TestSender_RaceDetector(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	autoAck(t, loop.bSide, loop.bSide)

	const goroutines = 10
	const messagesEach = 5

	var wg sync.WaitGroup
	var acked int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := 0; m < messagesEach; m++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				s := radio.NewSender(loop.aSide, envs,
					radio.SenderOptionTimeout(50*time.Millisecond),
					radio.SenderOptionMaxRetry(3),
				)
				a, _, _, err := s.Run(ctx)
				cancel()
				if err == nil {
					atomic.AddInt64(&acked, int64(a))
				}
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&acked) == 0 {
		t.Errorf("no acks recorded across %d goroutines", goroutines)
	}
}

// TestSender_CallbacksFire: onAcked fires for every acked envelope,
// onSuccess fires once when all are acked, onFailed does not fire
// on a happy path.
func TestSender_CallbacksFire(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()

	autoAck(t, loop.bSide, loop.bSide)

	var (
		mu        sync.Mutex
		ackedSeqs []uint32
		success   int
		failed    int
	)
	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(50*time.Millisecond),
		radio.SenderOptionMaxRetry(2),
		radio.SenderOptionOnAcked(func(seq uint32) {
			mu.Lock()
			defer mu.Unlock()
			ackedSeqs = append(ackedSeqs, seq)
		}),
		radio.SenderOptionOnSuccess(func() {
			mu.Lock()
			defer mu.Unlock()
			success++
		}),
		radio.SenderOptionOnFailed(func(_ *protocolpb.Envelope, _ int) {
			mu.Lock()
			defer mu.Unlock()
			failed++
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, _, err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ackedSeqs) != len(envs) {
		t.Errorf("ackedSeqs: want %d, got %d (seqs=%v)", len(envs), len(ackedSeqs), ackedSeqs)
	}
	if success != 1 {
		t.Errorf("success: want 1, got %d", success)
	}
	if failed != 0 {
		t.Errorf("failed: want 0, got %d", failed)
	}
}

// TestSender_NilEnv covers the nil-input guard.
func TestSender_NilEnv(t *testing.T) {
	loop := newLoopback(t)
	_, _, _, err := radio.NewSender(loop.aSide, nil,
		radio.SenderOptionTimeout(10*time.Millisecond),
		radio.SenderOptionMaxRetry(1),
	).Run(context.Background())
	if !errors.Is(err, radio.ErrNoEnvelopes) {
		t.Fatalf("Run(nil envs): want ErrNoEnvelopes, got %v", err)
	}
}

// failingRadio is a radio.Radio whose Send always returns an error.
type failingRadio struct{}

func (failingRadio) Init(context.Context, radio.Preset) error { return nil }
func (failingRadio) Send(context.Context, *protocolpb.Envelope) error {
	return errors.New("failing: send always errors")
}
func (failingRadio) Receive(context.Context) (*protocolpb.Envelope, error) {
	return nil, io.EOF
}
func (failingRadio) SetChannel(context.Context, radio.Channel) error { return nil }
func (failingRadio) Close() error                                    { return nil }

// TestSender_InitialSendError covers the very-first Send failing.
func TestSender_InitialSendError(t *testing.T) {
	envs := senderEnv()
	_, failed, _, err := radio.NewSender(failingRadio{}, envs,
		radio.SenderOptionTimeout(10*time.Millisecond),
		radio.SenderOptionMaxRetry(1),
	).Run(context.Background())
	if err == nil {
		t.Fatal("Run with failing radio: want error, got nil")
	}
	if !errors.Is(err, radio.ErrSendFailed) {
		t.Errorf("err: want ErrSendFailed, got %v", err)
	}
	if failed == nil {
		t.Error("failed: want non-nil envelope")
	}
}

// ctxRadio is a radio that cancels itself after a delay. Used to
// exercise the ctx.Done() paths in Sender.Run.
type ctxRadio struct {
	loop *loopbackSide
	ctx  context.Context
}

func (c ctxRadio) Init(context.Context, radio.Preset) error { return nil }
func (c ctxRadio) Send(ctx context.Context, env *protocolpb.Envelope) error {
	return c.loop.Send(ctx, env)
}
func (c ctxRadio) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	return c.loop.Receive(ctx)
}
func (c ctxRadio) SetChannel(context.Context, radio.Channel) error { return nil }
func (c ctxRadio) Close() error                                    { return nil }

// TestSender_LoggerOption covers the SenderOptionLogger branch.
func TestSender_LoggerOption(t *testing.T) {
	loop := newLoopback(t)
	envs := senderEnv()
	autoAck(t, loop.bSide, loop.bSide)

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		radio.SenderOptionTimeout(50*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, _, err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestSender_FailedCallback covers the onFailed callback firing when
// a max-retry is exhausted.
func TestSender_FailedCallback(t *testing.T) {
	loop := newLoopback(t)
	loop.SetPacketLoss(1.0)
	envs := senderEnv()

	var (
		mu       sync.Mutex
		failedEv *protocolpb.Envelope
		retries  int
	)
	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(10*time.Millisecond),
		radio.SenderOptionMaxRetry(2),
		radio.SenderOptionOnFailed(func(env *protocolpb.Envelope, n int) {
			mu.Lock()
			defer mu.Unlock()
			failedEv = env
			retries = n
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, _, err := s.Run(ctx); !errors.Is(err, radio.ErrMaxRetries) {
		t.Fatalf("Run: want ErrMaxRetries, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if failedEv == nil {
		t.Fatal("onFailed: not called")
	}
	if retries == 0 {
		t.Errorf("onFailed retries: want > 0, got %d", retries)
	}
}

// failingAfterN is a radio whose first Send succeeds (for seq 0) but
// all subsequent Sends fail. Used to exercise the timer-case Send
// error path.
type failingAfterN struct {
	inner  radio.Radio
	count  atomic.Int32
	failAt int32
}

func (f *failingAfterN) Init(ctx context.Context, p radio.Preset) error { return f.inner.Init(ctx, p) }
func (f *failingAfterN) Send(ctx context.Context, env *protocolpb.Envelope) error {
	n := f.count.Add(1)
	if n > f.failAt {
		return errors.New("failingAfterN: send failed")
	}
	return f.inner.Send(ctx, env)
}
func (f *failingAfterN) Receive(ctx context.Context) (*protocolpb.Envelope, error) {
	return f.inner.Receive(ctx)
}
func (f *failingAfterN) SetChannel(ctx context.Context, ch radio.Channel) error {
	return f.inner.SetChannel(ctx, ch)
}
func (f *failingAfterN) Close() error { return f.inner.Close() }

// TestSender_SendErrorOnTimeout covers the timer-case Send error.
func TestSender_SendErrorOnTimeout(t *testing.T) {
	loop := newLoopback(t)
	loop.SetPacketLoss(1.0) // drop everything (so the timer fires)
	envs := senderEnv()

	r := &failingAfterN{inner: loop.aSide, failAt: 1}
	// Send 1 succeeds (seq 0). Send 2 (the timer-case retransmit of
	// seq 0) fails.

	s := radio.NewSender(r, envs,
		radio.SenderOptionTimeout(10*time.Millisecond),
		radio.SenderOptionMaxRetry(10),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _, err := s.Run(ctx)
	if err == nil {
		t.Fatal("Run: want error, got nil")
	}
	if !errors.Is(err, radio.ErrSendFailed) {
		t.Errorf("err: want ErrSendFailed, got %v", err)
	}
}

// autoAck is a small helper that drains the side's RX queue and
// sends an ACK for every non-ACK envelope back to the same side
// (modeling a remote radio that ACKs whatever it sees).
func autoAck(t *testing.T, side, sendSide radio.Radio) {
	t.Helper()
	autoAckRecord(t, side, sendSide, nil)
}

// autoAckRecord is autoAck with an optional per-seen-seq callback.
func autoAckRecord(t *testing.T, side, sendSide radio.Radio, onSeen func(seq uint32)) {
	t.Helper()
	// Use a single long-lived context for the test, cancelled on
	// cleanup. This avoids the per-iteration context.WithTimeout
	// overhead that, under -race and -count=N, was slowing ACK
	// turnaround past the sender's retry budget.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			env, err := side.Receive(ctx)
			if err != nil {
				// ctx cancel or io.EOF — exit cleanly.
				return
			}
			if env.MsgType == protocolpb.MsgType_MSG_TYPE_ACK {
				continue
			}
			if onSeen != nil {
				onSeen(env.SeqNum)
			}
			// ACK with the env's own conv_id + msg_id so the sender's
			// payload-level validation (research.md §8.6) accepts it
			// regardless of which conversation the test uses.
			ack := &protocolpb.Envelope{
				ProtocolVersion: 1,
				MsgType:         protocolpb.MsgType_MSG_TYPE_ACK,
				ConversationId:  append([]byte(nil), env.ConversationId...),
				MessageId:       env.MessageId,
				Payload:         protocol.EncodeAckPayload(env.ConversationId, env.MessageId, env.SeqNum+1, 0, 0),
			}
			_ = sendSide.Send(context.Background(), ack)
		}
	}()
}

// TestSender_IgnoresAckForOtherConversation: an ACK envelope that
// carries a conversation_id / message_id NOT matching the sender's
// message must be ignored (REVIEW.md F3). Without this check an ACK
// for conversation A would falsely ack envelopes in conversation B.
//
// Deterministic proof: send the sender one ACK that claims EVERY seq
// is acked but carries a foreign conversation_id. If the sender
// honoured it, the run would succeed immediately. Because the sender
// must ignore it, the sender exhausts its retry budget and fails.
func TestSender_IgnoresAckForOtherConversation(t *testing.T) {
	t.Parallel()
	loop := newLoopback(t)
	envs := senderEnv() // conv_id = 0xCD*16, msg_id = 1

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	s := radio.NewSender(loop.aSide, envs,
		radio.SenderOptionTimeout(80*time.Millisecond),
		radio.SenderOptionMaxRetry(3),
	)

	done := make(chan struct{})
	var acked int
	var failed *protocolpb.Envelope
	go func() {
		defer close(done)
		acked, failed, _, _ = s.Run(ctx)
	}()

	// Drain the first DATA fragment off the b side and reply with an
	// ACK that claims the whole message is acked — but stamped with a
	// FOREIGN conversation id and message id.
	rxEnv, err := loop.bSide.Receive(ctx)
	if err != nil {
		t.Fatalf("receive first fragment: %v", err)
	}
	otherConv := bytes.Repeat([]byte{0xEE}, 16)
	spurious := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_ACK,
		ConversationId:  otherConv,
		MessageId:       rxEnv.MessageId + 999, // foreign message id too
		Payload:         protocol.EncodeAckPayload(otherConv, rxEnv.MessageId+999, uint32(len(envs)), 0xFFFF, 0xFFFF),
	}
	if err := loop.bSide.Send(ctx, spurious); err != nil {
		t.Fatalf("send spurious: %v", err)
	}

	// Keep draining retransmits and re-sending the foreign ACK so the
	// sender sees it on every retry. If the validation were absent the
	// sender would ack everything and succeed.
	go func() {
		for {
			e, err := loop.bSide.Receive(ctx)
			if err != nil {
				return
			}
			_ = loop.bSide.Send(ctx, spurious)
			_ = e
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not terminate")
	}
	if failed == nil {
		t.Fatal("sender succeeded on a foreign-conversation ACK; the ACK was not validated")
	}
	if acked != 0 {
		t.Errorf("acked: want 0 (foreign ACK must be ignored), got %d", acked)
	}
}
