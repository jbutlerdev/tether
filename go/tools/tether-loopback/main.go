// tether-loopback: an in-process end-to-end loopback harness for
// the Tether data plane. See plan.md §2.7.
//
// Usage:
//
//	tether-loopback                  # default: 1s synthetic audio
//	tether-loopback -duration 60s    # 60s synthetic audio
//
// On success the tool prints a one-line summary to stdout and exits
// 0. On any failure it prints the error and exits 1.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/loopback"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

// Options configures the loopback run.
type Options struct {
	Duration time.Duration
	Freq     float64
	Out      io.Writer
}

// Run executes the loopback end-to-end. It is the testable
// counterpart to main() so the CLI binary is exercised by the
// unit tests.
func Run(opts Options) error {
	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	pcm := SineWave(opts.Freq, 8000, opts.Duration)
	c := codec.NewMock()
	opusFrames := EncodeAll(c, pcm)

	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, err := protocol.Fragment(opusFrames, 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		return fmt.Errorf("fragment: %w", err)
	}

	stats := loopback.RunOnce(loopback.RunOnceOptions{
		LocalRadio:  pa,
		RemoteRadio: pb,
		Envelopes:   envs,
		Timeout:     200 * time.Millisecond,
		MaxRetry:    10,
	})

	fmt.Fprintf(opts.Out, "tether-loopback: sent=%d acked=%d received=%d retries=%d duration=%v\n",
		stats.Sent, stats.Acked, stats.Received, stats.Retries, stats.Duration)

	if stats.Failed != nil {
		return fmt.Errorf("failed at seq=%d", stats.Failed.SeqNum)
	}
	if stats.Acked != stats.Sent {
		return fmt.Errorf("not all acked: %d/%d", stats.Acked, stats.Sent)
	}
	return nil
}

// SineWave generates a sine wave of the given frequency, sample
// rate, and duration as int16 PCM. Exported so tests can use it.
func SineWave(freqHz float64, sampleRate int, dur time.Duration) []int16 {
	n := int(float64(sampleRate) * dur.Seconds())
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		v := math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate))
		out[i] = int16(v * 16000)
	}
	return out
}

// EncodeAll encodes a PCM buffer with the given codec as a
// length-delimited blob (2-byte LE length prefix per frame), padding
// the last frame if necessary.
func EncodeAll(c codec.Opus, pcm []int16) []byte {
	f := codec.NewFramer(c)
	out, err := f.EncodeBlob(pcm)
	if err != nil {
		return nil
	}
	return out
}

func main() {
	duration := flag.Duration("duration", 1*time.Second, "synthetic audio duration (sine wave)")
	freq := flag.Float64("freq", 440, "sine wave frequency in Hz")
	flag.Parse()

	if err := Run(Options{
		Duration: *duration,
		Freq:     *freq,
		Out:      log.Writer(),
	}); err != nil {
		log.Fatalf("tether-loopback: %v", err)
	}
}
