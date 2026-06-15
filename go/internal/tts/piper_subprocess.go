// Piper subprocess implementation. Always compiled; the build
// tag distinction is in piper_stub.go which replaces this when
// the `piper` tag is not set.

package tts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// newPiperBackend is the real subprocess constructor. It is
// overridden in piper_stub.go on builds without the `piper`
// build tag.
func newPiperBackend(cfg PiperConfig) (piperBackend, error) {
	return newPiperProc(cfg)
}

// piperProc is the live state of the subprocess.
type piperProc struct {
	cfg     PiperConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderrR io.ReadCloser

	// stderrLines delivers one line per stderr entry. The
	// banner reader and the per-synth "END" reader both
	// consume from this channel, so a single bufio.Reader is
	// not shared across goroutines. The drain goroutine
	// (started by newPiperProc) is the only reader of p.stderrR.
	stderrLines chan string
	stderrErr   error

	// mu serialises SYNTH/END round-trips and protects the
	// closed field. Piper is a single-threaded text-in, audio-
	// out engine.
	mu sync.Mutex

	// ready is closed once the PIPER_READY banner has been read.
	ready chan struct{}
	// readyErr is the error from reading the banner, if any.
	readyErr error

	// sampleRate is the rate reported by the binary.
	sampleRate int

	// closed marks the subprocess as terminated. Set by close()
	// under mu. Idempotent close() relies on this.
	closed bool
}

// Compile-time check.
var _ piperBackend = (*piperProc)(nil)

