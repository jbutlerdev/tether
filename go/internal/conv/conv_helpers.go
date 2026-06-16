// Helper for the conv package. The SHA-1 derivation is in here so
// the test file can stub it via roomIDToConvID = ...
package conv

import (
	"crypto/sha1"
)

// sha1Of is a thin wrapper so the roomIDToConvID closure can be
// swapped in tests.
var sha1Of = func(b []byte) []byte {
	h := sha1.Sum(b)
	return h[:]
}
