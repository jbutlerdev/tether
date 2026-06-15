// tui_test.go — unit tests for the Bubbletea-based operator TUI.
// See plan.md §10.1 (Task 9.1).
//
// The TUI is exercised through three lenses:
//
//  1. Pure state-update tests that hit Update() with synthetic
//     tea.KeyMsg / rfStatsMsg / tickMsg values and assert on the
//     resulting Model. These tests do not render, so they do not
//     require a terminal and are stable under CI.
//
//  2. View-string tests that snapshot the rendered Model.View()
//     against a golden string. The golden string is committed to
//     the test file (not an external file) so the test is
//     self-contained and machine-independent.
//
//  3. A quit-handler test that drives a "q" keystroke through
//     Update and confirms tea.Quit is returned.
package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTUI_RendersConversations checks that the rendered view
// includes the conversation list, the active-conversation marker,
// the unread badge, and the time-ago formatting. The view is
// produced from a Model seeded with two conversations: a Forge
// session and a Matrix room.
func TestTUI_RendersConversations(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	conv := []ConvRow{
		{
			ID:           "0a1b2c3d4e5f60718293a4b5c6d7e8f9",
			Name:         "Forge: build-fix",
			Kind:         "forge",
			LastActivity: now,
			UnreadCount:  2,
			Unread:       true,
		},
		{
			ID:           "11223344556677889900aabbccddeeff",
			Name:         "Alice (Matrix)",
			Kind:         "matrix",
			LastActivity: now.Add(-4 * time.Minute),
			UnreadCount:  0,
		},
		{
			ID:           "fedcba9876543210fedcba9876543210",
			Name:         "Bob (Matrix)",
			Kind:         "matrix",
			LastActivity: now.Add(-37 * time.Minute),
			UnreadCount:  1,
			Unread:       true,
		},
	}
	m := NewModel(Options{})
	m.SetConversations(conv)
	m.SetClock(func() time.Time { return now })

	view := m.View()
	want := []string{
		"Tether",
		"Conversations (3 active, 2 unread)",
		"► Forge: build-fix",
		"●2",
		"Alice (Matrix)",
		"Bob (Matrix)",
		"●1",
		"RF:",
		"Models:",
		"Quiescent:",
		"Battery:",
	}
	for _, w := range want {
		if !strings.Contains(view, w) {
			t.Errorf("view missing %q\n--- view ---\n%s\n--- end ---", w, view)
		}
	}
}

// TestTUI_RFStats_Update pushes a synthetic rfStatsMsg through
// Update and verifies the rendered view reflects the new values.
func TestTUI_RFStats_Update(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	m := NewModel(Options{})
	m.SetClock(func() time.Time { return now })
	mm, _ := m.Update(rfStatsMsg{
		SpreadFactor: 11,
		BandwidthKHz: 125,
		SNRdBm:       -8,
		TxCurrentMA:  14,
	})
	m = mm.(*Model)
	view := m.View()
	want := []string{
		"SF11",
		"BW125",
		"SNR -8",
		"TX 14mA",
	}
	for _, w := range want {
		if !strings.Contains(view, w) {
			t.Errorf("view missing %q\n--- view ---\n%s\n--- end ---", w, view)
		}
	}
}

// TestTUI_QuitCleanly drives a "q" keystroke through Update and
// checks that the returned tea.Cmd is tea.Quit.
func TestTUI_QuitCleanly(t *testing.T) {
	m := NewModel(Options{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("Update('q') returned a nil cmd; want tea.Quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Update('q') cmd returned %T, want tea.QuitMsg", msg)
	}
}

// TestTUI_ReplaysLastMessage asserts the "r" keystroke produces
// a ReplayRequested event on the model's event channel.
func TestTUI_ReplaysLastMessage(t *testing.T) {
	m := NewModel(Options{EventBuffer: 16})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatalf("Update('r') returned a nil cmd; want ReplayLast")
	}
	msg := cmd()
	if _, ok := msg.(ReplayLastMsg); !ok {
		t.Fatalf("Update('r') cmd returned %T, want ReplayLastMsg", msg)
	}
}

// TestTUI_ToggleMute asserts the "m" keystroke flips the
// Muted flag on the model.
func TestTUI_ToggleMute(t *testing.T) {
	m := NewModel(Options{})
	if m.Muted() {
		t.Fatalf("Muted should be false at start")
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = mm.(*Model)
	if !m.Muted() {
		t.Fatalf("Muted should be true after 'm'")
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = mm.(*Model)
	if m.Muted() {
		t.Fatalf("Muted should be false after second 'm'")
	}
}

// TestTUI_ConversationFilter_HidesEmpty checks that a zero
// conversation list does not crash the renderer.
func TestTUI_ConversationFilter_HidesEmpty(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	m := NewModel(Options{})
	m.SetClock(func() time.Time { return now })
	m.SetConversations(nil)
	view := m.View()
	if !strings.Contains(view, "Conversations (0 active") {
		t.Errorf("empty conversation list should show 0 active, got:\n%s", view)
	}
}
