// crosscheck_test.go — helper for cross-validating our HKDF
// against the standard library's crypto/hkdf.
//
// This file is its own _test.go so the stdlib import only gets
// pulled in for the cross-validation test. (The M5 firmware never
// sees this import; the cross-check is purely a CI sanity.)
package crypto_test

import (
	"crypto/sha256"
	"testing"

	xhkdf "golang.org/x/crypto/hkdf"
)

// stdHKDF runs the stdlib's HKDF-SHA256 and returns the result.
// Factored out so the cross-validation test reads cleanly.
func stdHKDF(t *testing.T, ikm, salt, info []byte, length uint32) []byte {
	t.Helper()
	r := xhkdf.New(sha256.New, ikm, salt, info)
	out := make([]byte, length)
	if _, err := r.Read(out); err != nil {
		t.Fatalf("stdlib hkdf.Read: %v", err)
	}
	return out
}
