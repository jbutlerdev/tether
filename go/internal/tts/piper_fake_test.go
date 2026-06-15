// Fake-Piper tests: run the wrapper against a tiny shell script
// that mimics Piper's line protocol. These tests verify the
// subprocess plumbing without requiring the real Piper binary.
// They run on the default (no-tag) build.
package tts_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/tts"
)

// fakePiperScript mimics Piper's line protocol. The wrapper
// invokes it as `fake-piper --model <voice.onnx> --output-raw`.
// We parse the args to find the voice path.
const fakePiperScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model)
            VOICE="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
if [ -z "$VOICE" ]; then
    echo "fake-piper: missing voice argument" >&2
    exit 2
fi
if [ ! -f "$VOICE" ]; then
    echo "fake-piper: voice file not found: $VOICE" >&2
    exit 3
fi
echo "PIPER_READY 22050 mono 16" >&2
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            text="${line#SYNTH }"
            n=$(( ${#text} * 220 ))
            bytecount=$(( n * 2 ))
            # Length line (decimal, newline-terminated).
            printf '%d\n' "$bytecount"
            # PCM payload: n int16 zero samples.
            dd if=/dev/zero bs=2 count=$n status=none
            echo "END" >&2
            ;;
        QUIT)
            exit 0
            ;;
        *)
            echo "fake-piper: unknown command: $line" >&2
            ;;
    esac
done
`

// writeFakePiper writes a fake Piper binary into dir and returns
// its path.
func writeFakePiper(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-piper")
	if err := os.WriteFile(path, []byte(fakePiperScript), 0o755); err != nil {
		t.Fatalf("write fake-piper: %v", err)
	}
	return path
}

// writeFakeVoice creates a placeholder file representing the
// .onnx model. The fake script just checks existence.
func writeFakeVoice(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-voice.onnx")
	if err := os.WriteFile(path, []byte("fake-voice"), 0o644); err != nil {
		t.Fatalf("write fake-voice: %v", err)
	}
	return path
}

// TestPiper_FakeSubprocess: a real Piper-shaped subprocess is
// driven by the wrapper and returns PCM of the right size.
func TestPiper_FakeSubprocess(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)

	p, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: bin,
		VoicePath:  voice,
	})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()

	const text = "Hello world"
	pcm, sr, err := p.Synthesize(context.Background(), text)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if sr != 22050 {
		t.Errorf("SampleRate: want 22050, got %d", sr)
	}
	want := len(text) * 220
	if len(pcm) != want {
		t.Errorf("PCM length: want %d, got %d", want, len(pcm))
	}
}

// TestPiper_FakeSubprocessStream: SynthesizeStream emits per-
// sentence PCM chunks.
func TestPiper_FakeSubprocessStream(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)

	p, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: bin,
		VoicePath:  voice,
	})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()

	in := make(chan string, 3)
	in <- "Hi."
	in <- "Hello there."
	close(in)
	out := make(chan []float32, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.SynthesizeStream(ctx, in, out); err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}
	close(out)
	var n int
	for chunk := range out {
		if len(chunk) == 0 {
			t.Errorf("chunk %d: zero length", n)
		}
		n++
	}
	if n < 2 {
		t.Errorf("stream emitted %d chunks, want >= 2", n)
	}
}

// TestPiper_FakeSubprocessMultiple: multiple Synthesize calls
// on the same wrapper reuse the live subprocess.
func TestPiper_FakeSubprocessMultiple(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)

	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()

	for i, text := range []string{"a", "bb", "ccc"} {
		pcm, _, err := p.Synthesize(context.Background(), text)
		if err != nil {
			t.Fatalf("Synthesize #%d: %v", i, err)
		}
		if len(pcm) != len(text)*220 {
			t.Errorf("Synthesize #%d: want %d samples, got %d", i, len(text)*220, len(pcm))
		}
	}
}

// TestPiper_FakeSubprocessContextCancel: cancelling the context
// mid-synth aborts promptly.
func TestPiper_FakeSubprocessContextCancel(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)

	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, _, err = p.Synthesize(ctx, "hello")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Synthesize on canceled ctx: want error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Synthesize did not abort promptly: %v", elapsed)
	}
}

// TestPiper_FakeSubprocessClose: Close terminates the subprocess.
func TestPiper_FakeSubprocessClose(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)

	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close #1: %v", err)
	}
	// Idempotent.
	if err := p.Close(); err != nil {
		t.Errorf("Close #2: %v", err)
	}
}

// fakePiperHangsScript: emits a banner, then reads a SYNTH
// command, then writes the length, then SLEEPS forever (no
// PCM, no END). Forces the wrapper to abort the read
// internally.
const fakePiperHangsScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model) VOICE="$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ -z "$VOICE" ] || [ ! -f "$VOICE" ]; then exit 3; fi
echo "PIPER_READY 22050 mono 16" >&2
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            # Emit length then sleep; the wrapper aborts and
            # we get a non-nil scanner.Err.
            echo "1000"
            sleep 60
            ;;
        QUIT) exit 0 ;;
    esac
done
`

// TestPiper_FakeSubprocessForceKill: a fake binary that hangs
// after a SYNTH causes the wrapper to force-kill it. The
// process is then reaped. Tests the close() force-kill path.
func TestPiper_FakeSubprocessForceKill(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-hangs")
	if err := os.WriteFile(bin, []byte(fakePiperHangsScript), 0o755); err != nil {
		t.Fatalf("write fake-hangs: %v", err)
	}
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	// Start a synth that will hang. Cancel after a brief moment
	// to force the wrapper to kill the subprocess.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = p.Synthesize(ctx, "hi")
	elapsed := time.Since(start)
	if err == nil {
		t.Error("Synthesize on hanging binary: want error, got nil")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Synthesize did not abort: %v", elapsed)
	}
	// Close should now force-kill. (PerSynthTimeout default
	// is 30s; we have a 2s hard limit in close().)
	start = time.Now()
	if err := p.Close(); err != nil {
		t.Logf("Close (after force-kill): %v", err)
	}
	elapsed = time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("Close took too long: %v", elapsed)
	}
}

