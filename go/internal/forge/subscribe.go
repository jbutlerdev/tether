// subscribe.go — SSE subscription lifecycle for the forge pipeline.
//
// This file owns the per-session consumer goroutine bookkeeping:
// opening a stream, stop-and-replace on re-subscribe, ensuring the
// conv.Store row exists, and the session-expired resume path that
// swaps the underlying forge session while keeping the conversation
// id stable (so the M5's UI is undisturbed). The event pump itself
// (the goroutine that reads the events channel) lives in
// sse_consumer.go.

package forge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jbutlerdev/tether/go/internal/conv"
)

// HandleSSESubscribe opens an SSE stream on the given forge
// session and dispatches events to the per-type handlers. It
// returns immediately; the actual event pump runs on a
// dedicated goroutine. The conversation id is derived from
// sessionID via SessionToConvID16.
//
// Calling HandleSSESubscribe for a session whose conversation
// id already has a running consumer is a stop-and-replace: the
// existing consumer is signalled to stop, awaited, and a fresh
// consumer is spawned for the new subscription. This keeps the
// pipeline robust to duplicate / re-issue subscribe calls.
func (p *Pipeline) HandleSSESubscribe(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	return p.subscribe(ctx, SessionToConvID16(sessionID), sessionID)
}

// subscribe opens an SSE stream for sessionID under the given
// (stable) convID. It is the single entry point for both
// initial subscribes and session-resume re-subscribes: on
// resume the caller passes the ORIGINAL convID with the NEW
// sessionID, so the M5 sees one stable conversation while the
// underlying forge session is swapped out.
func (p *Pipeline) subscribe(ctx context.Context, convID [16]byte, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}

	// Stop-and-replace any existing consumer for this convID.
	p.consMu.Lock()
	if existing, ok := p.consumers[convID]; ok {
		p.consMu.Unlock()
		existing.stop()
		// Wait for it to actually exit so the new consumer
		// does not race the old one.
		<-existing.done
		p.consMu.Lock()
		delete(p.consumers, convID)
	}
	p.consMu.Unlock()

	// Ensure a conv.Store row exists for this convID.
	if err := p.ensureConvRow(ctx, convID, sessionID); err != nil {
		p.logger.Warn("pipeline: ensure conv row", "err", err)
	}

	events, done, closer, err := p.cfg.Forge.SubscribeEvents(ctx, sessionID, 0)
	if err != nil {
		return fmt.Errorf("forge: subscribe: %w", err)
	}

	sc := &sseConsumer{
		pipeline:  p,
		sessionID: sessionID,
		convID:    convID,
		events:    events,
		done:      done,
		closer:    closer,
		finished:  make(chan struct{}),
	}
	p.consMu.Lock()
	p.consumers[convID] = sc
	p.consMu.Unlock()
	go sc.run()
	return nil
}

// ensureConvRow inserts a conv.Store row for the given forge
// session under the (stable) convID. Idempotent: a pre-existing
// row is left in place so a session resume does not create a
// duplicate conversation.
func (p *Pipeline) ensureConvRow(ctx context.Context, convID [16]byte, sessionID string) error {
	_, err := p.cfg.Store.Get(ctx, convID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, conv.ErrNotFound) {
		return err
	}
	name := "forge:" + shortSessionID(sessionID)
	_, _, err = p.cfg.Store.Upsert(ctx, convID, conv.ConvInfo{
		Name:               name,
		Kind:               conv.KindForge,
		Target:             sessionID,
		LastActivityUnixMs: time.Now().UnixMilli(),
	})
	return err
}

// HandleSSESessionExpired resumes the session: creates a new
// forge session with the same profile and re-subscribes the
// SSE stream. The user message is NOT re-sent; the agent is
// expected to recover state from the new session id.
func (p *Pipeline) HandleSSESessionExpired(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("forge: empty session id")
	}
	newID, err := p.cfg.Forge.CreateSession(ctx, p.profile)
	if err != nil {
		return fmt.Errorf("forge: resume create session: %w", err)
	}
	// The conversation id is derived from the ORIGINAL session
	// id and stays stable across resume, so the M5's UI is
	// undisturbed. Only the store row's Target (the session id)
	// is repointed at newID; the conv_id key is unchanged.
	convID := SessionToConvID16(sessionID)
	if existing, err := p.cfg.Store.Get(ctx, convID); err == nil {
		existing.Info.Target = newID
		existing.Info.LastActivityUnixMs = time.Now().UnixMilli()
		_, _, _ = p.cfg.Store.Upsert(ctx, convID, existing.Info)
	}
	p.logger.Info("pipeline: session resumed",
		"old", shortSessionID(sessionID),
		"new", shortSessionID(newID),
	)
	// Re-subscribe on the new session id under the SAME convID.
	return p.subscribe(ctx, convID, newID)
}

// shortSessionID returns the first 8 chars of a session id
// for compact log lines. The session id is a UUID; the first
// 8 chars are the first segment.
func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
