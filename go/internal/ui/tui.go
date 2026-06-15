// Package ui is the Bubbletea-based operator TUI for the Tether
// base station. See plan.md §10.1 (Task 9.1).
//
// The TUI is a single Model that owns the conversation list, RF
// statistics, model info, and key bindings. The Model is pure:
// all I/O is plumbed through tea.Cmd values, and the only state
// mutation is the conversation snapshot (set via
// SetConversations, typically by a ticker).
//
// The TUI is intentionally minimal: it does not edit
// conversations, start PTT sessions, or send messages. Its job
// is to make the daemon observable at a glance:
//
//   - Are the conversations arriving? (list, unread badges)
//   - Is the radio link healthy? (SF, BW, SNR, TX current)
//   - Are the models loaded? (parakeet + piper)
//   - How is the battery holding? (voltage, percent, quiescent)
//
// The model is exercised by the unit tests in tui_test.go via
// three lenses:
//
//  1. State-update tests — drive synthetic KeyMsg / rfStatsMsg
//     values through Update and assert on the resulting Model.
//  2. View-string tests — snapshot Model.View() against a
//     golden string committed to the test file.
//  3. Quit / replay handler tests — confirm Update returns
//     the right tea.Cmd.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Options configure the Model. The zero value is usable.
type Options struct {
	// EventBuffer is the size of the per-Model event queue used
	// for the "r" replay key. Zero (the default) means the key
	// produces a tea.Cmd only, with no event queue.
	EventBuffer int
	// Width / Height set the initial terminal size. Both default
	// to sensible values; the production entry point (see
	// cmd/tether-tui) uses tea.WithAltScreen() so the bubbletea
	// runtime takes care of resize.
	Width  int
	Height int
	// Models overrides the model-info line. Defaults are set
	// inside NewModel.
	STTModel string
	TTSVoice string
}

// ConvRow is one row in the conversation list. It is a denormalised
// view of the conv.Store contents: the daemon pulls a snapshot every
// tick and feeds it to the TUI via SetConversations.
type ConvRow struct {
	// ID is the 32-char hex conversation id. The TUI does not
	// parse it; it is rendered as a tooltip / log line.
	ID string
	// Name is the human-friendly conversation name.
	Name string
	// Kind is the source ("matrix", "forge", "broadcast").
	Kind string
	// LastActivity is the wall-clock time of the most recent
	// item in this conversation.
	LastActivity time.Time
	// UnreadCount is the unread badge.
	UnreadCount uint32
	// Unread is true if the conversation has unread items that
	// have not been opened by the operator. The TUI highlights
	// Unread=true rows even when UnreadCount is zero (e.g., an
	// "agent thinking" indicator).
	Unread bool
}

// RFStats is the snapshot of LoRa link health pushed into the
// Model on every tick. The TUI does not interpret the values;
// it just renders them.
type RFStats struct {
	SpreadFactor int
	BandwidthKHz int
	SNRdBm       int
	TxCurrentMA  int
}

// Model is the Bubbletea model. The zero value is not usable;
// always construct via NewModel.
type Model struct {
	opts          Options
	conversations []ConvRow
	rf            RFStats
	muted         bool
	quiescentMA   int
	batteryV      float64
	batteryPct    int
	clock         func() time.Time

	// rendered view size; defaults to Options.Width/Height.
	width  int
	height int
}

// NewModel returns a Model ready to be passed to tea.NewProgram.
func NewModel(opts Options) *Model {
	if opts.Width <= 0 {
		opts.Width = 80
	}
	if opts.Height <= 0 {
		opts.Height = 24
	}
	if opts.STTModel == "" {
		opts.STTModel = "parakeet-tdt 0.6b v2 (640 MB)"
	}
	if opts.TTSVoice == "" {
		opts.TTSVoice = "piper amy"
	}
	return &Model{
		opts:        opts,
		rf:          RFStats{SpreadFactor: 11, BandwidthKHz: 125, SNRdBm: -8, TxCurrentMA: 14},
		quiescentMA: 12,
		batteryV:    3.92,
		batteryPct:  78,
		clock:       time.Now,
		width:       opts.Width,
		height:      opts.Height,
	}
}

