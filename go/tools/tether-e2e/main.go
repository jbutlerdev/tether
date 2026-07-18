// tether-e2e: an in-process end-to-end simulator for the Tether data
// plane. See REVIEW.md F17 and go/internal/e2e.
//
// Usage:
//
//	tether-e2e                  # one full round trip (uplink + downlink)
//	tether-e2e -loss 0.3        # inject 30% LoRa loss on the uplink
//	tether-e2e -rounds 5        # run 5 round trips
//
// On success the tool prints a one-line summary per round and exits 0.
// On any failure it prints the error and exits 1. Every dependency is
// an in-process mock, so the binary runs anywhere Go runs — no radio,
// no forge server, no audio hardware.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"time"

	"github.com/jbutlerdev/tether/go/internal/e2e"
)

// Options configures a run.
type Options struct {
	Rounds int
	Loss   float64
	Out    io.Writer
}

// Run executes the simulator end-to-end. It is the testable
// counterpart to main() so the CLI binary is exercised by unit tests.
func Run(opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Rounds <= 0 {
		opts.Rounds = 1
	}
	sim, err := e2e.NewSimulator()
	if err != nil {
		return fmt.Errorf("new simulator: %w", err)
	}
	defer sim.Close()
	if opts.Loss > 0 {
		sim.SetUplinkLoss(opts.Loss)
	}

	rootCtx, rootCancel := context.WithTimeout(context.Background(), time.Duration(opts.Rounds)*30*time.Second)
	defer rootCancel()

	for r := 1; r <= opts.Rounds; r++ {
		ctx, cancel := context.WithTimeout(rootCtx, 30*time.Second)
		sessionID, convID, err := sim.NewConversation(ctx)
		if err != nil {
			cancel()
			return fmt.Errorf("round %d: new conversation: %w", r, err)
		}
		pcm := sine(1600) // 0.2 s of 440 Hz @ 8 kHz
		if err := sim.RunUplink(ctx, convID, pcm); err != nil {
			cancel()
			return fmt.Errorf("round %d: uplink: %w", r, err)
		}
		out, err := sim.RunDownlink(ctx, sessionID, "Hello world.")
		if err != nil {
			cancel()
			return fmt.Errorf("round %d: downlink: %w", r, err)
		}
		cancel()
		fmt.Fprintf(opts.Out, "round %d: uplink ok, downlink ok (%d PCM samples)%s\n",
			r, len(out), lossSuffix(opts.Loss))
	}
	return nil
}

func lossSuffix(loss float64) string {
	if loss <= 0 {
		return ""
	}
	return fmt.Sprintf(", loss=%.0f%%", loss*100)
}

// sine produces n samples of a 440 Hz sine wave at 8 kHz.
func sine(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(math.Sin(2*math.Pi*440*float64(i)/8000) * 8000)
	}
	return out
}

func main() {
	rounds := flag.Int("rounds", 1, "number of full round trips to run")
	loss := flag.Float64("loss", 0, "uplink packet-loss probability in [0,1]")
	flag.Parse()
	if err := Run(Options{Rounds: *rounds, Loss: *loss}); err != nil {
		log.SetFlags(0)
		log.Printf("tether-e2e: %v", err)
		os.Exit(1)
	}
}
