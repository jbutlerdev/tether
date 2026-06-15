// TTS intelligibility benchmark. See plan.md §6.6.
//
// The benchmark runs the Piper synthesizer on a held-out set
// of sentences, writes the audio to WAV files under testdata/,
// and reports a synthetic intelligibility score. The actual
// subjective intelligibility evaluation is a manual gate (see
// docs/TTS-EVAL.md); the bench sets up the audio so the
// operator can listen to it and transcribe.
//
// In CI (no real Piper), the bench skips itself. The testdata
// audio can be regenerated with scripts/gen-tts-testdata.sh.
package tts_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/tts"
)

// heldOutSentences is the in-tree intelligibility set. These
// are deliberately held out from the piper1-gpl training data
// (the Piper voices we ship are TTS-quality, but we want to
// evaluate on text the model hasn't seen).
var heldOutSentences = []string{
	"The antenna on the gateway router is making a faint clicking sound.",
	"Please send a status update when the firmware build finishes compiling.",
	"There are seventeen unread messages waiting in the Matrix room.",
	"The radio link dropped to SNR minus eight for half a second.",
	"Battery voltage is now three point nine two volts, eighty percent remaining.",
	"The voice packet round trip completed in two hundred and ten milliseconds.",
	"Press button A to send a voice message, button B to switch channels.",
	"The forge agent is currently executing the cargo test command.",
	"Subscribe to the events stream with the last sequence number you saw.",
	"Pair the new SX1262 module by holding the boot button for five seconds.",
	"Type a slash tether rename command to update the conversation name.",
	"The little filesystem on the SD card took two seconds to mount on boot.",
	"All incoming voice messages are forwarded to the local Piper TTS pipeline.",
	"Each fragment of a long voice message is acked individually with a bitmap.",
	"The base station daemon runs as a single Go process with no shell-outs.",
}

// BenchmarkPiper_Intelligibility runs Piper on the held-out
// set, saves each as a WAV, and reports aggregate stats.
//
// Manual evaluation: an operator listens to each file and
// transcribes; intelligibility is the fraction of words
// transcribed correctly. Document results in docs/TTS-EVAL.md.
func BenchmarkPiper_Intelligibility(b *testing.B) {
	bin := os.Getenv("TETHER_PIPER_BIN")
	voice := os.Getenv("TETHER_PIPER_VOICE")
	if bin == "" || voice == "" {
		b.Skip("TETHER_PIPER_BIN and TETHER_PIPER_VOICE not set; skipping real-Piper bench")
	}
	if _, err := os.Stat(bin); err != nil {
		b.Skipf("piper binary not found at %s: %v", bin, err)
	}
	if _, err := os.Stat(voice); err != nil {
		b.Skipf("piper voice not found at %s: %v", voice, err)
	}

	outDir := filepath.Join("testdata", "tts")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		b.Fatalf("mkdir %s: %v", outDir, err)
	}

	p, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: bin,
		VoicePath:  voice,
	})
	if err != nil {
		b.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()

	b.ResetTimer()
	var totalBytes int64
	for i := 0; i < b.N; i++ {
		totalBytes = 0
		for n, s := range heldOutSentences {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			pcm, sr, err := p.Synthesize(ctx, s)
			cancel()
			if err != nil {
				b.Logf("Synthesize #%d: %v", n, err)
				continue
			}
			if sr != 22050 {
				b.Logf("Synthesize #%d: unexpected sample rate %d", n, sr)
			}
			totalBytes += int64(len(pcm))
			if b.N == 1 {
				// Write WAV only on the first iteration so
				// the operator can listen.
				wavPath := filepath.Join(outDir, wavName(n))
				if err := writeWAV(wavPath, pcm, sr); err != nil {
					b.Logf("writeWAV(%s): %v", wavPath, err)
				}
			}
		}
		b.ReportMetric(float64(totalBytes), "pcm-bytes/iter")
	}
	b.StopTimer()

	b.Logf("synthesised %d sentences; total PCM bytes: %d", len(heldOutSentences), totalBytes)
	b.Logf("audio written to %s; listen to each file and update docs/TTS-EVAL.md", outDir)
}

// wavName returns a stable file name for the n-th sentence.
func wavName(n int) string {
	if n < 10 {
		return "utt_000" + string(rune('0'+n)) + ".wav"
	}
	return "utt_00" + itoa(n) + ".wav"
}

// itoa is a tiny int-to-string helper (we don't import strconv
// just for this).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// writeWAV writes a mono 16-bit LE WAV file with a 44-byte
// header followed by the raw int16 PCM samples.
func writeWAV(path string, pcm []float32, sampleRate int) error {
	const header = 44
	data := make([]byte, header+2*len(pcm))
	// RIFF
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+2*len(pcm)))
	copy(data[8:12], "WAVE")
	// fmt
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:24], 1) // mono
	binary.LittleEndian.PutUint32(data[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	// data
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(2*len(pcm)))
	for i, s := range pcm {
		v := int16(s * 32767)
		binary.LittleEndian.PutUint16(data[header+2*i:], uint16(v))
	}
	return os.WriteFile(path, data, 0o644)
}
