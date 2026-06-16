// File-based audio sinks (WAV writer and in-memory PCM buffer).
// See plan.md §6.8.
package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// WAV header constants.
const (
	wavHeaderSize  = 44
	wavFormatPCM   = 1
	wavChannels    = 1
	wavBitsPerSamp = 16
)

// FileSink writes mono 16-bit PCM to a WAV file. The header is
// written on Open; the data chunk length is patched on Close so
// streaming writes are safe.
type FileSink struct {
	mu        sync.Mutex
	f         *os.File
	closed    bool
	sampleR   int
	written   int64 // total PCM bytes written
	appending bool  // true if opened in append mode
}

// FileSinkOption configures a FileSink at construction time.
type FileSinkOption func(*FileSink)

// FileSinkAppend opens the file in append mode (reusing an
// existing WAV file's data section). The header is rewritten
// only if the file is brand new; if the file already exists and
// has a valid header, the data section is preserved and
// subsequent writes are appended.
func FileSinkAppend() FileSinkOption {
	return func(s *FileSink) { s.appending = true }
}

// NewFileSink opens (or creates) path for writing. If the file
// does not exist, a fresh WAV header is written. If it does and
// FileSinkAppend is used, writes are appended to the existing
// data section. The sample rate must be > 0.
func NewFileSink(path string, sampleRate int, opts ...FileSinkOption) (*FileSink, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("audio: invalid sample rate %d", sampleRate)
	}
	s := &FileSink{sampleR: sampleRate}
	for _, o := range opts {
		o(s)
	}

	var (
		f   *os.File
		err error
	)
	if s.appending {
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if st.Size() == 0 {
			// Brand new file; write a fresh header.
			if _, err := f.Write(wavHeader(sampleRate, 0)); err != nil {
				f.Close()
				return nil, err
			}
			s.written = 0
		} else if st.Size() < int64(wavHeaderSize) {
			f.Close()
			return nil, fmt.Errorf("audio: existing file %q is too small to be a WAV (%d bytes)", path, st.Size())
		} else {
			// Existing WAV file. Read the data chunk size from
			// the header and remember how many PCM bytes are
			// already there. We don't validate the rest of the
			// header; the append caller knows what they're doing.
			hdr := make([]byte, wavHeaderSize)
			if _, err := f.ReadAt(hdr, 0); err != nil {
				f.Close()
				return nil, err
			}
			dataSize := int64(binary.LittleEndian.Uint32(hdr[40:44]))
			s.written = dataSize
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				f.Close()
				return nil, err
			}
		}
	} else {
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(wavHeader(sampleRate, 0)); err != nil {
			f.Close()
			return nil, err
		}
	}
	s.f = f
	return s, nil
}

// Write pushes a chunk of mono 16-bit PCM.
func (s *FileSink) Write(pcm []int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("audio: write on closed FileSink")
	}
	if s.f == nil {
		return errors.New("audio: write on nil FileSink")
	}
	if len(pcm) == 0 {
		return nil
	}
	// Write each sample as little-endian int16.
	buf := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(v))
	}
	if _, err := s.f.Write(buf); err != nil {
		return err
	}
	s.written += int64(len(buf))
	return nil
}

// SampleRate returns the configured sample rate.
func (s *FileSink) SampleRate() int { return s.sampleR }

// Channels returns 1 (mono).
func (s *FileSink) Channels() int { return wavChannels }

// Close releases the file. On a successful close we patch the WAV
// header so the data chunk size is correct.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

// closeLocked is Close's body, split out so tests and the public
// Close share one path. Caller must hold s.mu.
func (s *FileSink) closeLocked() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.f == nil {
		return nil
	}
	// Patch the RIFF size (offset 4) and the data chunk size
	// (offset 40). Two seeks + writes keeps the header layout
	// correct.
	riffSize := uint32(s.written + int64(wavHeaderSize) - 8)
	dataSize := uint32(s.written)
	if err := s.writeHeaderField(4, riffSize); err != nil {
		return err
	}
	if err := s.writeHeaderField(40, dataSize); err != nil {
		return err
	}
	return s.f.Close()
}

// writeHeaderField writes a uint32 to a 4-byte slot in the WAV
// header. Caller must hold s.mu.
func (s *FileSink) writeHeaderField(offset int64, v uint32) error {
	if _, err := s.f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	return binary.Write(s.f, binary.LittleEndian, v)
}

// InMemorySink is a test-only sink that buffers all writes in
// memory. Reader() returns a reader over the raw PCM (no WAV
// header). Used by the end-to-end voice-pipeline tool and unit
// tests that need to inspect the audio without touching disk.
type InMemorySink struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	closed   bool
	sampleR  int
	channels int
}

// NewInMemorySink returns a fresh InMemorySink at the given
// sample rate (channels fixed to 1).
func NewInMemorySink(sampleRate int) (*InMemorySink, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("audio: invalid sample rate %d", sampleRate)
	}
	return &InMemorySink{sampleR: sampleRate, channels: 1}, nil
}

// Write appends pcm to the internal buffer.
func (s *InMemorySink) Write(pcm []int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("audio: write on closed InMemorySink")
	}
	for _, v := range pcm {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], uint16(v))
		s.buf.Write(b[:])
	}
	return nil
}

// SampleRate returns the configured sample rate.
func (s *InMemorySink) SampleRate() int { return s.sampleR }

// Channels returns 1 (mono).
func (s *InMemorySink) Channels() int { return s.channels }

// Close is a no-op for the in-memory sink.
func (s *InMemorySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Reader returns a fresh reader over the accumulated PCM bytes.
// The reader is a snapshot; subsequent Writes do not affect it.
func (s *InMemorySink) Reader() io.Reader {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.NewReader(s.buf.Bytes())
}

// wavHeader builds a 44-byte RIFF/WAVE header for mono 16-bit
// PCM at the given sample rate, with the data chunk size set to
// `dataSize` (0 if the caller will patch it later).
func wavHeader(sampleRate int, dataSize uint32) []byte {
	b := make([]byte, wavHeaderSize)
	// RIFF chunk descriptor
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(36+dataSize))
	copy(b[8:12], "WAVE")
	// fmt sub-chunk
	copy(b[12:16], "fmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16) // sub-chunk size
	binary.LittleEndian.PutUint16(b[20:22], wavFormatPCM)
	binary.LittleEndian.PutUint16(b[22:24], wavChannels)
	binary.LittleEndian.PutUint32(b[24:28], uint32(sampleRate))
	byteRate := uint32(sampleRate) * wavChannels * wavBitsPerSamp / 8
	binary.LittleEndian.PutUint32(b[28:32], byteRate)
	blockAlign := wavChannels * wavBitsPerSamp / 8
	binary.LittleEndian.PutUint16(b[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(b[34:36], wavBitsPerSamp)
	// data sub-chunk
	copy(b[36:40], "data")
	binary.LittleEndian.PutUint32(b[40:44], dataSize)
	return b
}
