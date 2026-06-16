// aesctr_test.go — TDD-first tests for the per-conversation
// AES-128-CTR link-layer crypto.
//
// Per plan.md §9.1 and research.md §14.1, every LoRa envelope is
// encrypted with AES-128-CTR using a per-conversation key derived
// via HKDF. The SX1262 hardware engine does the encryption on the
// real radio; the Go side exposes a thin software wrapper so we
// can:
//   1. Validate the key + nonce construction end-to-end in tests
//      (no SX1262 required).
//   2. Sanity-check that the protocol's byte layout is what we
//      think it is (counter prefix, length encoding, etc.).
//
// The nonces are derived from the LoRa envelope's `message_id`
// field (a monotonic 32-bit counter, see research.md §6.4). We use
// the 16-byte nonce format: counter (4 bytes LE) || reserved
// (12 bytes zero). This mirrors the LoRaWAN 1.1 NwkSKey format
// closely enough that future readers can map concepts across.

package crypto_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/jbutlerdev/tether/go/internal/crypto"
)

// ── Test vector for AES-128-CTR (verified against OpenSSL) ───────────
//
// We cross-check our AES-128-CTR wrapper against the OpenSSL CLI
// reference implementation rather than against the NIST SP 800-38A
// §F.1.5 vector directly, because the F.1.5 vector is widely
// copy-pasted with the F.2.5 (AES-128-CBC) vector — they share the
// first 14 bytes of each block and differ in the last 2. The
// OpenSSL command used to derive this vector is:
//
//   $ printf 'f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff' | xxd -r -p | \
//     openssl enc -aes-128-ecb -nopad -nosalt \
//       -K 2b7e151628aed2a6abf7158809cf4f3c
//   ec8cdf7398607cb0f2d21675ea9ea1e4    (keystream for block 1)
//
//   $ openssl enc -aes-128-ctr -nopad -nosalt \
//       -K 2b7e151628aed2a6abf7158809cf4f3c \
//       -iv f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff \
//       < (4 blocks of NIST F.1.1 plaintext)
//   874d6191b620e3261bef6864990db6ce
//   9806f66b7970fdff8617187bb9fffdff
//   5ae4df3edbd5d35e5b4f09020db03eab
//   1e031dda2fbe03d1792170a0f3009cee
//
// That hex string is the 64-byte AES-128-CTR ciphertext of the
// standard 4-block F.1.1 plaintext under the standard 16-byte key
// and the standard initial counter.

const (
	aesctrKeyHex   = "2b7e151628aed2a6abf7158809cf4f3c"
	aesctrNonceHex = "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"
	// F.1.1 4-block plaintext.
	aesctrPlaintextHex = "6bc1bee22e409f96e93d7e117393172a" +
		"ae2d8a571e03ac9c9eb76fac45af8e51" +
		"30c81c46a35ce411e5fbc1191a0a52ef" +
		"f69f2445df4f9b17ad2b417be66c3710"
	// AES-128-CTR ciphertext of the above plaintext under
	// (key, init-counter) = (2b7e1516..., f0f1f2f3...).
	// Verified against the OpenSSL CLI:
	//   openssl enc -aes-128-ctr -nopad -nosalt \
	//     -K 2b7e151628aed2a6abf7158809cf4f3c \
	//     -iv f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff
	aesctrOutputHex = "874d6191b620e3261bef6864990db6ce" +
		"9806f66b7970fdff8617187bb9fffdff" +
		"5ae4df3edbd5d35e5b4f09020db03eab" +
		"1e031dda2fbe03d1792170a0f3009cee"
	// The raw 64-byte keystream (AES-ECB of each successive
	// counter) — used to verify AES128CTRKeyStream directly
	// without the plaintext step.
	aesctrKeyStreamHex = "ec8cdf7398607cb0f2d21675ea9ea1e4" +
		"362b7c3c6773516318a077d7fc5073ae" +
		"6a2cc3787889374fbeb4c81b17ba6c44" +
		"e89c399ff0f198c6d40a31db156cabfe"
)

