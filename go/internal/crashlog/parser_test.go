// parser_test.go — TDD-first tests for the M5 crash-log parser
// (plan §9.3).
//
// The M5 firmware writes a small fixed-size binary record to
// /crash/<name>.bin on every panic / watchdog / brownout. On
// boot, the conv manager uploads each file to the base
// station as a kLog frame. The base station parses the blob
// and emits a structured slog entry.
//
// The on-disk format is mirrored from the C++ struct
// firmware/m5/components/crash_log/include/crash_log.h. It
// is a fixed 116-byte little-endian record:
//
//   uint32 magic            (4 bytes, 0x4D4F4F42)
//   uint32 reason           (4 bytes, ResetReason value)
//   uint32 boot_count       (4 bytes)
//   uint64 timestamp_unix_ms (8 bytes)
//   char   task_name[32]    (32 bytes, NUL-terminated)
//   char   note[64]         (64 bytes, NUL-terminated)
//
// All multi-byte fields are little-endian. The Go side decodes
// them with encoding/binary so the layout is independent of
// host endianness (the M5 is little-endian, and so is every
// host we care about, but we lock it down).

package crashlog_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jbutlerdev/tether/go/internal/crashlog"
)

const (
	kMagic         = uint32(0x4D4F4F42)
	kSizeOnDisk    = 116
)

// kKnownTimestamp is a fixed instant in time used as the
// timestamp in our test vectors. We pick 2024-06-15 12:00:00
// UTC (1718443200000 ms since the epoch) so the tests are
// stable across time zones.
var kKnownTimestamp = time.UnixMilli(1718443200000)

// encode builds the on-disk binary record from the fields.
// Mirrors the C++ struct layout byte-for-byte: the
// task_name and note fields are NUL-padded (not just
// truncated) because that's what the firmware writes.
func encode(reason, bootCount uint32, taskName, note string) []byte {
	buf := make([]byte, kSizeOnDisk)
	binary.LittleEndian.PutUint32(buf[0:4], kMagic)
	binary.LittleEndian.PutUint32(buf[4:8], reason)
	binary.LittleEndian.PutUint32(buf[8:12], bootCount)
	binary.LittleEndian.PutUint64(buf[12:20], uint64(kKnownTimestamp.UnixMilli()))
	// The 32-byte task_name field is NUL-padded.
	for i := 0; i < 32; i++ {
		if i < len(taskName) {
			buf[20+i] = taskName[i]
		} else {
			buf[20+i] = 0
		}
	}
	// The 64-byte note field is NUL-padded.
	for i := 0; i < 64; i++ {
		if i < len(note) {
			buf[52+i] = note[i]
		} else {
			buf[52+i] = 0
		}
	}
	return buf
}

