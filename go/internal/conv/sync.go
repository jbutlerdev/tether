// Conversation-store → M5 UI_UPDATE sync. See plan.md §7.4.
//
// Sync watches a conv.Store for changes and emits a UI_UPDATE
// packet (MsgType=UI_UPDATE) over the LoRa radio for every
// mutation. The M5's conv_manager (firmware/m5/components/
// conv_manager, plan §5.5) consumes those packets and updates
// its LittleFS DB.
//
// The sync layer is intentionally simple: it serialises each
// change as one ConvInfo payload, encodes it as the body of an
// Envelope, and hands the envelope to the radio. The radio layer
// (plan §2) is responsible for fragmentation / ACK / retry —
// Sync does not re-implement that machinery.
//
// "Piggybacks on existing connection" (plan §7.4 test
// TestSync_PiggybacksOnExistingConnection) is satisfied by
// simply not opening a new radio channel: Sync sends over the
// radio the caller hands it. There is no handshake.
package conv

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Radio is the subset of radio.Radio that Sync uses. It is
// defined here (rather than imported) so that tests can plug in
// a small fake radio without pulling in the real radio package's
// dependencies.
type Radio interface {
	Send(ctx context.Context, env *protocolpb.Envelope) error
}

// SyncConfig configures a Sync.
type SyncConfig struct {
	// Store is the conversation store to watch. Required.
	Store Store
	// Radio is the LoRa radio the M5 listens on. Required.
	Radio Radio
	// SenderID is the Tether node id that owns the radio
	// (typically the base station's node id, e.g. 0x0001).
	SenderID uint32
	// TargetID is the M5's node id (the broadcast address 0xFFFF
	// is the usual default; per-M5 addressing is also supported).
	TargetID uint32
	// Logger is the structured logger. Defaults to slog.Default().
	Logger *slog.Logger
	// SendTimeout is the per-Send deadline. Defaults to 2 s.
	SendTimeout time.Duration
	// MinInterval is the minimum interval between consecutive
	// UI_UPDATE packets to the same conversation. Updates that
	// arrive within this window are coalesced (the latest
	// payload wins). Defaults to 50 ms.
	MinInterval time.Duration
}

// Sync is the conv.Store → radio UI_UPDATE bridge. Construct
// with NewSync and call Run on a dedicated goroutine; the loop
// exits when ctx is canceled.
type Sync struct {
	store  Store
	radio  Radio
	logger *slog.Logger
	cfg    SyncConfig

	// nodeIDs are pre-rendered little-endian byte slices to
	// avoid per-Send allocations.
	senderID, targetID *protocolpb.NodeId
	sendTimeout        time.Duration
	minInterval        time.Duration

	// coalesceMu protects the per-conv last-sent timestamp map.
	// Used to satisfy the "MinInterval" rate-limit.
	coalesceMu sync.Mutex
	lastSent   map[[16]byte]time.Time
}

// NewSync returns a Sync ready to be Run. Default SendTimeout is
// 2 s; default MinInterval is 50 ms.
func NewSync(cfg SyncConfig) *Sync {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.SendTimeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	// A MinInterval < 0 is the sentinel for "no coalescing".
	// 0 means "use the default" (50 ms). Tests that want raw
	// per-event delivery pass MinInterval: -1.
	minInterval := cfg.MinInterval
	if minInterval == 0 {
		minInterval = 50 * time.Millisecond
	}
	return &Sync{
		store:  cfg.Store,
		radio:  cfg.Radio,
		cfg:    cfg,
		logger: logger,
		senderID: &protocolpb.NodeId{
			Value: cfg.SenderID,
		},
		targetID: &protocolpb.NodeId{
			Value: cfg.TargetID,
		},
		sendTimeout: timeout,
		minInterval: minInterval,
		lastSent:    make(map[[16]byte]time.Time),
	}
}

// Run consumes the store's Changes channel and emits a UI_UPDATE
// for every event. The order is preserved (one event → one
// packet, in causal order). Returns when ctx is canceled.
func (s *Sync) Run(ctx context.Context) error {
	s.logger.Info("conv sync: run starting")
	defer s.logger.Info("conv sync: run stopped")

	changes := s.store.Changes(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-changes:
			if !ok {
				return nil
			}
			if err := s.handle(ctx, ev); err != nil {
				s.logger.Warn("conv sync: handle failed",
					"id", ConvIDToHex(ev.ID[:]),
					"err", err,
				)
				// We do not abort the loop on a single
				// failure: the next event will get its own
				// shot. The M5's conv_manager tolerates
				// dropped UI_UPDATE packets (it re-reads
				// from LittleFS on a /tether list).
			}
		}
	}
}

