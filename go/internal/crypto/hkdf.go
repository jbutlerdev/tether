// Package crypto holds the Tether per-conversation crypto primitives.
//
// The link-layer encryption model (research.md §14.1, plan §9.1) is:
//
//	conv_key = HKDF-SHA256(masterPSK, salt=convID, info="tether-link-v1")
//
// The 16-byte conv_key is then used as the AES-128-CTR key on the
// SX1262. We deliberately keep both ends of the link (the M5
// firmware and the Go daemon) free of the standard library's
// crypto/hkdf — the M5 ESP-IDF build cannot import the net package,
// and a 30-line self-contained implementation is well-tested and
// audit-friendly. The wire compatibility is pinned by the three
// RFC 5869 §A test vectors in hkdf_test.go.
package crypto

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
)

// MaxOKMLen is the largest output length HKDFSHA256 will produce.
// RFC 5869 §2.3 limits the OKM to 255 * HashLen = 255 * 32 = 8160
// bytes for SHA-256.
const MaxOKMLen = 255 * sha256.Size

// Sentinel errors.
var (
	// ErrLengthTooLarge is returned when the requested OKM length
	// exceeds RFC 5869's 255 * HashLen ceiling.
	ErrLengthTooLarge = errors.New("crypto: requested OKM length exceeds 255*HashLen")
	// ErrMasterPSKTooShort is returned by ConvKey when the master
	// PSK is not at least 16 bytes (the AES-128 key width).
	ErrMasterPSKTooShort = errors.New("crypto: master PSK must be at least 16 bytes")
	// ErrNilInput is returned when a required input is nil.
	ErrNilInput = errors.New("crypto: nil input is not allowed")
)

// HKDFSHA256 implements the HKDF (RFC 5869) Extract-then-Expand
// with HMAC-SHA256.
//
// Parameters (RFC 5869 §2.2):
//
//	ikm   — input keying material (the master PSK in our case)
//	salt — optional, non-secret salt; empty means "use HashLen zeros"
//	info — optional context/info string
//	length — desired OKM length in bytes (1..255*HashLen)
//
// Returns the OKM or one of the package sentinel errors.
func HKDFSHA256(ikm, salt, info []byte, length uint32) ([]byte, error) {
	if length == 0 {
		return []byte{}, nil
	}
	if length > MaxOKMLen {
		return nil, fmt.Errorf("requested %d bytes, max %d: %w", length, MaxOKMLen, ErrLengthTooLarge)
	}

	// RFC 5869 §2.2: a nil/empty salt is replaced with HashLen zeros.
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}

	// Extract: PRK = HMAC-SHA256(salt, IKM)
	prk := hmacSHA256(salt, ikm)

	// Expand: OKM = T(1) || T(2) || ... || T(N) where
	//   T(0) = empty
	//   T(i) = HMAC-SHA256(PRK, T(i-1) || info || i)
	//
	// We always feed at least one byte of info-or-counter; an empty
	// info is fine.
	out := make([]byte, 0, length)
	var t []byte
	var counter byte = 1
	for uint32(len(out)) < length {
		t = hmacSHA256Expanding(prk, t, info, counter)
		out = append(out, t...)
		counter++
	}
	return out[:length], nil
}

// ConvKey derives the per-conversation AES-128 key from a master
// PSK and a conversation ID. It is the canonical entry point used
// by the sync layer when provisioning a new conversation and by the
// M5 firmware on boot when loading a conversation from LittleFS.
//
// The info string is fixed ("tether-link-v1") so the same
// (masterPSK, convID) pair always derives to the same key, and so
// future domain-separated uses (e.g. an E2EE channel) can use a
// different info string without colliding.
//
// Returns ErrMasterPSKTooShort if master is < 16 bytes.
func ConvKey(master, convID []byte) ([]byte, error) {
	if len(master) < 16 {
		return nil, ErrMasterPSKTooShort
	}
	// 16 bytes output = AES-128. The SX1262 hardware engine
	// consumes 16 bytes of key and a 16-byte nonce; the nonce is
	// the LoRa packet's `msg_id` (per research.md §6.4).
	return HKDFSHA256(master, convID, []byte(convKeyInfo), 16)
}