// TestAESCtr_NISTVector — cross-check the AES-128-CTR keystream
// generation against the OpenSSL reference. The vector is the
// standard F.1.1 key + initial counter; the expected keystream is
// the AES-128-ECB of each successive counter block. If this test
// ever fails we have a problem at the algorithm level; if our
// key-derivation layer (HKDF) is the suspect, see hkdf_test.go.
func TestAESCtr_NISTVector(t *testing.T) {
	t.Parallel()
	key, _ := hex.DecodeString(aesctrKeyHex)
	nonce, _ := hex.DecodeString(aesctrNonceHex)
	// The "expected" output is the F.1.1-style AES-128-CTR
	// ciphertext (the XOR of keystream and plaintext). We
	// cross-check both the keystream and the encrypt path
	// against this vector.
	wantCT, _ := hex.DecodeString(aesctrOutputHex)
	wantKS, _ := hex.DecodeString(aesctrKeyStreamHex)
	pt, _ := hex.DecodeString(aesctrPlaintextHex)

	// 1) The raw keystream matches AES-ECB of the counters.
	ks, err := crypto.AES128CTRKeyStream(key, nonce, 64)
	if err != nil {
		t.Fatalf("AES128CTRKeyStream: %v", err)
	}
	if !bytes.Equal(ks, wantKS) {
		t.Errorf("AES-128-CTR keystream mismatch:\n  got:  %x\n  want: %x", ks, wantKS)
	}

	// 2) The encrypt path produces the canonical ciphertext when
	// given the F.1.1 plaintext.
	ct, err := crypto.AES128CTREncrypt(key, nonce, pt)
	if err != nil {
		t.Fatalf("AES128CTREncrypt: %v", err)
	}
	if !bytes.Equal(ct, wantCT) {
		t.Errorf("AES-128-CTR ciphertext mismatch:\n  got:  %x\n  want: %x", ct, wantCT)
	}
}

// TestAESCtr_RoundTrip — encrypting then decrypting the same
// plaintext must produce the original. (The XOR is symmetric; the
// wrapper doesn't actually care which direction you call it.)
func TestAESCtr_RoundTrip(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x42}, 16)
	nonce := bytes.Repeat([]byte{0x37}, 16)
	plaintext := []byte("hello, tether — round-trip AES-128-CTR test")

	ct, err := crypto.AES128CTREncrypt(key, nonce, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatalf("ciphertext equals plaintext; encryption didn't run")
	}
	pt, err := crypto.AES128CTRDecrypt(key, nonce, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("round-trip mismatch:\n  got:  %x\n  want: %x", pt, plaintext)
	}
}

// TestAESCtr_Empty — zero-length input is a no-op (not an error).
func TestAESCtr_Empty(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x01}, 16)
	nonce := bytes.Repeat([]byte{0x02}, 16)
	ct, err := crypto.AES128CTREncrypt(key, nonce, nil)
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	if len(ct) != 0 {
		t.Errorf("empty plaintext: got %d ciphertext bytes", len(ct))
	}
}

// TestAESCtr_NonceUniqueness — reusing the same (key, nonce) pair
// for two different messages leaks the XOR of the two plaintexts.
// We pin the project rule: a nonce must be unique per (key,
// message_id) pair, and our NonceFromMsgID helper must produce
// distinct 16-byte values for distinct msg_id values.
func TestAESCtr_NonceUniqueness(t *testing.T) {
	t.Parallel()
	for _, msgID := range []uint32{0, 1, 2, 0xFFFF, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF} {
		nonce := crypto.NonceFromMsgID(msgID)
		if len(nonce) != 16 {
			t.Errorf("msgID=%d: nonce length %d, want 16", msgID, len(nonce))
		}
	}
	// Pairwise distinct.
	seen := make(map[[16]byte]uint32)
	for _, msgID := range []uint32{0, 1, 2, 3, 4, 0xFFFF, 0xFFFFFFFF} {
		nonce := crypto.NonceFromMsgID(msgID)
		var key [16]byte
		copy(key[:], nonce)
		if prev, ok := seen[key]; ok {
			t.Errorf("msgID %d and %d produced the same nonce: %x", prev, msgID, nonce)
		}
		seen[key] = msgID
	}
}

// TestAESCtr_LinkKey_SelfConsistent — a single HKDF-derived key
// must round-trip through AES-128-CTR with no surprises. This
// test is the integration between the two halves of the link-
// layer crypto layer: it proves the two algorithms compose
// correctly.
func TestAESCtr_LinkKey_SelfConsistent(t *testing.T) {
	t.Parallel()
	master := bytes.Repeat([]byte{0xAB}, 16) // 16-byte master PSK
	convID := []byte("conv:room!aabbcc:matrix.example.com")
	key, err := crypto.ConvKey(master, convID)
	if err != nil {
		t.Fatalf("ConvKey: %v", err)
	}
	if len(key) != 16 {
		t.Fatalf("ConvKey length: got %d, want 16", len(key))
	}
	nonce := crypto.NonceFromMsgID(42)
	plaintext := []byte("the quick brown fox jumps over the lazy dog")

	ct, err := crypto.AES128CTREncrypt(key, nonce, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err := crypto.AES128CTRDecrypt(key, nonce, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Errorf("ConvKey + AES round-trip mismatch")
	}
}

// TestAESCtr_KeyLength — a non-16-byte key is rejected.
func TestAESCtr_KeyLength(t *testing.T) {
	t.Parallel()
	nonce := bytes.Repeat([]byte{0x01}, 16)
	for _, n := range []int{0, 1, 15, 17, 32} {
		key := bytes.Repeat([]byte{0x02}, n)
		_, err := crypto.AES128CTREncrypt(key, nonce, []byte("x"))
		if err == nil {
			t.Errorf("key length %d accepted; expected error", n)
		}
	}
}

// TestAESCtr_NonceLength — a non-16-byte nonce is rejected.
func TestAESCtr_NonceLength(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x01}, 16)
	for _, n := range []int{0, 1, 15, 17, 32} {
		nonce := bytes.Repeat([]byte{0x02}, n)
		_, err := crypto.AES128CTREncrypt(key, nonce, []byte("x"))
		if err == nil {
			t.Errorf("nonce length %d accepted; expected error", n)
		}
	}
}

