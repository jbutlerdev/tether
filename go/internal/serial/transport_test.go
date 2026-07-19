package serial

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

type pipePort struct {
	conn net.Conn
}

func (p *pipePort) Read(b []byte) (int, error)  { return p.conn.Read(b) }
func (p *pipePort) Write(b []byte) (int, error) { return p.conn.Write(b) }
func (p *pipePort) Close() error                { return p.conn.Close() }

// newPipePair returns two connected Ports (like a serial loopback).
func newPipePair() (Port, Port) {
	a, b := net.Pipe()
	return &pipePort{a}, &pipePort{b}
}

func TestTransportSendReceive(t *testing.T) {
	// The Go side (transport) and the "bridge" side (raw port).
	goPort, bridgePort := newPipePair()
	defer goPort.Close()
	defer bridgePort.Close()

	// net.Pipe is synchronous — writes block until the other side
	// reads. Start a background reader that collects frames from the
	// bridge side so the transport's writes don't deadlock.
	bridgeDec := NewFrameDecoder()
	var bridgeMu sync.Mutex
	var bridgeFrames []Frame
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		buf := make([]byte, 512)
		for {
			n, err := bridgePort.Read(buf)
			if n > 0 {
				bridgeMu.Lock()
				bridgeDec.Feed(buf[:n])
				for {
					f, ok := bridgeDec.Next()
					if !ok {
						break
					}
					bridgeFrames = append(bridgeFrames, f)
				}
				bridgeMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	tr := NewTransport(TransportConfig{Port: goPort})
	if err := tr.Init(context.Background(), radio.Preset{
		SpreadingFactor: 11,
		BandwidthHz:     125000,
		CodingRate:      8,
		TxPowerDbm:      20,
		SyncWord:        0xF3,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Send an envelope through the transport.
	env := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_DATA,
		TargetId:        &protocolpb.NodeId{Value: 0xFFFF},
		SenderId:        &protocolpb.NodeId{Value: 0x0001},
		ConversationId:  make([]byte, 16),
		MessageId:       42,
		SeqNum:          0,
		TotalSeqs:       1,
		Payload:         []byte("hello over loRa"),
	}
	if err := tr.Send(context.Background(), env); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for the bridge reader to collect the two frames
	// (kSetConfig from Init + kAck from Send).
	waitFrames := func(want int) []Frame {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			bridgeMu.Lock()
			if len(bridgeFrames) >= want {
				fs := append([]Frame(nil), bridgeFrames...)
				bridgeMu.Unlock()
				return fs
			}
			bridgeMu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d frames (got %d)", want, len(bridgeFrames))
		return nil
	}
	frames := waitFrames(2)

	// First frame: kSetConfig (from Init).
	if frames[0].Type != FrameSetConfig {
		t.Errorf("first frame type: got %d, want %d (kSetConfig)", frames[0].Type, FrameSetConfig)
	}
	// Second frame: kAck (from Send) — payload is the encoded envelope.
	if frames[1].Type != FrameAck {
		t.Errorf("second frame type: got %d, want %d (kAck)", frames[1].Type, FrameAck)
	}
	got, err := protocol.Decode(frames[1].Payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.MessageId != 42 || string(got.Payload) != "hello over loRa" {
		t.Errorf("decoded env: msg_id=%d payload=%q", got.MessageId, string(got.Payload))
	}

	// Now simulate the bridge receiving a LoRa packet: send a
	// kRxPacket frame to the transport.
	rxEnv := &protocolpb.Envelope{
		ProtocolVersion: 1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_ACK,
		TargetId:        &protocolpb.NodeId{Value: 0x0001},
		SenderId:        &protocolpb.NodeId{Value: 0x0002},
		ConversationId:  make([]byte, 16),
		MessageId:       99,
		Payload:         make([]byte, 28),
	}
	rxEncoded, err := protocol.Encode(rxEnv)
	if err != nil {
		t.Fatalf("protocol.Encode: %v", err)
	}
	rxFrame, _ := EncodeFrame(Frame{Type: FrameRxPacket, Payload: rxEncoded})
	if _, err := bridgePort.Write(rxFrame); err != nil {
		t.Fatalf("bridge write: %v", err)
	}

	// The transport's Receive should produce the envelope.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got2, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got2.MessageId != 99 {
		t.Errorf("received env msg_id: got %d, want 99", got2.MessageId)
	}
}

func TestTransportLogHandler(t *testing.T) {
	goPort, bridgePort := newPipePair()
	defer goPort.Close()
	defer bridgePort.Close()

	// Drain the bridge side so the transport's Init write doesn't block.
	go func() {
		buf := make([]byte, 512)
		for {
			if _, err := bridgePort.Read(buf); err != nil {
				return
			}
		}
	}()

	var mu sync.Mutex
	var logs []string
	tr := NewTransport(TransportConfig{
		Port: goPort,
		LogHandler: func(line string) {
			mu.Lock()
			logs = append(logs, line)
			mu.Unlock()
		},
	})
	if err := tr.Init(context.Background(), radio.Preset{
		SpreadingFactor: 11, BandwidthHz: 125000, CodingRate: 8, TxPowerDbm: 20, SyncWord: 0xF3,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Send a kLog frame from the bridge.
	logFrame, _ := EncodeFrame(Frame{Type: FrameLog, Payload: []byte("bridge: LoRa ready")})
	bridgePort.Write(logFrame)

	// Wait for the handler to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if len(logs) > 0 {
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("no log received")
	}
	if logs[0] != "bridge: LoRa ready" {
		t.Errorf("log: got %q, want %q", logs[0], "bridge: LoRa ready")
	}
}

func TestTransportCloseUnblocksReceive(t *testing.T) {
	goPort, bridgePort := newPipePair()
	defer bridgePort.Close()
	// Drain so Init's write doesn't block.
	go func() {
		buf := make([]byte, 512)
		for {
			if _, err := bridgePort.Read(buf); err != nil {
				return
			}
		}
	}()
	tr := NewTransport(TransportConfig{Port: goPort})
	if err := tr.Init(context.Background(), radio.Preset{
		SpreadingFactor: 11, BandwidthHz: 125000, CodingRate: 8, TxPowerDbm: 20, SyncWord: 0xF3,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Receive in a goroutine; it should unblock when we Close.
	done := make(chan error, 1)
	go func() {
		_, err := tr.Receive(context.Background())
		done <- err
	}()

	time.Sleep(50 * time.Millisecond) // let Receive block
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if err != io.EOF {
			t.Errorf("Receive after close: got %v, want io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not unblock after Close")
	}
}

func TestTransportReceiveContextCancel(t *testing.T) {
	goPort, bridgePort := newPipePair()
	defer goPort.Close()
	defer bridgePort.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			if _, err := bridgePort.Read(buf); err != nil {
				return
			}
		}
	}()
	tr := NewTransport(TransportConfig{Port: goPort})
	if err := tr.Init(context.Background(), radio.Preset{
		SpreadingFactor: 11, BandwidthHz: 125000, CodingRate: 8, TxPowerDbm: 20, SyncWord: 0xF3,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tr.Receive(ctx)
	if err != io.EOF {
		t.Errorf("Receive on canceled ctx: got %v, want io.EOF", err)
	}
}

func TestEncodeSetConfigPayload(t *testing.T) {
	payload, err := encodeSetConfigPayload(radio.Preset{
		SpreadingFactor: 11,
		BandwidthHz:     125000,
		CodingRate:      8,
		TxPowerDbm:      20,
		SyncWord:        0xF3,
	})
	if err != nil {
		t.Fatalf("encodeSetConfigPayload: %v", err)
	}
	want := []byte{11, 0, 8, 20, 0xF3}
	if len(payload) != len(want) {
		t.Fatalf("payload len: got %d, want %d", len(payload), len(want))
	}
	for i, b := range want {
		if payload[i] != b {
			t.Errorf("byte %d: got %d, want %d", i, payload[i], b)
		}
	}
}