// TestParse_HappyPath — a well-formed record round-trips.
func TestParse_HappyPath(t *testing.T) {
	t.Parallel()
	rec, err := crashlog.Parse(encode(3 /*kTaskWdt*/, 42, "audio_capture", "missed 5 feeds"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rec.Reason != 3 {
		t.Errorf("Reason: got %d, want 3", rec.Reason)
	}
	if rec.BootCount != 42 {
		t.Errorf("BootCount: got %d, want 42", rec.BootCount)
	}
	if rec.TaskName != "audio_capture" {
		t.Errorf("TaskName: got %q, want %q", rec.TaskName, "audio_capture")
	}
	if rec.Note != "missed 5 feeds" {
		t.Errorf("Note: got %q, want %q", rec.Note, "missed 5 feeds")
	}
	if rec.Timestamp.UnixMilli() != kKnownTimestamp.UnixMilli() {
		t.Errorf("Timestamp: got %v, want %v", rec.Timestamp, kKnownTimestamp)
	}
}

// TestParse_ReasonString — the human-readable label.
func TestParse_ReasonString(t *testing.T) {
	t.Parallel()
	for reason, want := range map[uint32]string{
		0:   "unknown",
		1:   "power-on",
		2:   "soft-restart",
		3:   "task-wdt",
		4:   "panic",
		5:   "brownout",
		99:  "unknown",
		255: "unknown",
	} {
		got := crashlog.ReasonString(reason)
		if got != want {
			t.Errorf("ReasonString(%d) = %q, want %q", reason, got, want)
		}
	}
}

// TestParse_TruncatedInput — a short buffer is rejected, not
// silently zero-padded.
func TestParse_TruncatedInput(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, 8, 50, kSizeOnDisk - 1} {
		_, err := crashlog.Parse(make([]byte, n))
		if err == nil {
			t.Errorf("Parse(%d bytes): expected error", n)
		}
	}
}

// TestParse_BadMagic — the magic prefix is wrong, the record
// is foreign. We refuse to decode it (the M5 might have
// written an old format and a future firmware version is
// uploading a different file).
func TestParse_BadMagic(t *testing.T) {
	t.Parallel()
	buf := encode(3, 1, "x", "y")
	// Tamper with the magic.
	binary.LittleEndian.PutUint32(buf[0:4], 0xDEADBEEF)
	_, err := crashlog.Parse(buf)
	if err == nil {
		t.Fatalf("Parse with bad magic: expected error")
	}
	if !errors.Is(err, crashlog.ErrBadMagic) {
		t.Errorf("Parse with bad magic: error %v is not ErrBadMagic", err)
	}
}

// TestParse_TaskNameTruncated — the on-disk task_name is 32
// bytes; a name longer than 31 chars (one byte for the NUL)
// is truncated. We test that the parser extracts the bytes
// verbatim, with the NUL terminator honored.
func TestParse_TaskNameTruncated(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("A", 100) // 100 A's
	rec, err := crashlog.Parse(encode(4, 1, long, ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// The on-disk buffer is 32 bytes. If the encoder fills
	// the field to capacity with no NUL, the parser returns
	// the entire 32-byte region. If the encoder leaves a
	// trailing NUL, the parser stops there. We accept either
	// (both are valid on-disk representations); the test
	// pins the field is at most 32 bytes and starts with
	// the first len(TaskName) A's of the input.
	if len(rec.TaskName) > 32 {
		t.Errorf("TaskName length: got %d, want ≤ 32", len(rec.TaskName))
	}
	if !strings.HasPrefix(long, rec.TaskName) {
		t.Errorf("TaskName: got %q, want prefix of %q", rec.TaskName, long)
	}
}

// TestParse_NoteTruncated — same for the 64-byte note field.
func TestParse_NoteTruncated(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("B", 200)
	rec, err := crashlog.Parse(encode(4, 1, "", long))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rec.Note) > 64 {
		t.Errorf("Note length: got %d, want ≤ 64", len(rec.Note))
	}
	if !strings.HasPrefix(long, rec.Note) {
		t.Errorf("Note: got %q, want prefix of %q", rec.Note, long)
	}
}

// TestParse_EmptyFields — an "all zeros" record is valid (the
// struct is initialised to zero; the magic and reason are
// non-zero in normal use but Parse must accept zero too).
func TestParse_EmptyFields(t *testing.T) {
	t.Parallel()
	buf := make([]byte, kSizeOnDisk)
	// Set the magic so it's not garbage.
	binary.LittleEndian.PutUint32(buf[0:4], kMagic)
	rec, err := crashlog.Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rec.Reason != 0 {
		t.Errorf("Reason: got %d, want 0", rec.Reason)
	}
	if rec.TaskName != "" {
		t.Errorf("TaskName: got %q, want \"\"", rec.TaskName)
	}
	if rec.Note != "" {
		t.Errorf("Note: got %q, want \"\"", rec.Note)
	}
}

// TestParse_NULTerminatesStrings — even if the source buffer
// has trailing garbage past the NUL, the parser stops at the
// first NUL.
func TestParse_NULTerminatesStrings(t *testing.T) {
	t.Parallel()
	buf := encode(3, 1, "audio_capture", "stuck")
	// Splat some garbage past the NUL terminator of task_name.
	for i := 33; i < 52; i++ {
		buf[20+i] = 0xCC
	}
	rec, err := crashlog.Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rec.TaskName != "audio_capture" {
		t.Errorf("TaskName: got %q, want %q", rec.TaskName, "audio_capture")
	}
}

// TestParse_BigEndianRejected — the on-disk format is
// little-endian. A big-endian encoding of the same fields
// must be rejected (we don't want a sloppy middleware to
// silently swap the byte order).
func TestParse_BigEndianRejected(t *testing.T) {
	t.Parallel()
	buf := make([]byte, kSizeOnDisk)
	binary.BigEndian.PutUint32(buf[0:4], kMagic) // wrong endian
	binary.BigEndian.PutUint32(buf[4:8], 3)
	_, err := crashlog.Parse(buf)
	if err == nil {
		t.Fatalf("Parse with big-endian magic: expected error")
	}
}

// TestParse_RealisticInput — a hand-crafted "production" record
// that includes every field at realistic values. The result
// must round-trip with byte-exact equality on the well-defined
// fields.
func TestParse_RealisticInput(t *testing.T) {
	t.Parallel()
	rec, err := crashlog.Parse(encode(
		4,    // kPanic
		137,  // boot_count
		"ui_state", "out of memory in render (heap full)",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if rec.Reason != 4 {
		t.Errorf("Reason: got %d, want 4", rec.Reason)
	}
	if rec.BootCount != 137 {
		t.Errorf("BootCount: got %d, want 137", rec.BootCount)
	}
	if rec.TaskName != "ui_state" {
		t.Errorf("TaskName: got %q", rec.TaskName)
	}
	if !bytes.HasSuffix([]byte(rec.Note), []byte("(heap full)")) {
		t.Errorf("Note suffix: got %q", rec.Note)
	}
}