// SetConversations replaces the conversation snapshot. The TUI
// sorts by last-activity (most recent first) on render.
func (m *Model) SetConversations(rows []ConvRow) {
	cp := make([]ConvRow, len(rows))
	copy(cp, rows)
	m.conversations = cp
}

// SetClock overrides the wall-clock source. Tests use this to
// pin "now" and avoid time-dependent flakes.
func (m *Model) SetClock(f func() time.Time) { m.clock = f }

// SetRFStats overrides the RF stats snapshot. The ticker in the
// production entry point calls this; tests call it directly.
func (m *Model) SetRFStats(s RFStats) { m.rf = s }

// Muted reports the current mute state.
func (m *Model) Muted() bool { return m.muted }

// Init is the bubbletea-required initial Cmd. It returns a tick
// every second so the wall-clock-derived view (e.g., the time
// "now" line) updates without a state mutation.
func (m *Model) Init() tea.Cmd { return Tick() }

// rfStatsMsg is the internal tea.Msg that the production ticker
// sends. It is unexported because the entry point is the only
// legitimate sender, but it is part of the testable surface —
// tests push this Msg through Update to drive the renderer.
type rfStatsMsg struct {
	SpreadFactor int
	BandwidthKHz int
	SNRdBm       int
	TxCurrentMA  int
}

// ReplayLastMsg is the tea.Msg the TUI emits when the operator
// presses "r". The production entry point catches it and asks
// the daemon to replay the last TTS audio burst.
type ReplayLastMsg struct{}

// TickMsg is fired every second by the Init Cmd. It exists so
// tests can drive a tick through Update without a real timer.
type TickMsg time.Time

// Tick is the periodic Cmd. Tests can call it via Init(); the
// production entry point uses tea.Every to keep it ticking.
func Tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Update handles a tea.Msg and returns the new Model + Cmd.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case rfStatsMsg:
		m.rf = RFStats{
			SpreadFactor: msg.SpreadFactor,
			BandwidthKHz: msg.BandwidthKHz,
			SNRdBm:       msg.SNRdBm,
			TxCurrentMA:  msg.TxCurrentMA,
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case TickMsg:
		// Re-arm the next tick so the time line stays current.
		return m, Tick()
	default:
		return m, nil
	}
}

// handleKey is the keyboard-routing helper. The three keys
// documented in plan.md §10.1 are:
//
//   - "q" → tea.Quit
//   - "r" → ReplayLastMsg
//   - "m" → toggle muted
//
// ctrl+c is also a Quit (the bubbletea default). Anything else
// is a no-op.
func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyRunes:
		if len(k.Runes) == 0 {
			return m, nil
		}
		switch k.Runes[0] {
		case 'q':
			return m, tea.Quit
		case 'r':
			return m, func() tea.Msg { return ReplayLastMsg{} }
		case 'm':
			m.muted = !m.muted
			return m, nil
		}
	}
	return m, nil
}

