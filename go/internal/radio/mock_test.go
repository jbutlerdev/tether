// Tests for the mock radio. See plan.md §1.5.
package radio_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// newMock is a small constructor that wires up a Mock with reasonable
// defaults and registers cleanup.
func newMock(t *testing.T, opts ...radio.MockOption) *radio.Mock {
	t.Helper()
	m := radio.NewMock(opts...)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func sampleEnv() *protocolpb.Envelope {
	return &protocolpb.Envelope{
		ProtocolVersion: 1,
		TargetId:        &protocolpb.NodeId{Value: 0xFFFF},
		SenderId:        &protocolpb.NodeId{Value: 0x0001},
		ConversationId:  bytes.Repeat([]byte{0xCD}, 16),
		MessageId:       1,
		Payload:         []byte("hello"),
	}
}

func TestMockRadio_SendReceive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	m := newMock(t)
	if err := m.Init(ctx, radio.Preset{SpreadingFactor: 7, BandwidthHz: 125000}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	env := sampleEnv()
	if err := m.Send(ctx, env); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got, err := m.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got.MessageId != env.MessageId {
		t.Errorf("MessageId: want %d, got %d", env.MessageId, got.MessageId)
	}
	if !bytes.Equal(got.Payload, env.Payload) {
		t.Errorf("Payload: want %q, got %q", env.Payload, got.Payload)
	}
}

func TestMockRadio_SendDropsOnFullQueue(t *testing.T) {
	ctx := context.Background()
	m := newMock(t, radio.MockOptionMaxQueueSize(2))

	if err := m.Send(ctx, sampleEnv()); err != nil {
		t.Fatalf("Send #1: %v", err)
	}
	if err := m.Send(ctx, sampleEnv()); err != nil {
		t.Fatalf("Send #2: %v", err)
	}
	// The third Send should fail because the queue is full and no
	// goroutine is draining via Receive.
	err := m.Send(ctx, sampleEnv())
	if !errors.Is(err, radio.ErrQueueFull) {
		t.Fatalf("Send #3: want ErrQueueFull, got %v", err)
	}
}

func TestMockRadio_ConcurrentSendReceive(t *testing.T) {
	// Run with `go test -race` to detect data races.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	m := newMock(t, radio.MockOptionMaxQueueSize(64))

	const (
		producers = 50
		consumers = 50
		perWorker = 10
	)
	var wg sync.WaitGroup
	var sent, received int64

	// Producers
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				env := sampleEnv()
				env.MessageId = uint32(j + 1)
				if err := m.Send(ctx, env); err != nil {
					if errors.Is(err, radio.ErrQueueFull) {
						time.Sleep(100 * time.Microsecond)
						continue
					}
					t.Errorf("Send: %v", err)
					return
				}
				atomic.AddInt64(&sent, 1)
			}
		}()
	}

	// Consumers
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				ctx2, cancel2 := context.WithTimeout(ctx, 500*time.Millisecond)
				_, err := m.Receive(ctx2)
				cancel2()
				if err != nil {
					if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
						continue
					}
					t.Errorf("Receive: %v", err)
					return
				}
				atomic.AddInt64(&received, 1)
			}
		}()
	}

	wg.Wait()
	if atomic.LoadInt64(&received) == 0 {
		t.Errorf("no envelopes received across 100 goroutines")
	}
}

