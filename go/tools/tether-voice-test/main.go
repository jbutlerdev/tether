// tether-voice-test: an end-to-end voice pipeline harness for
// the Tether base station. See plan.md §6.9.
//
// Usage:
//
//	tether-voice-test -in input.wav -out output.wav
//
// On success the tool prints a one-line summary to stdout and
// exits 0. The pipeline:
//
//  1. Reads a 16-bit mono WAV from -in (any sample rate;
//     we resample internally to 16 kHz for STT).
//  2. Runs STT (Parakeet-TDT 0.6B v2 via sherpa-onnx when
//     built with -tags parakeet; the mock otherwise).
//  3. Prints the recognised text to stdout.
//  4. Runs TTS (Piper subprocess when built with -tags piper
//     and a binary is available; the mock otherwise).
//  5. Resamples the TTS output to 8 kHz and writes a 16-bit
//     mono WAV to -out (or to the InMemorySink if -sink is
//     used by the test harness).
//
// The CLI lives in main() and is exercised by the unit tests
// via RunOnce (see main_test.go). The harness uses the
// polyphase resampler from internal/codec to bridge 8/16/22 kHz.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/jbutlerdev/tether/go/internal/audio"
	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// Options configures a single RunOnce execution. The CLI builds
// it from flags; the tests build it directly.
type Options struct {
	InWAV  string
	OutWAV string
	Sink   audio.Sink
	STT    stt.Transcriber
	TTS    tts.Synthesizer
	Out    io.Writer
}

// RunOnce executes the voice pipeline end-to-end. It is the
// testable counterpart to main().
//
// The pipeline:
//
//  1. Read -in as a 16-bit mono WAV. Convert to float32.
//  2. Resample to 16 kHz.
//  3. STT.Transcribe → text.
//  4. Print text to opts.Out.
//  5. TTS.Synthesize → PCM at the TTS sample rate (22050).
//  6. Resample to 8 kHz.
//  7. Convert to int16.
//  8. Write to -out as a 16-bit mono WAV (or to Sink).
//
// Returns nil on success.
func RunOnce(opts Options) error {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.InWAV == "" && opts.Sink == nil {
		fmt.Fprintln(opts.Out, "usage: tether-voice-test -in input.wav -out output.wav")
		return errors.New("tether-voice-test: missing -in")
	}
	if opts.STT == nil {
		opts.STT = stt.NewMock()
	}
	if opts.TTS == nil {
		opts.TTS = tts.NewMock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Read the input WAV.
	pcm, sampleRate, err := readWAV(opts.InWAV)
	if err != nil {
		return fmt.Errorf("tether-voice-test: read input: %w", err)
	}

	// 2. Resample to 16 kHz for STT (Parakeet's native rate).
	var pcm16k []float32
	if sampleRate == stt.ParakeetSampleRate {
		pcm16k = pcm
	} else if sampleRate > 0 {
		pcm16k = codec.Resample(pcm, sampleRate, stt.ParakeetSampleRate)
	} else {
		return fmt.Errorf("tether-voice-test: invalid sample rate %d", sampleRate)
	}

	// 3. STT.
	text, err := opts.STT.Transcribe(ctx, pcm16k, stt.ParakeetSampleRate)
	if err != nil {
		return fmt.Errorf("tether-voice-test: stt: %w", err)
	}
	fmt.Fprintf(opts.Out, "tether-voice-test: stt=%q\n", text)

	// 4. TTS.
	ttsPcm, ttsRate, err := opts.TTS.Synthesize(ctx, text)
	if err != nil {
		return fmt.Errorf("tether-voice-test: tts: %w", err)
	}

	// 5. Resample TTS to 8 kHz.
	const outRate = 8000
	var pcm8k []float32
	if ttsRate == outRate {
		pcm8k = ttsPcm
	} else if ttsRate > 0 {
		pcm8k = codec.Resample(ttsPcm, ttsRate, outRate)
	} else {
		return fmt.Errorf("tether-voice-test: invalid tts sample rate %d", ttsRate)
	}

	// 6. Convert to int16 and write.
	pcm16 := floatsToInt16(pcm8k)
	if opts.Sink != nil {
		return writeToSink(opts.Sink, pcm16)
	}
	if opts.OutWAV == "" {
		return errors.New("tether-voice-test: missing -out (or Sink)")
	}
	if err := writeWAV(opts.OutWAV, pcm16, outRate); err != nil {
		return fmt.Errorf("tether-voice-test: write output: %w", err)
	}
	fmt.Fprintf(opts.Out, "tether-voice-test: tts→%s (%d samples @ %d Hz)\n",
		opts.OutWAV, len(pcm16), outRate)
	return nil
}

// readWAV reads a 16-bit mono PCM WAV file and returns the
// samples as float32 in [-1, 1] plus the sample rate.
func readWAV(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	sampleRate := int(binary.LittleEndian.Uint32(data[24:28]))
	if sampleRate <= 0 {
		return nil, 0, errors.New("invalid sample rate")
	}
	pcmBytes := data[44:]
	n := len(pcmBytes) / 2
	out := make([]float32, n)
	for i := range out {
		u := binary.LittleEndian.Uint16(pcmBytes[2*i:])
		s := int16(u)
		out[i] = float32(s) / 32768.0
	}
	return out, sampleRate, nil
}

// writeWAV writes a 16-bit mono PCM WAV file.
func writeWAV(path string, pcm []int16, sampleRate int) error {
	const header = 44
	buf := make([]byte, header+2*len(pcm))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+2*len(pcm)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(buf[header+2*i:], uint16(v))
	}
	return os.WriteFile(path, buf, 0o644)
}

// writeToSink writes PCM samples to an audio.Sink.
func writeToSink(s audio.Sink, pcm []int16) error {
	// The InMemorySink accepts any number of samples; the
	// FileSink appends. We write in 4096-sample chunks to
	// match the canonical Tether frame size.
	const chunk = 4096
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := s.Write(pcm[off:end]); err != nil {
			return err
		}
	}
	return nil
}

// floatsToInt16 converts a float32 PCM buffer to int16 with
// clamping.
func floatsToInt16(in []float32) []int16 {
	out := make([]int16, len(in))
	for i, v := range in {
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		out[i] = int16(v * 32767)
	}
	return out
}

func main() {
	var (
		inPath  = flag.String("in", "", "input WAV file (16-bit mono PCM)")
		outPath = flag.String("out", "", "output WAV file (16-bit mono PCM @ 8 kHz)")
	)
	flag.Parse()

	opts := Options{
		InWAV:  *inPath,
		OutWAV: *outPath,
		Out:    log.Writer(),
	}
	if err := RunOnce(opts); err != nil {
		log.Fatalf("tether-voice-test: %v", err)
	}
	_ = bytes.NewBuffer(nil) // keep bytes import for tests
}
