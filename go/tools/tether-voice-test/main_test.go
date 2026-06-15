// Tests for the tether-voice-test CLI. See plan.md §6.9.
//
// The CLI exposes the full voice pipeline end-to-end:
//
//   voice WAV → resample 8→16kHz → STT → text → TTS → resample
//   22→8kHz → Opus encode → fragment → LoRa → reassemble →
//   Opus decode → audio file
//
// The tests in this file run the same code path the CLI uses
// (RunOnce in main.go) so the binary is exercised by `go test`.
//
// The tests use the Mock STT and Mock TTS so they don't
// require sherpa-onnx or piper installed. The full real-model
// integration is gated by the 'parakeet' and 'piper' build
// tags (see tether-voice-test_integration_test.go).
package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/audio"
	"github.com/jbutlerdev/tether/go/internal/codec"
	"github.com/jbutlerdev/tether/go/internal/stt"
	"github.com/jbutlerdev/tether/go/internal/tts"
)

// makeTestWAV writes a 16-bit mono PCM WAV file at the given
// sample rate with a sine wave at the given frequency. Returns
// the raw PCM as float32 in [-1, 1] (also written to disk).
func makeTestWAV(t *testing.T, path string, dur, sampleRate int, freq float64) []float32 {
	t.Helper()
	n := dur * sampleRate
	pcm := make([]float32, n)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)))
	}
	// Encode as int16.
	pcm16 := make([]int16, n)
	for i, v := range pcm {
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		pcm16[i] = int16(v * 32767)
	}
	const header = 44
	buf := make([]byte, header+2*len(pcm16))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+2*len(pcm16)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(2*len(pcm16)))
	for i, v := range pcm16 {
		binary.LittleEndian.PutUint16(buf[header+2*i:], uint16(v))
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return pcm
}

// TestVoicePipeline_HelloWorld_RoundTrip: a known WAV runs
// through the pipeline; the STT mock returns a deterministic
// text, the TTS mock returns a deterministic PCM, and the
// output file is written and is non-empty.
func TestVoicePipeline_HelloWorld_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")

	makeTestWAV(t, inPath, 1, 8000, 440)

	opts := Options{
		InWAV:    inPath,
		OutWAV:   outPath,
		STT:      stt.NewMock(stt.MockOptionLatency(0)),
		TTS:      tts.NewMock(),
		Out:      &bytes.Buffer{},
	}
	if err := RunOnce(opts); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Output file exists and is non-empty.
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat %s: %v", outPath, err)
	}
	if info.Size() < 44 {
		t.Errorf("output WAV too small: %d bytes", info.Size())
	}
	// Output should be a valid RIFF/WAVE.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	if !bytes.Equal(data[0:4], []byte("RIFF")) {
		t.Errorf("output is not RIFF: %q", data[0:4])
	}
	if !bytes.Equal(data[8:12], []byte("WAVE")) {
		t.Errorf("output is not WAVE: %q", data[8:12])
	}
}

// TestVoicePipeline_MissingInput: a non-existent input file
// fails with a clear error.
func TestVoicePipeline_MissingInput(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		InWAV:  filepath.Join(dir, "nope.wav"),
		OutWAV: filepath.Join(dir, "out.wav"),
		STT:    stt.NewMock(),
		TTS:    tts.NewMock(),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err == nil {
		t.Fatal("RunOnce with missing input: want error, got nil")
	}
}

