// Package audio defines the audio-sink interface and concrete
// implementations. The Tether base station uses audio sinks to play
// back TTS audio and to record microphone audio for tests.
//
// See plan.md §6.8.
package audio

// Sink is the abstract audio destination. Implementations take
// mono 16-bit PCM at the sink's native sample rate. The Tether
// base station uses a PulseAudio sink in production (Linux), a
// VB-Cable sink on Windows, and a file/in-memory sink for tests
// and the "save to disk" mode.
type Sink interface {
	// Write pushes a chunk of mono 16-bit PCM at the sink's
	// sample rate. Implementations may block.
	Write(pcm []int16) error

	// SampleRate returns the sink's native sample rate in Hz.
	SampleRate() int

	// Channels returns the channel count. The Tether base station
	// uses 1 (mono) for the M5 link; this is always 1 for v1.
	Channels() int

	// Close releases any resources held by the sink. Idempotent.
	Close() error
}