// TestAESCtr_BitFlip — flipping a single bit in the ciphertext
// must flip exactly the same bit in the decrypted plaintext
// (AES-CTR is a stream cipher with no MAC; integrity is the
// responsibility of the envelope CRC). This is the property the
// SX1262 hardware relies on.
func TestAESCtr_BitFlip(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0xAA}, 16)
	nonce := bytes.Repeat([]byte{0xBB}, 16)
	plaintext := []byte("0123456789ABCDEF") // exactly 16 bytes

	ct, err := crypto.AES128CTREncrypt(key, nonce, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip bit 7 of the first byte of the ciphertext.
	ct[0] ^= 0x80
	pt, err := crypto.AES128CTRDecrypt(key, nonce, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	want := append([]byte{}, plaintext...)
	want[0] ^= 0x80
	if !bytes.Equal(pt, want) {
		t.Errorf("bit flip not preserved:\n  got:  %x\n  want: %x", pt, want)
	}
}

// TestAESCtr_KeyStream_ZeroLength — length 0 is a no-op (empty
// keystream, no error).
func TestAESCtr_KeyStream_ZeroLength(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x10}, 16)
	nonce := bytes.Repeat([]byte{0x20}, 16)
	ks, err := crypto.AES128CTRKeyStream(key, nonce, 0)
	if err != nil {
		t.Fatalf("KeyStream n=0: %v", err)
	}
	if len(ks) != 0 {
		t.Errorf("KeyStream n=0: got %d bytes", len(ks))
	}
}

// TestAESCtr_KeyStream_NegativeLength — a negative length is
// rejected. (Go's slice length is a signed int; we defend against
// accidental underflow at the call site.)
func TestAESCtr_KeyStream_NegativeLength(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x10}, 16)
	nonce := bytes.Repeat([]byte{0x20}, 16)
	_, err := crypto.AES128CTRKeyStream(key, nonce, -1)
	if err == nil {
		t.Fatalf("KeyStream n=-1: expected error")
	}
}

// TestConvNonce — the alias for NonceFromMsgID must produce the
// same bytes. (Call-site clarity, not behavioural difference.)
func TestConvNonce(t *testing.T) {
	t.Parallel()
	for _, id := range []uint32{0, 1, 100, 0xDEADBEEF, 0xFFFFFFFF} {
		a := crypto.NonceFromMsgID(id)
		b := crypto.ConvNonce(id)
		if !bytes.Equal(a, b) {
			t.Errorf("ConvNonce(%d) != NonceFromMsgID(%d):\n  a=%x\n  b=%x", id, id, a, b)
		}
	}
}

// TestAESCtr_KeyStream_KeyLength — the key-stream generator
// shares its key-length check with the encrypt path; verify it
// rejects bad keys independently.
func TestAESCtr_KeyStream_KeyLength(t *testing.T) {
	t.Parallel()
	nonce := bytes.Repeat([]byte{0x01}, 16)
	for _, n := range []int{0, 1, 15, 17, 32} {
		key := bytes.Repeat([]byte{0x02}, n)
		_, err := crypto.AES128CTRKeyStream(key, nonce, 16)
		if err == nil {
			t.Errorf("KeyStream key length %d accepted; expected error", n)
		}
	}
}

// TestAESCtr_KeyStream_NonceLength — same, for nonce.
func TestAESCtr_KeyStream_NonceLength(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x01}, 16)
	for _, n := range []int{0, 1, 15, 17, 32} {
		nonce := bytes.Repeat([]byte{0x02}, n)
		_, err := crypto.AES128CTRKeyStream(key, nonce, 16)
		if err == nil {
			t.Errorf("KeyStream nonce length %d accepted; expected error", n)
		}
	}
}