// TestPiper_BinaryMissing: NewPiper with a missing binary errors.
func TestPiper_BinaryMissing(t *testing.T) {
	_, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: "/nonexistent/piper",
		VoicePath:  "/tmp/anything.onnx",
	})
	if err == nil {
		t.Fatal("NewPiper with missing binary: want error, got nil")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error should mention 'binary': %v", err)
	}
}

// TestPiper_BadVoice: NewPiper with a missing voice errors.
func TestPiper_BadVoice(t *testing.T) {
	_, err := tts.NewPiper(tts.PiperConfig{
		BinaryPath: "/bin/true",
		VoicePath:  "/nonexistent.onnx",
	})
	if err == nil {
		t.Fatal("NewPiper with missing voice: want error, got nil")
	}
	if !strings.Contains(err.Error(), "voice") {
		t.Errorf("error should mention 'voice': %v", err)
	}
}

// TestPiper_EmptyBinary: NewPiper with empty paths errors before
// touching the disk.
func TestPiper_EmptyBinary(t *testing.T) {
	if _, err := tts.NewPiper(tts.PiperConfig{}); err == nil {
		t.Error("NewPiper with empty config: want error, got nil")
	}
	if _, err := tts.NewPiper(tts.PiperConfig{BinaryPath: "/bin/true"}); err == nil {
		t.Error("NewPiper with empty voice: want error, got nil")
	}
}

// TestPiper_SampleRate: the wrapper reports 22050 Hz.
func TestPiper_SampleRate(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	if got := p.SampleRate(); got != 22050 {
		t.Errorf("SampleRate: want 22050, got %d", got)
	}
}

// fakePiperNoBannerScript is a variant of the fake Piper that
// does NOT emit a "PIPER_READY" banner on startup. The wrapper
// must still succeed and default to 22050 Hz.
const fakePiperNoBannerScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model)
            VOICE="$2"
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
if [ -z "$VOICE" ] || [ ! -f "$VOICE" ]; then
    exit 3
