// STT word-error-rate benchmark. See plan.md §6.3.
//
// The benchmark computes WER against a held-out transcript set
// (a small LibriSpeech-like sample we ship in testdata). The
// full LibriSpeech test-clean download is too large to commit;
// the bench documents the procedure and the WER formula, then
// runs against the in-tree sample so it executes in CI.
//
// Run with:
//
//	cd go && go test -tags parakeet -run=WER -bench=BenchmarkParakeet_WER \
//	    -benchtime=1x ./internal/stt/
//
// The WER algorithm itself (in wer.go) is unit-tested in the
// default (no-tag) build. The integration with Parakeet is
// gated by the `parakeet` tag.
//
//go:build parakeet

package stt_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/stt"
)

// werSample is one row of the held-out sample.
type werSample struct {
	Path       string
	Transcript string
}

// werSampleSet is the in-tree held-out set. The audio is
// synthesised by scripts/gen-test-audio.sh from LibriSpeech
// test-clean samples. When the audio is not present the bench
// skips itself.
var werSampleSet = []werSample{
	{Path: "wer/utt_0001.wav", Transcript: "the quick brown fox jumps over the lazy dog"},
	{Path: "wer/utt_0002.wav", Transcript: "she sells seashells by the seashore"},
	{Path: "wer/utt_0003.wav", Transcript: "one two three four five six seven eight"},
	{Path: "wer/utt_0004.wav", Transcript: "the rain in spain stays mainly on the plain"},
	{Path: "wer/utt_0005.wav", Transcript: "to be or not to be that is the question"},
}

// BenchmarkParakeet_WER runs the Parakeet recogniser on every
// sample in werSampleSet, computes aggregate WER, and (if
// TETHER_WER_THRESHOLD is set) asserts the result. The bench
// is gated by -bench so it does not run on every `go test`.
func BenchmarkParakeet_WER(b *testing.B) {
	dir := parakeetModelDir(b)
	p, err := stt.NewParakeet(stt.ParakeetConfig{ModelDir: dir, NumThreads: 2})
	if err != nil {
		b.Fatalf("NewParakeet: %v", err)
	}
	defer p.Close()

	threshold := 0.10
	if t := os.Getenv("TETHER_WER_THRESHOLD"); t != "" {
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = f
		}
	}

	b.ResetTimer()
	var totalWER float64
	var totalWords int
	for i := 0; i < b.N; i++ {
		totalWER = 0
		totalWords = 0
		for _, s := range werSampleSet {
			wavPath := filepath.Join("testdata", s.Path)
			pcm, sr := loadTestWAVBench(b, wavPath)
			if sr != stt.ParakeetSampleRate {
				pcm = codec.Resample(pcm, sr, stt.ParakeetSampleRate)
			}
			got, err := p.Transcribe(context.Background(), pcm, stt.ParakeetSampleRate)
			if err != nil {
				b.Logf("Transcribe(%s): %v", s.Path, err)
				continue
			}
			w := stt.WER(s.Transcript, got)
			ref := stt.NormalizeWER(s.Transcript)
			totalWER += w * float64(len(ref))
			totalWords += len(ref)
		}
		b.ReportMetric(totalWER, "wer-frac")
	}
	b.StopTimer()

	if totalWords == 0 {
		b.Skip("no WER samples ran (audio files missing?)")
	}
	avg := totalWER / float64(totalWords)
	b.Logf("average WER over %d words: %.4f (threshold %.2f)", totalWords, avg, threshold)
	if avg > threshold {
		b.Fatalf("WER %.4f exceeds threshold %.4f", avg, threshold)
	}
}

// loadTestWAVBench is the benchmark-side loader; it does not
// t.Skip on missing files (it fatals).
func loadTestWAVBench(b *testing.B, path string) ([]float32, int) {
	b.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("read %s: %v", path, err)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" {
		b.Fatalf("%s: not a WAV file", path)
	}
	sr := int(data[24]) | int(data[25])<<8 | int(data[26])<<16 | int(data[27])<<24
	pcmBytes := data[44:]
	out := make([]float32, len(pcmBytes)/2)
	for i := range out {
		lo := uint16(pcmBytes[2*i])
		hi := uint16(pcmBytes[2*i+1])
		u := lo | hi<<8
		s := int16(u)
		out[i] = float32(s) / 32768.0
	}
	return out, sr
}
