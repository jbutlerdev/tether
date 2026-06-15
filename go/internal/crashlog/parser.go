// Package crashlog decodes the M5 firmware's crash-record
// binary format. See plan.md §9.3 and
// firmware/m5/components/crash_log/include/crash_log.h for the
// canonical layout.
package crashlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
)

// SizeOnDisk is the byte length of a serialised CrashRecord.
const SizeOnDisk = 116

// Magic is the 4-byte little-endian prefix that identifies a
// valid crash record. The constant is shared with the C++
// implementation (firmware/m5/components/crash_log/include/
// crash_log.h).
const Magic = 0x4D4F4F42

// Sentinel errors. The conv manager matches on these with
// errors.Is.
var (
	// ErrBadMagic is returned by Parse when the first 4 bytes
	// of the input are not the expected magic constant. The
	// record is treated as foreign and skipped.
	ErrBadMagic = errors.New("crashlog: bad magic prefix")
	// ErrTruncated is returned by Parse when the input buffer
	// is shorter than SizeOnDisk.
	ErrTruncated = errors.New("crashlog: input truncated")
)

// Record is the decoded form of one M5 crash entry. The fields
// mirror the C++ struct; their interpretation is documented in
// plan §9.3.
type Record struct {
	Reason    uint32    // ResetReason value (1..5 on the wire)
	BootCount uint32    // which boot produced this crash
	Timestamp time.Time // wall-clock time of the crash
	TaskName  string    // empty for non-task crashes
	Note      string    // free-form annotation
}

// Parse decodes a single 116-byte crash-record blob. The
// returned Record is zero-valued on error.
//
// Errors:
//   - ErrTruncated if buf is shorter than SizeOnDisk
//   - ErrBadMagic if the magic prefix doesn't match
func Parse(buf []byte) (Record, error) {
	var rec Record
	if len(buf) < SizeOnDisk {
		return rec, ErrTruncated
	}
	magic := binary.LittleEndian.Uint32(buf[0:4])
	if magic != Magic {
		return rec, ErrBadMagic
	}
	rec.Reason = binary.LittleEndian.Uint32(buf[4:8])
	rec.BootCount = binary.LittleEndian.Uint32(buf[8:12])
	ts := binary.LittleEndian.Uint64(buf[12:20])
	rec.Timestamp = time.UnixMilli(int64(ts))
	// Strings are NUL-terminated; we stop at the first NUL or
	// the end of the field. The fields are fixed-size on disk
	// so a future addition must bump Magic and add a new
	// layout; this parser is intentionally strict.
	rec.TaskName = cstring(buf[20:52])
	rec.Note = cstring(buf[52:116])
	return rec, nil
}

// cstring extracts a NUL-terminated string from a fixed-size
// byte slice. If no NUL is present the entire slice is
// returned as a string (the parser accepts the on-disk
// truncation rule: the field is NUL-terminated iff the
// caller wrote a NUL).
func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// ReasonString returns a human-readable label for a ResetReason
// value. Unknown codes (and zero) map to "unknown".
func ReasonString(reason uint32) string {
	switch reason {
	case 1:
		return "power-on"
	case 2:
		return "soft-restart"
	case 3:
		return "task-wdt"
	case 4:
		return "panic"
	case 5:
		return "brownout"
	default:
		return "unknown"
	}
}
