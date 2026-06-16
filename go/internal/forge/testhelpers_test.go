// Test-only helpers for the forge package. The helpers
// duplicate small bits of logic from other packages so the
// tests can assert on them without dragging in heavy
// dependencies.
package forge_test

import "crypto/sha1"

// sha1Trunc16 returns the first 16 bytes of the SHA-1 of s.
// This is the same derivation used by conv.RoomIDToConvID; we
// re-implement it here to avoid importing the conv package
// from a forge test (the conv package's helper is closed
// over its own matrix/matrix package).
func sha1Trunc16(s string) []byte {
	h := sha1.Sum([]byte(s))
	out := make([]byte, 16)
	copy(out, h[:16])
	return out
}