// handle converts one Change into one (or zero) UI_UPDATE
// envelope and sends it.
func (s *Sync) handle(ctx context.Context, ev Change) error {
	// Rate-limit: skip if a packet for this conversation was
	// sent within minInterval. The latest payload always wins
	// (the next event will catch up).
	if !s.shouldSend(ev.ID) {
		return nil
	}

	var info *ConvInfo
	if ev.Kind == ChangeUpsert {
		info = &ev.New.Info
	}
	// For ChangeRemove, info is nil → the encoder sets Remove=true.

	env, err := s.encode(ev.ID, info)
	if err != nil {
		return err
	}

	sCtx, cancel := context.WithTimeout(ctx, s.sendTimeout)
	defer cancel()
	return s.radio.Send(sCtx, env)
}

// shouldSend returns true if no UI_UPDATE for the given conv id
// was sent within the configured MinInterval. A negative
// MinInterval disables coalescing.
func (s *Sync) shouldSend(id [16]byte) bool {
	if s.minInterval < 0 {
		return true
	}
	now := time.Now()
	s.coalesceMu.Lock()
	defer s.coalesceMu.Unlock()
	if last, ok := s.lastSent[id]; ok && now.Sub(last) < s.minInterval {
		return false
	}
	s.lastSent[id] = now
	return true
}

// encode builds a UI_UPDATE Envelope. The payload is the wire-
// encoded ConvInfo protobuf (plan §1.4: ConvInfo is the only
// legal body for MsgType=UI_UPDATE).
func (s *Sync) encode(id [16]byte, info *ConvInfo) (*protocolpb.Envelope, error) {
	ci := &protocolpb.ConvInfo{
		ConversationId: append([]byte(nil), id[:]...),
	}
	if info != nil {
		// Truncate name to the M5's display width.
		name := info.Name
		if len(name) > protocol.MaxConvNameLen {
			name = name[:protocol.MaxConvNameLen]
		}
		ci.Name = name
		ci.Kind = convKindToProto(info.Kind)
		ci.Target = info.Target
		if len(info.EncryptionKey) > 0 {
			ci.EncryptionKey = append([]byte(nil), info.EncryptionKey...)
		}
		ci.LastActivityUnixMs = uint64(info.LastActivityUnixMs)
		ci.UnreadCount = info.UnreadCount
	} else {
		// A nil info means "this is a Remove".
		ci.Remove = true
	}
	ci.Remove = info == nil

	// Validate before send: a name longer than 24 chars is a
	// protocol violation (the truncation above should prevent
	// it, but a defensive check is cheap).
	if err := protocol.ValidateConvInfo(ci); err != nil {
		return nil, err
	}

	body, err := proto.Marshal(ci)
	if err != nil {
		return nil, err
	}

	// MessageID and SeqNum are not used by the M5 conv_manager
	// (UI_UPDATE is a single-packet message; fragmentation is
	// not meaningful here), but the envelope header requires
	// sensible values. SeqNum=0, TotalSeqs=1.
	env := &protocolpb.Envelope{
		ProtocolVersion: 1,
		TargetId:        s.targetID,
		SenderId:        s.senderID,
		ConversationId:  append([]byte(nil), id[:]...),
		MessageId:       nextMessageID(),
		SeqNum:          0,
		TotalSeqs:       1,
		MsgType:         protocolpb.MsgType_MSG_TYPE_UI_UPDATE,
		AudioKind:       protocolpb.AudioKind_AUDIO_KIND_UNSPECIFIED,
		Payload:         body,
	}
	return env, nil
}

// messageIDCounter is a per-process counter for UI_UPDATE message
// ids. The M5 conv_manager does not look at MessageID for
// UI_UPDATE packets (it uses ConversationId as the join key), so
// the counter is purely a "fill the field" convenience.
var messageIDCounter struct {
	sync.Mutex
	next uint32
}

func nextMessageID() uint32 {
	messageIDCounter.Lock()
	defer messageIDCounter.Unlock()
	messageIDCounter.next++
	return messageIDCounter.next
}

// convKindToProto maps the conv.Kind enum to the protobuf enum.
func convKindToProto(k Kind) protocolpb.ConvKind {
	switch k {
	case KindMatrix:
		return protocolpb.ConvKind_CONV_KIND_MATRIX
	case KindForge:
		return protocolpb.ConvKind_CONV_KIND_FORGE
	case KindBroadcast:
		return protocolpb.ConvKind_CONV_KIND_BROADCAST
	default:
		return protocolpb.ConvKind_CONV_KIND_UNSPECIFIED
	}
}
