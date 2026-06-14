// tether-loopback: an in-process end-to-end loopback harness for
// the Tether data plane. See plan.md §2.7.
//
// Usage:
//
//	tether-loopback                  # default: 1s synthetic audio
//	tether-loopback -duration 60s    # 60s synthetic audio
//	tether-loopback -payload custom  # read payload from stdin
//
// On success the tool prints a one-line summary to stdout and exits
// 0. On any failure it prints the error and exits 1.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/loopback"
	"github.com/jbutlerdev/tether/go/internal/radio"
	"github.com/jbutlerdev/tether/go/internal/serial"
	"github.com/jbutlerdev/tether/go/pkg/protocol"
	"github.com/jbutlerdev/tether/go/pkg/protocol/protocolpb"
)

func main() {
	duration := flag.Duration("duration", 1*time.Second, "synthetic audio duration (sine wave)")
	freq := flag.Float64("freq", 440, "sine wave frequency in Hz")
	flag.Parse()

	pa, pb := serial.NewLoopbackPair()
	defer pa.Close()
	defer pb.Close()

	// Generate the synthetic audio.
	pcm := sineWave(*freq, 8000, *duration)

	// Encode and fragment.
	c := codec.NewMock()
	opusFrames := encodeAll(c, pcm)

	convID := bytes.Repeat([]byte{0xCD}, 16)
	envs, err := protocol.Fragment(opusFrames, 1, convID,
		protocolpb.MsgType_MSG_TYPE_DATA, protocolpb.AudioKind_AUDIO_KIND_MIC)
	if err != nil {
		log.Fatalf("fragment: %v", err)
	}

	// Run the round-trip.
	stats := loopback.RunOnce(loopback.RunOnceOptions{
		LocalRadio:  pa,
		RemoteRadio: pb,
		Envelopes:   envs,
		Timeout:     200 * time.Millisecond,
		MaxRetry:    10,
	})

	// Report.
	fmt.Fprintf(os.Stdout, "tether-loopback: sent=%d acked=%d received=%d retries=%d duration=%v\n",
		stats.Sent, stats.Acked, stats.Received, stats.Retries, stats.Duration)

	if stats.Failed != nil {
		log.Fatalf("failed at seq=%d", stats.Failed.SeqNum)
	}
	if stats.Acked != stats.Sent {
		log.Fatalf("not all acked: %d/%d", stats.Acked, stats.Sent)
	}

	_ = context.Background
}

func sineWave(freqHz float64, sampleRate int, dur time.Duration) []int16 {
	n := int(float64(sampleRate) * dur.Seconds())
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		v := math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate))
		out[i] = int16(v * 16000)
	}
	return out
}

func encodeAll(c codec.Opus, pcm []int16) []byte {
	var out []byte
	frame := c.FrameSize()
	for off := 0; off < len(pcm); off += frame {
		end := off + frame
		if end > len(pcm) {
			buf := make([]int16, frame)
			copy(buf, pcm[off:])
			encoded, _ := c.Encode(buf)
			out = append(out, encoded...)
			break
		}
		encoded, _ := c.Encode(pcm[off:end])
		out = append(out, encoded...)
	}
	return out
}

var _ radio.Radio
