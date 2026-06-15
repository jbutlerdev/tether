// Tests for the in-process loopback transport. See plan.md §2.6.
package serial_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

func newLoopbackPair(t *testing.T) (a, b radio.Radio, closer func()) {
	t.Helper()
	pa, pb := serial.NewLoopbackPair()
	t.Cleanup(func() { pa.Close(); pb.Close() })
	return pa, pb, func() { pa.Close(); pb.Close() }
}

// TestLoopback_RoundTrip: 100 packets, 0% loss, all delivered in order.
func TestLoopback_RoundTrip(t *testing.T) {
	pa, pb, _ := newLoopbackPair(t)

	const N = 100
	for i := 0; i < N; i++ {
		env := &protocolpb.Envelope{
			MessageId: uint32(i),
			SeqNum:    uint32(i),
		}
		if err := pa.Send(context.Background(), env); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < N; i++ {
		env, err := pb.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive #%d: %v", i, err)
		}
		if env.SeqNum != uint32(i) {
			t.Errorf("SeqNum: want %d, got %d", i, env.SeqNum)
		}
	}
}

// TestLoopback_BothDirections: both sides can send and receive.
func TestLoopback_BothDirections(t *testing.T) {
	pa, pb, _ := newLoopbackPair(t)

	_ = pa.Send(context.Background(), &protocolpb.Envelope{MessageId: 1})
	_ = pb.Send(context.Background(), &protocolpb.Envelope{MessageId: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	env1, err := pb.Receive(ctx)
	if err != nil {
		t.Fatalf("pb.Receive: %v", err)
	}
	if env1.MessageId != 1 {
		t.Errorf("env1.MessageId: want 1, got %d", env1.MessageId)
	}

	env2, err := pa.Receive(ctx)
	if err != nil {
		t.Fatalf("pa.Receive: %v", err)
	}
	if env2.MessageId != 2 {
		t.Errorf("env2.MessageId: want 2, got %d", env2.MessageId)
	}
}

// TestLoopback_PacketLoss: with 50% loss, ~half are delivered.
func TestLoopback_PacketLoss(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	var pl serial.PacketLosser
	var ok bool
	if pl, ok = pa.(serial.PacketLosser); !ok {
		t.Skip("loopback does not implement PacketLosser")
	}
	pl.SetPacketLoss(0.5)

	const N = 1000
	for i := 0; i < N; i++ {
		_ = pa.Send(context.Background(), &protocolpb.Envelope{SeqNum: uint32(i)})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	delivered := 0
	for {
		env, err := pb.Receive(ctx)
		if err != nil {
			break
		}
		_ = env
		delivered++
	}

	// With 50% loss, expect ~500 delivered, accept 300..700.
	if delivered < 300 || delivered > 700 {
		t.Errorf("delivered: want 300..700 (50%% loss), got %d", delivered)
	}
}

// TestLoopback_ConcurrentClose: closing the receiver side while
// paused in Receive returns io.EOF. We don't pre-load the queue
// because the close-vs-queue race would make the test flaky.
func TestLoopback_ConcurrentClose(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = ctx

	// Start a Receive in a goroutine. It will block on the empty
	// queue. Closing pb unblocks it with io.EOF.
	recvErr := make(chan error, 1)
	go func() {
		_, err := pb.Receive(ctx)
		recvErr <- err
	}()

	// Give the Receive a moment to enter the select.
	time.Sleep(10 * time.Millisecond)
	if err := pb.Close(); err != nil {
		t.Fatalf("pb.Close: %v", err)
	}

	select {
	case err := <-recvErr:
		if !errors.Is(err, io.EOF) {
			t.Errorf("Receive: want io.EOF, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Receive did not return after close")
	}
	_ = pa.Close()
}

// TestLoopback_CloseIdempotent: calling Close twice does not panic.
func TestLoopback_CloseIdempotent(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	if err := pa.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := pa.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestLoopback_Init: Init on open and closed sides.
func TestLoopback_Init(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	if err := pa.Init(context.Background(), radio.Preset{SpreadingFactor: 7}); err != nil {
		t.Fatalf("Init on open: %v", err)
	}
	if err := pa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := pa.Init(context.Background(), radio.Preset{}); err == nil {
		t.Fatal("Init on closed: want error, got nil")
	}
}

// TestLoopback_SendNil covers the nil-env guard.
func TestLoopback_SendNil(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	if err := pa.Send(context.Background(), nil); err == nil {
		t.Fatal("Send(nil): want error, got nil")
	}
}

// TestLoopback_SendOnClosed covers the closed-side error path.
func TestLoopback_SendOnClosed(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	if err := pa.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := pa.Send(context.Background(), &protocolpb.Envelope{}); err == nil {
		t.Fatal("Send on closed: want error, got nil")
	}
}

// TestLoopback_SetChannel covers SetChannel's success and stores
// the index on the side. We don't have a getter, so we only verify
// it doesn't error.
func TestLoopback_SetChannel(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	if err := pa.SetChannel(context.Background(), radio.Channel{Index: 7}); err != nil {
		t.Errorf("SetChannel: %v", err)
	}
}

// TestLoopback_SetPacketLossClamp covers the clamp branches in
// SetPacketLoss (negative values → 0, > 1 → 1).
func TestLoopback_SetPacketLossClamp(t *testing.T) {
	pa, _ := serial.NewLoopbackPair()
	defer pa.Close()

	// Negative clamps to 0 (no loss).
	pa.(serial.PacketLosser).SetPacketLoss(-0.5)
	// > 1 clamps to 1 (full loss).
	pa.(serial.PacketLosser).SetPacketLoss(2.0)
	// In-range should not error.
	pa.(serial.PacketLosser).SetPacketLoss(0.5)
}

// TestLoopback_ReceiveCtxCancel covers Receive returning io.EOF on
// context cancel.
func TestLoopback_ReceiveCtxCancel(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pb.Receive(ctx)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Receive on canceled ctx: want io.EOF, got %v", err)
	}
}

// TestLoopback_QueueFull covers the Send branch when the partner's
// rx queue is full.
func TestLoopback_QueueFull(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	// Fill the queue with no consumer.
	for i := 0; i < 10000; i++ {
		err := pa.Send(context.Background(), &protocolpb.Envelope{SeqNum: uint32(i)})
		if err == nil {
			continue
		}
		if err.Error() == "loopback: rx queue full" {
			// Got the queue-full path. Pass.
			return
		}
		t.Fatalf("unexpected error at i=%d: %v", i, err)
	}
	t.Fatal("never hit queue full after 10000 sends")
}

// TestLoopback_StressConcurrent: many goroutines sending through both
// sides. Race-detector clean.
func TestLoopback_StressConcurrent(t *testing.T) {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	const (
		producers = 20
		perWorker = 50
	)
	var wg sync.WaitGroup
	var sent, received int64

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				env := &protocolpb.Envelope{MessageId: uint32(id*1000 + j)}
				_ = pa.Send(context.Background(), env)
				atomic.AddInt64(&sent, 1)
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			_, err := pb.Receive(ctx)
			cancel()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
					if atomic.LoadInt64(&sent) >= producers*int64(perWorker) {
						return
					}
					continue
				}
				return
			}
			atomic.AddInt64(&received, 1)
		}
	}()

	wg.Wait()
	if atomic.LoadInt64(&received) == 0 {
		t.Error("no envelopes received")
	}
}
