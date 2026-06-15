// Tests for the audio sink interface and file implementation.
// See plan.md §6.8.
package audio_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/audio"
)

// TestFileSink_RoundTrip: write 1 s of mono PCM, read it back, equal.
func TestFileSink_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")

	const sampleRate = 8000
	const numSamples = 8000 // 1 s
	pcm := make([]int16, numSamples)
	for i := range pcm {
		pcm[i] = int16(i % 256)
	}

	s, err := audio.NewFileSink(path, sampleRate)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := s.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := s.SampleRate(); got != sampleRate {
		t.Errorf("SampleRate: want %d, got %d", sampleRate, got)
	}
	if got := s.Channels(); got != 1 {
		t.Errorf("Channels: want 1, got %d", got)
	}

	// Read the file back. The WAV header is 44 bytes, then
	// numSamples * 2 bytes of data.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	const header = 44
	if len(data) != header+2*numSamples {
		t.Fatalf("file size: want %d, got %d", header+2*numSamples, len(data))
	}
	for i := 0; i < numSamples; i++ {
		got := int16(binary.LittleEndian.Uint16(data[header+2*i:]))
		if got != pcm[i] {
			t.Errorf("sample %d: want %d, got %d", i, pcm[i], got)
			break
		}
	}
}

// TestFileSink_WAVHeader: the first 44 bytes are a valid RIFF/WAVE
// header with the right sample rate and channel count.
func TestFileSink_WAVHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	const sampleRate = 16000
	s, err := audio.NewFileSink(path, sampleRate)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := s.Write([]int16{0, 1, -1, 2, -2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) < 44 {
		t.Fatalf("file too small: %d", len(data))
	}
	if !bytes.Equal(data[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF tag: %q", data[0:4])
	}
	if !bytes.Equal(data[8:12], []byte("WAVE")) {
		t.Errorf("missing WAVE tag: %q", data[8:12])
	}
	if !bytes.Equal(data[12:16], []byte("fmt ")) {
		t.Errorf("missing fmt tag: %q", data[12:16])
	}
	if !bytes.Equal(data[36:40], []byte("data")) {
		t.Errorf("missing data tag: %q", data[36:40])
	}
	// Sample rate at offset 24 (uint32 LE).
	if got := binary.LittleEndian.Uint32(data[24:28]); got != sampleRate {
		t.Errorf("header sample rate: want %d, got %d", sampleRate, got)
	}
	// Channels at offset 22 (uint16 LE).
	if got := binary.LittleEndian.Uint16(data[22:24]); got != 1 {
		t.Errorf("header channels: want 1, got %d", got)
	}
	// Bits per sample at offset 34 (uint16 LE).
	if got := binary.LittleEndian.Uint16(data[34:36]); got != 16 {
		t.Errorf("header bits per sample: want 16, got %d", got)
	}
}

// TestFileSink_Appendable: opening the same path with the append
// option writes a second chunk that continues the first.
func TestFileSink_Appendable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	const sampleRate = 8000

	s1, err := audio.NewFileSink(path, sampleRate)
	if err != nil {
		t.Fatalf("NewFileSink #1: %v", err)
	}
	if err := s1.Write([]int16{1, 2, 3, 4}); err != nil {
		t.Fatalf("Write #1: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}

	s2, err := audio.NewFileSink(path, sampleRate, audio.FileSinkAppend())
	if err != nil {
		t.Fatalf("NewFileSink (append): %v", err)
	}
	if err := s2.Write([]int16{5, 6, 7, 8}); err != nil {
		t.Fatalf("Write #2: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// 44 byte header + 8 * 2 bytes of PCM.
	want := 44 + 8*2
	if len(data) != want {
		t.Errorf("file size after append: want %d, got %d", want, len(data))
	}
	// Sample sequence is 1..8.
	for i := 0; i < 8; i++ {
		got := int16(binary.LittleEndian.Uint16(data[44+2*i:]))
		if got != int16(i+1) {
			t.Errorf("sample %d: want %d, got %d", i, i+1, got)
		}
	}
}

// TestFileSink_Empty: zero writes is allowed and produces a valid
// header-only file.
func TestFileSink_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != 44 {
		t.Errorf("file size: want 44, got %d", len(data))
	}
	if !bytes.Equal(data[0:4], []byte("RIFF")) {
		t.Errorf("missing RIFF tag")
	}
}

// TestFileSink_CloseIdempotent: Close twice is allowed.
func TestFileSink_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close #2: %v", err)
	}
	// Writes after Close are errors.
	if err := s.Write([]int16{1}); err == nil {
		t.Error("Write after Close: want error, got nil")
	}
}

// TestFileSink_OpenMissingDir: writing to a non-existent directory
// fails at NewFileSink time.
func TestFileSink_OpenMissingDir(t *testing.T) {
	_, err := audio.NewFileSink("/nonexistent/dir/out.wav", 8000)
	if err == nil {
		t.Fatal("NewFileSink on missing dir: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no such file or directory") &&
		!strings.Contains(err.Error(), "cannot find the path") {
		// Just confirm we got *some* error.
		t.Logf("got expected error: %v", err)
	}
}

// TestFileSink_Concurrent: race-detector clean.
func TestFileSink_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := s.Write([]int16{1, 2, 3, 4}); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestFileSink_StreamsLarge: a large write succeeds and the file
// contains the expected number of bytes.
func TestFileSink_StreamsLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	chunk := make([]int16, 4096)
	const chunks = 100
	for i := 0; i < chunks; i++ {
		if err := s.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := int64(44 + 2*len(chunk)*chunks)
	if st.Size() != want {
		t.Errorf("file size: want %d, got %d", want, st.Size())
	}
}

// TestFileSink_DropInMemory: the InMemory sink accepts writes and
// the resulting PCM matches.
func TestFileSink_InMemory(t *testing.T) {
	s, err := audio.NewInMemorySink(22050)
	if err != nil {
		t.Fatalf("NewInMemorySink: %v", err)
	}
	if s.SampleRate() != 22050 {
		t.Errorf("SampleRate: want 22050, got %d", s.SampleRate())
	}
	if s.Channels() != 1 {
		t.Errorf("Channels: want 1, got %d", s.Channels())
	}
	pcm := []int16{1, 2, 3, 4, 5}
	if err := s.Write(pcm); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := io.ReadAll(s.Reader())
	if err != nil {
		t.Fatalf("Reader: %v", err)
	}
	if len(got) != 2*len(pcm) {
		t.Errorf("Reader size: want %d, got %d", 2*len(pcm), len(got))
	}
	for i, v := range pcm {
		g := int16(binary.LittleEndian.Uint16(got[2*i:]))
		if g != v {
			t.Errorf("sample %d: want %d, got %d", i, v, g)
		}
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent.
	if err := s.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
	// Write after Close is an error.
	if err := s.Write(pcm); err == nil {
		t.Error("Write after Close: want error, got nil")
	}
}

// TestFileSink_BadSampleRate: NewFileSink refuses a 0 or negative
// sample rate.
func TestFileSink_BadSampleRate(t *testing.T) {
	if _, err := audio.NewFileSink("/tmp/x.wav", 0); err == nil {
		t.Error("NewFileSink(0): want error, got nil")
	}
	if _, err := audio.NewInMemorySink(-1); err == nil {
		t.Error("NewInMemorySink(-1): want error, got nil")
	}
}

// TestFileSink_AppendToSmallFile: an existing file too small to be
// a WAV is rejected with an error.
func TestFileSink_AppendToSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.wav")
	// Write a 20-byte file (less than the 44-byte WAV header).
	if err := os.WriteFile(path, []byte("0123456789ABCDEFGHIJ"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := audio.NewFileSink(path, 8000, audio.FileSinkAppend()); err == nil {
		t.Fatal("NewFileSink (append small file): want error, got nil")
	}
}

// TestFileSink_WriteEmpty: writing an empty buffer is a no-op, no
// error.
func TestFileSink_WriteEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	defer s.Close()
	if err := s.Write(nil); err != nil {
		t.Errorf("Write(nil): %v", err)
	}
	if err := s.Write([]int16{}); err != nil {
		t.Errorf("Write([]int16{}): %v", err)
	}
}

// TestFileSink_AppendThenRead: writes a chunk, closes, reopens in
// append mode, writes another chunk, closes, and verifies the
// combined file's data section is the concatenation.
func TestFileSink_AppendThenRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")
	s1, err := audio.NewFileSink(path, 8000)
	if err != nil {
		t.Fatalf("NewFileSink #1: %v", err)
	}
	for _, v := range []int16{10, 20, 30} {
		if err := s1.Write([]int16{v}); err != nil {
			t.Fatalf("Write #1: %v", err)
		}
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	s2, err := audio.NewFileSink(path, 8000, audio.FileSinkAppend())
	if err != nil {
		t.Fatalf("NewFileSink (append): %v", err)
	}
	for _, v := range []int16{40, 50, 60} {
		if err := s2.Write([]int16{v}); err != nil {
			t.Fatalf("Write #2: %v", err)
		}
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := []int16{10, 20, 30, 40, 50, 60}
	if len(data) != 44+2*len(want) {
		t.Errorf("file size: want %d, got %d", 44+2*len(want), len(data))
	}
	for i, v := range want {
		got := int16(binary.LittleEndian.Uint16(data[44+2*i:]))
		if got != v {
			t.Errorf("sample %d: want %d, got %d", i, v, got)
		}
	}
	// Header data-size and RIFF size must match the new total.
	dataSize := binary.LittleEndian.Uint32(data[40:44])
	if dataSize != uint32(2*len(want)) {
		t.Errorf("header data size: want %d, got %d", 2*len(want), dataSize)
	}
	riffSize := binary.LittleEndian.Uint32(data[4:8])
	if riffSize != uint32(36+2*len(want)) {
		t.Errorf("header RIFF size: want %d, got %d", 36+2*len(want), riffSize)
	}
}

// Compile-time check.
var _ audio.Sink = (*audio.FileSink)(nil)
var _ audio.Sink = (*audio.InMemorySink)(nil)
