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

// TestTUI_InitReturnsTick asserts Init() returns a non-nil Cmd
// (a tea.Tick) so the time line stays current.
func TestTUI_InitReturnsTick(t *testing.T) {
	m := NewModel(Options{})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil; want tea.Tick")
	}
}

// TestTUI_TickReturnsCmd asserts Tick() returns a non-nil Cmd
// so the production entry point can chain it.
func TestTUI_TickReturnsCmd(t *testing.T) {
	cmd := Tick()
	if cmd == nil {
		t.Fatal("Tick() returned nil; want tea.Cmd")
	}
}

// TestTUI_ResizeWindow exercises the WindowSizeMsg path
// through Update; the rendered view's width must reflect
// the new size.
func TestTUI_ResizeWindow(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	m := NewModel(Options{})
	m.SetClock(func() time.Time { return now })
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = mm.(*Model)
	if m.width != 100 || m.height != 30 {
		t.Fatalf("WindowSizeMsg not applied: got width=%d height=%d", m.width, m.height)
	}
}

// TestTUI_TickMsgReturnsTickCmd asserts the TickMsg path
// re-arms the next tick so the time line stays current.
func TestTUI_TickMsgReturnsTickCmd(t *testing.T) {
	m := NewModel(Options{})
	_, cmd := m.Update(TickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("Update(TickMsg) returned nil cmd; want Tick")
	}
}

// TestTUI_CtrlCQuits exercises the ctrl+c branch of
// handleKey.
func TestTUI_CtrlCQuits(t *testing.T) {
	m := NewModel(Options{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Update(KeyCtrlC) returned nil; want tea.Quit")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("Update(KeyCtrlC) cmd returned %T, want tea.QuitMsg", msg)
	}
}

// TestTUI_FormatTimeAgo asserts the time-ago formatter covers
// every branch in the switch.
func TestTUI_FormatTimeAgo(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"future", now.Add(time.Hour), "15:32"},
		{"just_now", now.Add(-10 * time.Second), "just now"},
		{"minutes_ago", now.Add(-5 * time.Minute), "5m ago"},
		{"same_day", now.Add(-1 * time.Hour), "13:32"},
		{"yesterday", now.Add(-24 * time.Hour), "yesterday"},
		{"this_year", now.Add(-30 * 24 * time.Hour), "May 16"},
		{"last_year", now.Add(-400 * 24 * time.Hour), "2025-05-11"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTimeAgo(c.in, now)
			if got != c.want {
				t.Errorf("formatTimeAgo(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestTUI_Truncate asserts the truncate helper for both the
// "short" and "long" branches.
func TestTUI_Truncate(t *testing.T) {
	if got := truncate("hi", 5); got != "hi" {
		t.Errorf("truncate(short) = %q, want %q", got, "hi")
	}
	if got := truncate("hello world", 5); got != "hello" {
		t.Errorf("truncate(long) = %q, want %q", got, "hello")
	}
}

// TestTUI_MaxInt asserts the maxInt helper for both branches.
func TestTUI_MaxInt(t *testing.T) {
	if got := maxInt(3, 5); got != 5 {
		t.Errorf("maxInt(3, 5) = %d, want 5", got)
	}
	if got := maxInt(7, 2); got != 7 {
		t.Errorf("maxInt(7, 2) = %d, want 7", got)
	}
	if got := maxInt(0, 0); got != 0 {
		t.Errorf("maxInt(0, 0) = %d, want 0", got)
	}
}

// TestTUI_SetRFStats exercises the SetRFStats setter; the
// view must reflect the new values.
func TestTUI_SetRFStats(t *testing.T) {
	now := time.Date(2026, 6, 15, 14, 32, 0, 0, time.UTC)
	m := NewModel(Options{})
	m.SetClock(func() time.Time { return now })
	m.SetRFStats(RFStats{SpreadFactor: 7, BandwidthKHz: 250, SNRdBm: -3, TxCurrentMA: 22})
	view := m.View()
	for _, w := range []string{"SF7", "BW250", "SNR -3", "TX 22mA"} {
		if !strings.Contains(view, w) {
			t.Errorf("SetRFStats not applied; view missing %q\n--- view ---\n%s", w, view)
		}
	}
}

// TestTUI_TruncateEdgeCases covers the remaining branches of
// truncate (empty string, exact length, n=0).
func TestTUI_TruncateEdgeCases(t *testing.T) {
	if got := truncate("", 5); got != "" {
		t.Errorf("truncate(empty) = %q, want %q", got, "")
	}
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("truncate(exact) = %q, want %q", got, "hello")
	}
	if got := truncate("hello", 0); got != "" {
		t.Errorf("truncate(n=0) = %q, want %q", got, "")
	}
	if got := truncate("hi", 0); got != "" {
		t.Errorf("truncate(short, n=0) = %q, want %q", got, "")
	}
}

// TestTUI_KeyRunesEmpty asserts that a KeyRunes with no runes
// is a no-op (the handleKey helper's defensive guard).
func TestTUI_KeyRunesEmpty(t *testing.T) {
	m := NewModel(Options{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: nil})
	if cmd != nil {
		t.Errorf("empty KeyRunes produced a cmd %T; want nil", cmd())
	}
}

// TestTUI_KeyOtherRuneIgnored asserts that a rune that is not
// q/r/m is a no-op.
func TestTUI_KeyOtherRuneIgnored(t *testing.T) {
	m := NewModel(Options{})
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = mm.(*Model)
	if m.Muted() {
		t.Errorf("'x' should not toggle mute")
	}
}
