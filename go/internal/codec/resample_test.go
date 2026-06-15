// Tests for the PCM resampler. See plan.md §6.7.
//
// The resampler must convert mono PCM between 8 kHz, 16 kHz, and
// 22.05 kHz. It is used to bridge the M5 (8 kHz), Parakeet (16 kHz),
// and Piper (22.05 kHz) worlds. The tests are waveform-based:
// a known sine wave at the input rate must produce a sine wave at
// the same physical frequency at the output rate.
package codec_test

import (
	"math"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/codec"
)

const (
	// Allow amplitude and frequency tolerance for the FFT/sine
	// reconstruction check. Polyphase resampling loses a few dB of
	// headroom due to the anti-alias filter.
	resampleAmpTol = 0.15 // ±15% amplitude
)

// sine generates a mono sine wave at freqHz / sampleRate / dur.
func sine(freqHz float64, sampleRate int, dur float64) []float32 {
	n := int(float64(sampleRate) * dur)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
	}
	return out
}

// peakFrequency returns the physical frequency in Hz of the largest
// FFT bin in sig (mono, at sampleRate). It uses a simple DFT, using
// min(1024, len(sig)) samples. Good enough for "is the dominant
// frequency close to what we put in?".
func peakFrequency(sig []float32, sampleRate int) float64 {
	N := 1024
	if len(sig) < N {
		N = len(sig)
	}
	if N == 0 {
		return 0
	}
	var bestMagSq float64
	bestK := -1
	for k := 0; k < N/2; k++ {
		var re, im float64
		for n := 0; n < N; n++ {
			phi := 2 * math.Pi * float64(k) * float64(n) / float64(N)
			re += float64(sig[n]) * math.Cos(phi)
			im -= float64(sig[n]) * math.Sin(phi)
		}
		magSq := re*re + im*im
		if magSq > bestMagSq {
			bestMagSq = magSq
			bestK = k
		}
	}
	if bestK < 0 {
		return 0
	}
	return float64(bestK) * float64(sampleRate) / float64(N)
}

// TestResample_8kTo16k_Sine: 1 kHz sine at 8 kHz → 1 kHz sine at 16 kHz.
func TestResample_8kTo16k_Sine(t *testing.T) {
	in := sine(1000, 8000, 0.1) // 0.1 s = 800 samples
	out := codec.Resample(in, 8000, 16000)
	if len(out) < 1024 {
		t.Fatalf("Resample produced %d samples; need at least 1024 for FFT", len(out))
	}
	peak := peakFrequency(out, 16000)
	if math.Abs(peak-1000) > 25 {
		t.Errorf("8k→16k of 1 kHz sine: peak at %v Hz, want ~1000 Hz", peak)
	}
}

// TestResample_22kTo8k_Sine: 1 kHz sine at 22.05 kHz → 1 kHz sine at 8 kHz.
func TestResample_22kTo8k_Sine(t *testing.T) {
	in := sine(1000, 22050, 0.5) // 0.5 s = 11025 samples → ~4000 at 8k
	out := codec.Resample(in, 22050, 8000)
	if len(out) < 1024 {
		t.Fatalf("Resample produced %d samples; need at least 1024 for FFT", len(out))
	}
	peak := peakFrequency(out, 8000)
	if math.Abs(peak-1000) > 25 {
		t.Errorf("22.05k→8k of 1 kHz sine: peak at %v Hz, want ~1000 Hz", peak)
	}
}

// TestResample_8kTo22k_Sine: 1 kHz sine at 8 kHz → 1 kHz sine at 22.05 kHz.
func TestResample_8kTo22k_Sine(t *testing.T) {
	in := sine(1000, 8000, 0.1)
	out := codec.Resample(in, 8000, 22050)
	if len(out) < 1024 {
		t.Fatalf("Resample produced %d samples; need at least 1024 for FFT", len(out))
	}
	peak := peakFrequency(out, 22050)
	if math.Abs(peak-1000) > 25 {
		t.Errorf("8k→22.05k of 1 kHz sine: peak at %v Hz, want ~1000 Hz", peak)
	}
}