func TestMockRadio_ChannelSwitch(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.SetChannel(ctx, radio.Channel{Index: 0}); err != nil {
		t.Fatalf("SetChannel #0: %v", err)
	}
	if err := m.SetChannel(ctx, radio.Channel{Index: 7}); err != nil {
		t.Fatalf("SetChannel #7: %v", err)
	}

	if got := m.CurrentChannel(); got.Index != 7 {
		t.Errorf("CurrentChannel().Index: want 7, got %d", got.Index)
	}

	// SetChannel must take effect on the next TX; verify by sending
	// and inspecting the recorded channel.
	if err := m.Send(ctx, sampleEnv()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := m.LastSendChannel(); got.Index != 7 {
		t.Errorf("LastSendChannel().Index: want 7, got %d", got.Index)
	}
}

func TestMockRadio_ContextCancel(t *testing.T) {
	m := newMock(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := m.Receive(ctx)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Receive on canceled ctx: want io.EOF, got %v", err)
	}
}

func TestMockRadio_CloseIdempotent(t *testing.T) {
	m := newMock(t)
	// Close is called again in t.Cleanup, so two calls total.
	if err := m.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second call must not panic and should return nil.
	if err := m.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// Compile-time check that Mock implements Radio.
var _ radio.Radio = (*radio.Mock)(nil)

// ─── error-path coverage ──────────────────────────────────────────────

func TestMockRadio_SendNil(t *testing.T) {
	m := newMock(t)
	if err := m.Send(context.Background(), nil); err == nil {
		t.Fatal("Send(nil): want error, got nil")
	}
}

func TestMockRadio_SendOnClosed(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Send(ctx, sampleEnv()); err == nil {
		t.Fatal("Send on closed: want error, got nil")
	}
}

func TestMockRadio_InitOnClosed(t *testing.T) {
	m := newMock(t)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Init(context.Background(), radio.Preset{}); err == nil {
		t.Fatal("Init on closed: want error, got nil")
	}
}

func TestMockRadio_SetChannelOnClosed(t *testing.T) {
	m := newMock(t)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.SetChannel(context.Background(), radio.Channel{Index: 3}); err == nil {
		t.Fatal("SetChannel on closed: want error, got nil")
	}
}

func TestMockRadio_ReceiveOnClosed(t *testing.T) {
	m := newMock(t)
	// Pre-load an envelope, then close and drain. We must observe EOF
	// on a fresh Receive call.
	if err := m.Send(context.Background(), sampleEnv()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The pre-loaded envelope is still in the queue; Receive may
	// return it or EOF depending on whether the queue was drained
	// on Close. The contract is that *eventually* EOF is returned.
	_, _ = m.Receive(context.Background())
	_, err := m.Receive(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Receive after drain: want io.EOF, got %v", err)
	}
}

func TestMockRadio_TxAirtime(t *testing.T) {
	// An airtime > 0 should make Send take a noticeable amount of time
	// but still complete successfully. We use a small delay (1ms) so
	// the test runs in a few ms.
	m := newMock(t, radio.MockOptionTxAirtime(1*time.Millisecond))
	ctx := context.Background()

	start := time.Now()
	if err := m.Send(ctx, sampleEnv()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Microsecond {
		t.Errorf("Send returned too quickly (%v) — airtime delay not applied?", elapsed)
	}
}

func TestMockRadio_TxAirtimeCtxCancel(t *testing.T) {
	// With a long airtime, a canceled ctx must abort Send promptly.
	m := newMock(t, radio.MockOptionTxAirtime(10*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := m.Send(ctx, sampleEnv())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Send on canceled ctx: want error, got nil")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Send did not abort promptly on cancel: %v", elapsed)
	}
}

// TestMockRadio_CloseMidAirtime covers the post-airtime-delta closed
// check: a Send that started while the mock was open can find the
// mock closed by the time the airtime delay elapses.
func TestMockRadio_CloseMidAirtime(t *testing.T) {
	m := newMock(t, radio.MockOptionTxAirtime(50*time.Millisecond))
	env := sampleEnv()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Send(context.Background(), env)
	}()

	// Give the goroutine a moment to enter the airtime wait, then close.
	time.Sleep(10 * time.Millisecond)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Send: want error after mid-airtime close, got nil")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Send did not return after close")
	}
}

func TestMockRadio_OptionTxAirtime(t *testing.T) {
	// Cover the option setter.
	m := radio.NewMock(radio.MockOptionTxAirtime(2 * time.Millisecond))
	defer m.Close()
	if m == nil {
		t.Fatal("NewMock with TxAirtime option: got nil")
	}
}