fi
# No banner. Just wait for SYNTH.
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            text="${line#SYNTH }"
            n=$(( ${#text} * 220 ))
            bytecount=$(( n * 2 ))
            printf '%d\n' "$bytecount"
            dd if=/dev/zero bs=2 count=$n status=none
            echo "END" >&2
            ;;
        QUIT)
            exit 0
            ;;
    esac
done
`

// TestPiper_NoBanner: a Piper-shaped binary that doesn't emit
// the "PIPER_READY" banner still works; the wrapper defaults
// to 22050 Hz.
func TestPiper_NoBanner(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-piper-nobanner")
	if err := os.WriteFile(bin, []byte(fakePiperNoBannerScript), 0o755); err != nil {
		t.Fatalf("write fake-piper-nobanner: %v", err)
	}
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	if got := p.SampleRate(); got != 22050 {
		t.Errorf("SampleRate (no banner): want 22050, got %d", got)
	}
	pcm, _, err := p.Synthesize(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) != 2*220 {
		t.Errorf("PCM length (no banner): want 440, got %d", len(pcm))
	}
}

// TestPiper_ShortText: a single-character text produces a small
// but non-empty PCM buffer.
func TestPiper_ShortText(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakePiper(t, dir)
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	pcm, _, err := p.Synthesize(context.Background(), "x")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) == 0 {
		t.Error("ShortText: PCM buffer is empty")
	}
}

// fakePiperBadStderrScript emits something other than "END" on
// stderr to exercise the error path.
const fakePiperBadStderrScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model) VOICE="$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ -z "$VOICE" ] || [ ! -f "$VOICE" ]; then exit 3; fi
echo "PIPER_READY 22050 mono 16" >&2
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            text="${line#SYNTH }"
            n=$(( ${#text} * 220 ))
            bytecount=$(( n * 2 ))
            printf '%d\n' "$bytecount"
            dd if=/dev/zero bs=2 count=$n status=none
            # Emit something unexpected on stderr.
            echo "I AM NOT END" >&2
            ;;
        QUIT) exit 0 ;;
    esac
done
`

// TestPiper_BadStderrLine: a fake binary that writes something
// other than "END" on stderr causes Synthesize to error.
func TestPiper_BadStderrLine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bad")
	if err := os.WriteFile(bin, []byte(fakePiperBadStderrScript), 0o755); err != nil {
		t.Fatalf("write fake-bad: %v", err)
	}
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	_, _, err = p.Synthesize(context.Background(), "hi")
	if err == nil {
		t.Fatal("Synthesize with bad stderr: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected stderr line") {
		t.Errorf("error should mention 'unexpected stderr line': %v", err)
	}
}

// fakePiperBadLengthScript writes a non-numeric length line.
const fakePiperBadLengthScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model) VOICE="$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ -z "$VOICE" ] || [ ! -f "$VOICE" ]; then exit 3; fi
echo "PIPER_READY 22050 mono 16" >&2
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            printf 'not a number\n'
            echo "END" >&2
            ;;
        QUIT) exit 0 ;;
    esac
done
`

// TestPiper_BadLengthLine: a fake binary that writes a non-
// numeric length causes Synthesize to error.
func TestPiper_BadLengthLine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bad-len")
	if err := os.WriteFile(bin, []byte(fakePiperBadLengthScript), 0o755); err != nil {
		t.Fatalf("write fake-bad-len: %v", err)
	}
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	_, _, err = p.Synthesize(context.Background(), "hi")
	if err == nil {
		t.Fatal("Synthesize with bad length: want error, got nil")
	}
	if !strings.Contains(err.Error(), "parse length") {
		t.Errorf("error should mention 'parse length': %v", err)
	}
}

// fakePiperEmptyScript: returns a 0-byte payload.
const fakePiperEmptyScript = `#!/usr/bin/env bash
VOICE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --model) VOICE="$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ -z "$VOICE" ] || [ ! -f "$VOICE" ]; then exit 3; fi
echo "PIPER_READY 22050 mono 16" >&2
while IFS= read -r line; do
    case "$line" in
        SYNTH\ *)
            printf '0\n'
            echo "END" >&2
            ;;
        QUIT) exit 0 ;;
    esac
done
`

// TestPiper_EmptySynth: a 0-byte synthesis returns a 0-length
// PCM buffer with no error.
func TestPiper_EmptySynth(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-empty")
	if err := os.WriteFile(bin, []byte(fakePiperEmptyScript), 0o755); err != nil {
		t.Fatalf("write fake-empty: %v", err)
	}
	voice := writeFakeVoice(t, dir)
	p, err := tts.NewPiper(tts.PiperConfig{BinaryPath: bin, VoicePath: voice})
	if err != nil {
		t.Fatalf("NewPiper: %v", err)
	}
	defer p.Close()
	pcm, _, err := p.Synthesize(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(pcm) != 0 {
		t.Errorf("Empty synth: want 0 samples, got %d", len(pcm))
	}
}