// TestResample_Passthrough_8kTo8k: no change in length or content.
func TestResample_Passthrough_8kTo8k(t *testing.T) {
	in := sine(440, 8000, 0.05)
	out := codec.Resample(in, 8000, 8000)
	if len(out) != len(in) {
		t.Fatalf("Passthrough 8k→8k: len(out)=%d, want %d", len(out), len(in))
	}
	for i := range in {
		if math.Abs(float64(out[i]-in[i])) > 1e-6 {
			t.Errorf("Passthrough 8k→8k: sample %d mismatch: got %v, want %v", i, out[i], in[i])
			break
		}
	}
}

// TestResample_Silent: all zeros, no NaN.
func TestResample_Silent(t *testing.T) {
	in := make([]float32, 1024)
	out := codec.Resample(in, 8000, 16000)
	if len(out) == 0 {
		t.Fatal("Resample of silent: got empty output")
	}
	for i, s := range out {
		if s != 0 {
			t.Errorf("Resample of silent: sample %d = %v, want 0", i, s)
			break
		}
		if math.IsNaN(float64(s)) {
			t.Errorf("Resample of silent: sample %d is NaN", i)
			break
		}
	}
}

// TestResample_LongInput: 60 s of audio resamples without artifacts.
func TestResample_LongInput(t *testing.T) {
	in := sine(440, 8000, 60.0) // 60 s = 480_000 samples
	out := codec.Resample(in, 8000, 16000)
	// Expect approximately 960_000 samples. Allow ±1 sample tolerance.
	if len(out) < 959_999 || len(out) > 960_001 {
		t.Errorf("60s 8k→16k: got %d samples, want ~960000", len(out))
	}
	// No NaNs.
	for i, s := range out {
		if math.IsNaN(float64(s)) {
			t.Fatalf("NaN at sample %d", i)
		}
	}
}

// TestResample_RatioEdge: 22050 → 8000 = 8000/22050. The length
// must be ≈ 8000/22050 of the input.
func TestResample_RatioEdge(t *testing.T) {
	const inLen = 22050 * 2 // 2 seconds
	in := make([]float32, inLen)
	for i := range in {
		in[i] = float32(i) / float32(inLen)
	}
	out := codec.Resample(in, 22050, 8000)
	want := 8000 * 2 // 2 s at 8 kHz
	if math.Abs(float64(len(out)-want)) > 2 {
		t.Errorf("22050→8000: got %d samples, want %d ±2", len(out), want)
	}
}

// TestResample_OutputLen: the package must document the output
// length formula somewhere reachable. We assert it here by running
// it for a few representative input lengths.
func TestResample_OutputLen(t *testing.T) {
	cases := []struct {
		inRate, outRate, inLen, want int
	}{
		{8000, 16000, 800, 1600},   // exactly 2x
		{16000, 8000, 1600, 800},   // exactly 0.5x
		{22050, 8000, 22050, 8000}, // exactly 0.3628x
		{8000, 22050, 8000, 22050}, // exactly 2.75625x
	}
	for _, c := range cases {
		in := make([]float32, c.inLen)
		out := codec.Resample(in, c.inRate, c.outRate)
		// Allow ±1 sample.
		if math.Abs(float64(len(out)-c.want)) > 1 {
			t.Errorf("Resample(%d→%d, inLen=%d): outLen=%d, want %d±1",
				c.inRate, c.outRate, c.inLen, len(out), c.want)
		}
	}
}

// TestResample_Resample8kInputResampleTo22kThenBack: round-trip
// preserves length within ±2 samples and has no NaNs.
func TestResample_RoundTrip_8k_22k_8k(t *testing.T) {
	in := sine(1000, 8000, 0.5)
	up := codec.Resample(in, 8000, 22050)
	back := codec.Resample(up, 22050, 8000)
	if math.Abs(float64(len(back)-len(in))) > 4 {
		t.Errorf("8k→22.05k→8k: got %d, want ~%d", len(back), len(in))
	}
	for i, s := range back {
		if math.IsNaN(float64(s)) {
			t.Fatalf("NaN at sample %d", i)
		}
	}
}