// newPiperProc starts the binary and waits for the banner.
func newPiperProc(cfg PiperConfig) (*piperProc, error) {
	startup := cfg.StartupTimeout
	if startup == 0 {
		startup = 5 * time.Second
	}
	perSynth := cfg.PerSynthTimeout
	if perSynth == 0 {
		perSynth = 30 * time.Second
	}

	args := []string{"--model", cfg.VoicePath, "--output-raw"}
	if cfg.UseGPU {
		args = append(args, "--use-cuda")
	}
	args = append(args, cfg.ExtraArgs...)

	cmd := exec.Command(cfg.BinaryPath, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("tts: piper: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("tts: piper: stdout pipe: %w", err)
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("tts: piper: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderrR.Close()
		return nil, fmt.Errorf("tts: piper: start: %w", err)
	}
	p := &piperProc{
		cfg:         cfg,
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		stderrR:     stderrR,
		stderrLines: make(chan string, 16),
		ready:       make(chan struct{}),
	}
	// Single goroutine drains stderr into stderrLines. The
	// banner reader and the per-synth "END" reader both consume
	// from this channel, so we never share a bufio.Reader
	// across goroutines.
	go p.drainStderr()
	go p.readBanner()
	select {
	case <-p.ready:
		if p.readyErr != nil {
			p.close()
			return nil, p.readyErr
		}
	case <-time.After(startup):
		p.close()
		return nil, fmt.Errorf("tts: piper: startup timeout after %v", startup)
	}
	_ = perSynth
	return p, nil
}

// drainStderr reads stderr line-by-line and ships them on
// p.stderrLines. On EOF (subprocess exited) the channel is
// closed so readers can finish.
func (p *piperProc) drainStderr() {
	scanner := bufio.NewScanner(p.stderrR)
	for scanner.Scan() {
		p.stderrLines <- scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		p.stderrErr = err
	}
	close(p.stderrLines)
}

// readBanner reads the first stderr line from p.stderrLines
// and parses the "PIPER_READY" banner. If the banner does not
// arrive within 500 ms we proceed with the default sample rate
// (some Piper builds do not emit a banner at all).
func (p *piperProc) readBanner() {
	const bannerTimeout = 500 * time.Millisecond
	select {
	case line, ok := <-p.stderrLines:
		if !ok {
			// stderr closed before any line; default.
			p.sampleRate = 22050
			close(p.ready)
			return
		}
		bline := bytes.TrimSpace([]byte(line))
		if !bytes.HasPrefix(bline, []byte("PIPER_READY")) {
			p.sampleRate = 22050
			close(p.ready)
			return
		}
		parts := bytes.Fields(bline)
		if len(parts) >= 2 {
			if rate, err := strconv.Atoi(string(parts[1])); err == nil {
				p.sampleRate = rate
			}
		}
		if p.sampleRate == 0 {
			p.sampleRate = 22050
		}
		close(p.ready)
	case <-time.After(bannerTimeout):
		p.sampleRate = 22050
		close(p.ready)
	}
}

// synthesize sends "SYNTH <text>\n" on stdin and reads the
// resulting PCM from stdout. The protocol is:
//
//   - The wrapper reads a length line (decimal) from stdout.
//   - The wrapper reads exactly that many bytes of PCM from stdout.
//   - The wrapper reads "END\n" from stderr.
//
// The length and END reads are independent: END is consumed by
// the stderrLines channel to keep stderr draining.
func (p *piperProc) synthesize(ctx context.Context, text string) ([]float32, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Send the command.
	cmd := []byte("SYNTH " + text + "\n")
	if _, err := p.stdin.Write(cmd); err != nil {
		return nil, 0, fmt.Errorf("tts: piper: write SYNTH: %w", err)
	}

	// Read the length line from stdout.
	lengthReader := bufio.NewReader(p.stdout)
	lengthStr, err := lengthReader.ReadString('\n')
	if err != nil {
		_ = p.stdin.Close()
		return nil, 0, fmt.Errorf("tts: piper: read length: %w", err)
	}
	lengthStr = strings.TrimSpace(lengthStr)
	byteCount, err := strconv.Atoi(lengthStr)
	if err != nil {
		_ = p.stdin.Close()
		return nil, 0, fmt.Errorf("tts: piper: parse length %q: %w", lengthStr, err)
	}
	if byteCount < 0 {
		_ = p.stdin.Close()
		return nil, 0, fmt.Errorf("tts: piper: negative length %d", byteCount)
	}

	// Read exactly byteCount bytes of PCM. We use io.ReadFull
	// so a short read is an error, not silent truncation.
	raw := make([]byte, byteCount)
	if byteCount > 0 {
		// Set a read deadline via ctx: if it fires, we close
		// stdin to unblock the read and report ctx.Err.
		done := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(lengthReader, raw)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				return nil, 0, fmt.Errorf("tts: piper: read PCM: %w", err)
			}
		case <-ctx.Done():
			_ = p.stdin.Close()
			return nil, 0, ctx.Err()
		}
	}

	// Wait for "END" to appear on stderr. If the binary errors
	// before printing END, surface the error.
	select {
	case line, ok := <-p.stderrLines:
		if !ok {
			// stderr closed without "END"; possibly the
			// subprocess died.
			if p.stderrErr != nil {
				return nil, 0, fmt.Errorf("tts: piper: read END: %w", p.stderrErr)
			}
			return nil, 0, errors.New("tts: piper: stderr closed before END")
		}
		bline := bytes.TrimSpace([]byte(line))
		if !bytes.Equal(bline, []byte("END")) {
			return nil, 0, fmt.Errorf("tts: piper: unexpected stderr line %q", string(bline))
		}
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}

	// Decode the raw int16 LE buffer to float32.
	if byteCount%2 != 0 {
		return nil, 0, errors.New("tts: piper: odd PCM byte count")
	}
	out := make([]float32, byteCount/2)
	for i := range out {
		u := binary.LittleEndian.Uint16(raw[2*i:])
		s := int16(u)
		out[i] = float32(s) / 32768.0
	}
	return out, p.sampleRate, nil
}

// synthesizeStream consumes sentences from in and emits per-
// sentence PCM chunks on out. The piper subprocess is held open
// across sentences.
func (p *piperProc) synthesizeStream(ctx context.Context, in <-chan string, out chan<- []float32) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case text, ok := <-in:
			if !ok {
				return nil
			}
			pcm, _, err := p.synthesize(ctx, text)
			if err != nil {
				return err
			}
			select {
			case out <- pcm:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// close terminates the subprocess. Sends "QUIT\n" on stdin, then
// waits for the process to exit (with a short grace period).
// Idempotent.
func (p *piperProc) close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Best-effort QUIT.
	_, _ = p.stdin.Write([]byte("QUIT\n"))
	_ = p.stdin.Close()

	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		// Force kill.
		_ = p.cmd.Process.Kill()
		<-done
		return errors.New("tts: piper: forced kill after timeout")
	}
}
