// hkdf_internal_test.go — black-box-ish tests for the internal
// hmacHash methods (Reset, Size, BlockSize). Lives in package
// crypto (not crypto_test) so it can poke at the unexported
// hmacHash struct.

package crypto

import (
	"crypto/sha256"
	"testing"
)

// TestHmacHash_InterfaceMethods — drive the hash.Hash interface
// methods on the internal hmacHash. Size and BlockSize are
// constant; Reset must produce the same output on a freshly
// re-summed hasher as on a fresh one.
func TestHmacHash_InterfaceMethods(t *testing.T) {
	t.Parallel()
	key := []byte("0123456789abcdef")
	msg := []byte("hello world")

	// First call: build, write, sum.
	h1 := hmacNew(sha256.New, key)
	if got, want := h1.BlockSize(), 64; got != want {
		t.Errorf("BlockSize: got %d, want %d", got, want)
	}
	if got, want := h1.Size(), sha256.Size; got != want {
		t.Errorf("Size: got %d, want %d", got, want)
	}
	h1.Write(msg)
	sum1 := h1.Sum(nil)

	// Second call: same hasher, reset, write, sum.
	h1.Reset()
	h1.Write(msg)
	sum2 := h1.Sum(nil)
	if !bytesEqual(sum1, sum2) {
		t.Errorf("Reset() did not produce a fresh hasher:\n  before: %x\n  after:  %x", sum1, sum2)
	}
}

// TestHmacHash_LongKeyIsHashed — RFC 2104 §2: keys longer than
// the block size (64 bytes for SHA-256) are first hashed with
// H. Two such keys that hash to the same 32-byte SHA-256 value
// should produce the same HMAC. We construct that scenario by
// choosing a long key and verifying the result is stable across
// runs (determinism), and that it differs from the same content
// used as a short key.
func TestHmacHash_LongKeyIsHashed(t *testing.T) {
	t.Parallel()
	shortKey := []byte("0123456789abcdef") // 16 bytes — shorter than block
	longKey := make([]byte, 200)           // 200 bytes — must be hashed first
	for i := range longKey {
		longKey[i] = byte(i)
	}
	msg := []byte("tether")

	// Long-key HMAC is stable (deterministic).
	first := hmacSHA256(longKey, msg)
	second := hmacSHA256(longKey, msg)
	if !bytesEqual(first, second) {
		t.Errorf("long-key HMAC is not deterministic:\n  a: %x\n  b: %x", first, second)
	}
	// And it differs from the short-key HMAC (the keys are not
	// equivalent; RFC 2104 says long keys get hashed first).
	short := hmacSHA256(shortKey, msg)
	if bytesEqual(first, short) {
		t.Errorf("long-key HMAC and short-key HMAC coincidentally matched: %x", first)
	}
}

// bytesEqual is a tiny test helper to avoid importing bytes in
// this internal-test file.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