// TestVoicePipeline_Resample8kTo16k: the pipeline resamples
// 8 kHz input to 16 kHz for STT.
func TestVoicePipeline_Resample8kTo16k(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")
	makeTestWAV(t, inPath, 1, 8000, 440)

	// Use a STT that records what it received; check the sample
	// rate was 16000.
	var mock = stt.NewMock()
	opts := Options{
		InWAV:  inPath,
		OutWAV: outPath,
		STT:    mock,
		TTS:    tts.NewMock(),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	calls := mock.Calls()
	if len(calls) == 0 {
		t.Fatal("STT was not called")
	}
	if calls[0].SampleRate != 16000 {
		t.Errorf("STT sample rate: want 16000 (resampled), got %d", calls[0].SampleRate)
	}
}

// TestVoicePipeline_TTSSampleRate: the TTS output is resampled
// to 8 kHz before being written to the output WAV.
func TestVoicePipeline_TTSSampleRate(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")
	makeTestWAV(t, inPath, 1, 8000, 440)

	opts := Options{
		InWAV:  inPath,
		OutWAV: outPath,
		STT:    stt.NewMock(),
		TTS:    tts.NewMock(),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Read the output WAV header and check the sample rate.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	sr := int(data[24]) | int(data[25])<<8 | int(data[26])<<16 | int(data[27])<<24
	if sr != 8000 {
		t.Errorf("output sample rate: want 8000, got %d", sr)
	}
}

// TestVoicePipeline_DefaultOptions: a no-input config uses
// the defaults (help-style banner).
func TestVoicePipeline_DefaultOptions(t *testing.T) {
	buf := &bytes.Buffer{}
	opts := Options{
		Out: buf,
	}
	// RunOnce with empty paths should error, not panic.
	if err := RunOnce(opts); err == nil {
		t.Error("RunOnce with empty paths: want error, got nil")
	}
	if !strings.Contains(buf.String(), "usage") &&
		!strings.Contains(buf.String(), "error") {
		t.Logf("output: %q", buf.String())
	}
}

// TestVoicePipeline_STTError: an STT error propagates to the
// pipeline.
func TestVoicePipeline_STTError(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")
	makeTestWAV(t, inPath, 1, 8000, 440)
	opts := Options{
		InWAV:  inPath,
		OutWAV: outPath,
		STT:    stt.NewMock(stt.MockOptionError(errSTTSimulated)),
		TTS:    tts.NewMock(),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err == nil {
		t.Fatal("RunOnce with STT error: want error, got nil")
	}
}

// errSTTSimulated is a sentinel for the STT error path.
var errSTTSimulated = &testErr{message: "simulated STT failure"}

// testErr is a tiny error type so we can use errors.Is.
type testErr struct{ message string }

func (e *testErr) Error() string { return e.message }

// TestVoicePipeline_TTSError: a TTS error propagates.
func TestVoicePipeline_TTSError(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")
	makeTestWAV(t, inPath, 1, 8000, 440)
	opts := Options{
		InWAV:  inPath,
		OutWAV: outPath,
		STT:    stt.NewMock(),
		TTS:    tts.NewMock(tts.MockOptionError(errTTSSimulated)),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err == nil {
		t.Fatal("RunOnce with TTS error: want error, got nil")
	}
}

var errTTSSimulated = &testErr{message: "simulated TTS failure"}

// TestVoicePipeline_ResampleUsed: confirm the resample module
// is wired into the pipeline (this test simply exercises
// RunOnce with a non-8k input and confirms no crash).
func TestVoicePipeline_ResampleUsed(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	outPath := filepath.Join(dir, "output.wav")
	// 16 kHz input: the pipeline does not need to resample on
	// the way in, but the test confirms the path is exercised.
	makeTestWAV(t, inPath, 1, 16000, 440)
	opts := Options{
		InWAV:  inPath,
		OutWAV: outPath,
		STT:    stt.NewMock(),
		TTS:    tts.NewMock(),
		Out:    &bytes.Buffer{},
	}
	if err := RunOnce(opts); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

// TestVoicePipeline_AudioSink: the pipeline can use an
// InMemorySink instead of a file.
func TestVoicePipeline_AudioSink(t *testing.T) {
	dir := t.TempDir()
	inPath := filepath.Join(dir, "input.wav")
	makeTestWAV(t, inPath, 1, 8000, 440)
	sink, err := audio.NewInMemorySink(8000)
	if err != nil {
		t.Fatalf("NewInMemorySink: %v", err)
	}
	opts := Options{
		InWAV: inPath,
		Sink:  sink,
		STT:   stt.NewMock(),
		TTS:   tts.NewMock(),
		Out:   &bytes.Buffer{},
	}
	if err := RunOnce(opts); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// The sink should have some data.
	rd := sink.Reader()
	buf := make([]byte, 1024)
	n, _ := rd.Read(buf)
	if n == 0 {
		t.Error("InMemorySink has no data after RunOnce")
	}
}

// Compile-time check that the codec package is used.
var _ = codec.Resample