// convKeyInfo is the canonical info string for the Tether
// link-layer key. Changing this value invalidates every previously
// derived key, so it must only change as part of a versioned
// protocol upgrade.
const convKeyInfo = "tether-link-v1"

// ── Internal HMAC-SHA256 helpers ──────────────────────────────────────

// hmacSHA256 returns HMAC-SHA256(key, msg). RFC 2104. We
// deliberately do NOT use crypto/hmac from the std library so the
// M5 firmware (which doesn't link net) can carry the same
// algorithm. The implementation is a textbook RFC 2104 HMAC.
func hmacSHA256(key, msg []byte) []byte {
	return hmacSum(sha256.New, key, msg)
}

// hmacSHA256Expanding returns HMAC-SHA256(key, a || b || c) where
// a, b, c are concatenated in order. Used by the HKDF Expand step
// where the input is T(i-1) || info || counter-byte.
func hmacSHA256Expanding(key, t, info []byte, counter byte) []byte {
	hs := hmacNew(sha256.New, key)
	hs.Write(t)
	hs.Write(info)
	hs.Write([]byte{counter})
	return hs.Sum(nil)
}

// hmacNew is split out for the test in internal HMAC: it returns
// an HMAC hasher ready to be written to. The hmacHash implements
// hash.Hash so it is testable through the standard interface.
//
// h must be a constructor that returns a fresh hash.Hash.
func hmacNew(h func() hash.Hash, key []byte) hash.Hash {
	const blockSize = 64 // SHA-256 block size
	// RFC 2104 §2: keys longer than the block size are hashed
	// first; shorter keys are zero-padded to block size.
	k := key
	if len(k) > blockSize {
		hh := h()
		hh.Write(k)
		k = hh.Sum(nil)
	}
	if len(k) < blockSize {
		padded := make([]byte, blockSize)
		copy(padded, k)
		k = padded
	}
	ipad := make([]byte, blockSize)
	opad := make([]byte, blockSize)
	for i := 0; i < blockSize; i++ {
		ipad[i] = k[i] ^ 0x36
		opad[i] = k[i] ^ 0x5c
	}
	inner := h()
	inner.Write(ipad)
	return &hmacHash{
		outer: h(),
		inner: inner,
		ipad:  ipad,
		opad:  opad,
	}
}

// hmacHash implements hash.Hash for HMAC-SHA256 in the
// "write-then-sum" usage pattern used by HKDF. We expose
// Reset/Size/BlockSize for interface compliance and to enable
// call-site reuse (not currently exercised by HKDF, but available
// for callers that want to reuse a hasher).
//
// The inner hasher is a plain SHA-256; we prepend ipad on every
// Reset and on construction so that a Sum() always operates on
// (ipad || msg). This is the textbook HMAC pattern.
type hmacHash struct {
	outer hash.Hash
	inner hash.Hash
	ipad  []byte
	opad  []byte
}

func (s *hmacHash) Write(p []byte) (int, error) { return s.inner.Write(p) }
func (s *hmacHash) Sum(b []byte) []byte {
	innerSum := s.inner.Sum(nil)
	s.outer.Reset()
	s.outer.Write(s.opad)
	s.outer.Write(innerSum)
	return s.outer.Sum(b)
}
func (s *hmacHash) Reset() {
	s.inner.Reset()
	s.inner.Write(s.ipad) // re-prepend ipad so the next Sum is HMAC(msg), not HMAC(msg')
	s.outer.Reset()
}
func (s *hmacHash) Size() int      { return s.outer.Size() }
func (s *hmacHash) BlockSize() int { return 64 }

// hmacSum is a one-shot helper that mirrors hmacSHA256 but accepts
// the constructor directly. Used by HKDFSHA256.
func hmacSum(h func() hash.Hash, key, msg []byte) []byte {
	hs := hmacNew(h, key)
	hs.Write(msg)
	return hs.Sum(nil)
}
