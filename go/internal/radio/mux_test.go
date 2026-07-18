// mux_test.go — tests for the half-duplex radio demuxer.
package radio_test

import (
	"context"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// TestMux_RoutesByMsgType: a DATA envelope goes to DataRadio, an ACK
// goes to AckRadio, and neither crosses over.
func TestMux_RoutesByMsgType(t *testing.T) {
	t.Parallel()
	a, b := serial.NewLoopbackPair()
	mux := radio.NewMux(a)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(mux.Close)
	go func() { _ = mux.Run(ctx) }()

	data := &protocolpb.Envelope{MsgType: protocolpb.MsgType_MSG_TYPE_DATA, Payload: []byte("d")}
	ack := &protocolpb.Envelope{MsgType: protocolpb.MsgType_MSG_TYPE_ACK}

	// b sends one DATA and one ACK; the Mux on a sorts them.
	if err := b.Send(ctx, data); err != nil {
		t.Fatalf("send data: %v", err)
	}
	if err := b.Send(ctx, ack); err != nil {
		t.Fatalf("send ack: %v", err)
	}

	rctx, rcancel := context.WithTimeout(ctx, time.Second)
	defer rcancel()

	gotData, err := mux.DataRadio().Receive(rctx)
	if err != nil {
		t.Fatalf("DataRadio.Receive: %v", err)
	}
	if gotData.MsgType != protocolpb.MsgType_MSG_TYPE_DATA {
		t.Errorf("data radio got %v, want DATA", gotData.MsgType)
	}

	gotAck, err := mux.AckRadio().Receive(rctx)
	if err != nil {
		t.Fatalf("AckRadio.Receive: %v", err)
	}
	if gotAck.MsgType != protocolpb.MsgType_MSG_TYPE_ACK {
		t.Errorf("ack radio got %v, want ACK", gotAck.MsgType)
	}
}

// TestMux_SendDelegates: Send on a sub-radio goes out the underlying
// radio (the peer receives it).
func TestMux_SendDelegates(t *testing.T) {
	t.Parallel()
	a, b := serial.NewLoopbackPair()
	mux := radio.NewMux(a)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(mux.Close)
	go func() { _ = mux.Run(ctx) }()

	env := &protocolpb.Envelope{MsgType: protocolpb.MsgType_MSG_TYPE_DATA, Payload: []byte("x")}
	if err := mux.DataRadio().Send(ctx, env); err != nil {
		t.Fatalf("DataRadio.Send: %v", err)
	}
	rctx, rcancel := context.WithTimeout(ctx, time.Second)
	defer rcancel()
	got, err := b.Receive(rctx)
	if err != nil {
		t.Fatalf("b.Receive: %v", err)
	}
	if got.MsgType != protocolpb.MsgType_MSG_TYPE_DATA {
		t.Errorf("b got %v, want DATA", got.MsgType)
	}
}

// TestMux_ReceiveCancelsOnCtx: Receive on a sub-radio returns io.EOF
// when ctx is canceled.
func TestMux_ReceiveCancelsOnCtx(t *testing.T) {
	t.Parallel()
	a, _ := serial.NewLoopbackPair()
	mux := radio.NewMux(a)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(mux.Close)
	go func() { _ = mux.Run(ctx) }()

	cancel()
	rctx, rcancel := context.WithTimeout(context.Background(), time.Second)
	defer rcancel()
	if _, err := mux.DataRadio().Receive(rctx); err == nil {
		t.Fatal("DataRadio.Receive after cancel: want error, got nil")
	}
}