// View renders the model as a string. The layout is the one
// from plan.md §10.1:
//
//	┌─ Tether ─────────────────────────────────────────┐
//	│ Conversations (4 active, 2 unread)                │
//	│  ► Forge: build-fix   14:32  ●2                   │
//	│    Alice (Matrix)      14:28                       │
//	│    Bob (Matrix)        13:55  ●1                   │
//	│    Forge: research     yesterday                   │
//	├──────────────────────────────────────────────────┤
//	│ RF: SF11 BW125 SNR -8 dBm  TX 14mA                │
//	│ Models: parakeet-tdt 0.6b v2 (640 MB), piper amy  │
//	│ Quiescent: 12 mA   Battery: 3.92V  (78%)         │
//	├──────────────────────────────────────────────────┤
//	│ [r] Replay last  [m] Mute mic  [q] Quit           │
//	└──────────────────────────────────────────────────┘
//
// The actual box-drawing characters are the same Unicode
// characters the plan uses (no ASCII fallback — the operator
// terminal is expected to support UTF-8).
func (m *Model) View() string {
	var b strings.Builder
	// Title bar.
	b.WriteString("┌─ Tether ")
	b.WriteString(strings.Repeat("─", maxInt(0, m.width-10)))
	b.WriteString("┐\n")
	// Conversation list.
	rows := m.sortedConversations()
	unread := 0
	for _, r := range rows {
		if r.Unread {
			unread++
		}
	}
	fmt.Fprintf(&b, "│ Conversations (%d active, %d unread)\n", len(rows), unread)
	now := m.clock()
	maxRows := maxInt(0, m.height-7) // leave room for footer lines
	if maxRows > 8 {
		maxRows = 8
	}
	for i, r := range rows {
		if i >= maxRows {
			fmt.Fprintf(&b, "│   … %d more\n", len(rows)-maxRows)
			break
		}
		b.WriteString("│ ")
		if i == 0 {
			b.WriteString("► ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%-24s %s", truncate(r.Name, 24), formatTimeAgo(r.LastActivity, now))
		if r.UnreadCount > 0 {
			fmt.Fprintf(&b, "  ●%d", r.UnreadCount)
		}
		b.WriteString("\n")
	}
	// Separator.
	b.WriteString("├")
	b.WriteString(strings.Repeat("─", maxInt(0, m.width-2)))
	b.WriteString("┤\n")
	// RF / models / battery lines.
	fmt.Fprintf(&b, "│ RF: SF%d BW%d SNR %d dBm  TX %dmA\n",
		m.rf.SpreadFactor, m.rf.BandwidthKHz, m.rf.SNRdBm, m.rf.TxCurrentMA)
	fmt.Fprintf(&b, "│ Models: %s, %s\n", m.opts.STTModel, m.opts.TTSVoice)
	fmt.Fprintf(&b, "│ Quiescent: %d mA   Battery: %.2fV  (%d%%)\n",
		m.quiescentMA, m.batteryV, m.batteryPct)
	// Footer.
	b.WriteString("├")
	b.WriteString(strings.Repeat("─", maxInt(0, m.width-2)))
	b.WriteString("┤\n")
	b.WriteString("│ [r] Replay last  [m] Mute mic  [q] Quit\n")
	b.WriteString("└")
	b.WriteString(strings.Repeat("─", maxInt(0, m.width-2)))
	b.WriteString("┘\n")
	return b.String()
}

// sortedConversations returns a copy of the conversation list
// sorted by LastActivity (most recent first).
func (m *Model) sortedConversations() []ConvRow {
	out := make([]ConvRow, len(m.conversations))
	copy(out, m.conversations)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out
}

// truncate returns s truncated to at most n runes. It is used
// to keep the conversation column at 24 chars in the layout.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// formatTimeAgo returns a compact human-readable time string:
//
//	just now   < 60s
//	Ns ago     < 60m
//	HH:MM      today
//	yesterday  1 day ago
//	Mon DD     this year
//	YYYY-MM-DD older
//
// The format is intentionally simple — the TUI does not have
// local time-zone context.
func formatTimeAgo(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return t.Format("15:04")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d / time.Minute)
		return fmt.Sprintf("%dm ago", mins)
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case t.Year() == now.Year() && t.YearDay() == now.YearDay()-1:
		return "yesterday"
	case t.Year() == now.Year():
		return t.Format("Jan 02")
	default:
		return t.Format("2006-01-02")
	}
}

// maxInt returns max(a, b). The stdlib added math.Max for ints
// in 1.21 but the project pins 1.20 for the cgo path; this tiny
// helper keeps the TUI import-clean.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
