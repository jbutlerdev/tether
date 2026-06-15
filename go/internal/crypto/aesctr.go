// aesctr.go — per-conversation AES-128-CTR link-layer crypto.
//
// The Tether radio layer (research.md §6.4, plan §9.1) encrypts
// every LoRa envelope with AES-128-CTR using a per-conversation
// key derived from a master PSK via HKDF-SHA256:
//
//	conv_key = HKDF(masterPSK, salt=convID, info="tether-link-v1")
//	nonce    = (msg_id as 4-byte LE) || (12 bytes of zero)
//
// The SX1262 hardware engine does the on-air encryption, so this
// file's purpose is to provide a software implementation that
// lets us:
//
//   1. Pin the project's key/nonce construction (so a typo in
//      `NonceFromMsgID` is caught in CI, not on the bench).
//   2. Cross-check the on-the-wire byte layout for future
//      firmware ports.
//   3. Allow off-air unit tests of the protocol layer without a
//      real radio.
//
// The wrapper uses crypto/aes + crypto/cipher from the standard
// library. There is no need to re-implement AES in this package.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
)

// NonceSize is the 16-byte nonce layout used by the Tether link
// layer. The first 4 bytes are the 32-bit message id (little-
// endian); the remaining 12 bytes are zero. The 12-byte suffix is
// the standard "reserved" tail that aligns with the LoRaWAN 1.1
// NwkSKey block-cipher nonce format.
const NonceSize = 16

// KeySize is the AES-128 key length.
const KeySize = 16

// Sentinel errors for the AES-128-CTR layer.
var (
	ErrKeyLength   = errors.New("crypto: AES-128 key must be 16 bytes")
	ErrNonceLength = errors.New("crypto: AES-128-CTR nonce must be 16 bytes")
)

// NonceFromMsgID builds the 16-byte link-layer nonce for a given
// 32-bit message id. The message id is the LoRa envelope's
// `message_id` field, a monotonic counter assigned by the sender
// (see research.md §6.4 and plan §1.4).
//
// Layout (16 bytes):
//
//	[0..3]   uint32 message id, little-endian
//	[4..15]  12 bytes of zero (reserved)
//
// The reserved tail is zero in v1; future protocol versions may
// split it into a sender-id field or a session-id field. v1 just
// keeps it zero to minimise header bloat.
func NonceFromMsgID(msgID uint32) []byte {
	nonce := make([]byte, NonceSize)
	binary.LittleEndian.PutUint32(nonce[0:4], msgID)
	// bytes 4..15 are already zero.
	return nonce
}

// AES128CTRKeyStream generates `length` bytes of AES-128-CTR
// keystream starting at the given 16-byte nonce (treated as the
// initial counter block). This is the raw keystream — XOR it
// with the plaintext to encrypt, or with the ciphertext to
// decrypt.
func AES128CTRKeyStream(key, nonce []byte, length int) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key length %d: %w", len(key), ErrKeyLength)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("nonce length %d: %w", len(nonce), ErrNonceLength)
	}
	if length < 0 {
		return nil, errors.New("crypto: length must be non-negative")
	}
	if length == 0 {
		return []byte{}, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	stream := cipher.NewCTR(block, nonce)
	out := make([]byte, length)
	stream.XORKeyStream(out, out) // XOR with itself = keystream
	return out, nil
}

// AES128CTREncrypt encrypts plaintext under key+nonce using
// AES-128-CTR. Returns ciphertext of the same length.
//
// AES-CTR is symmetric: encrypt and decrypt are the same XOR
// with the keystream. We expose two functions for clarity at the
// call site, not because they do different work.
func AES128CTREncrypt(key, nonce, plaintext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key length %d: %w", len(key), ErrKeyLength)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("nonce length %d: %w", len(nonce), ErrNonceLength)
	}
	if len(plaintext) == 0 {
		return []byte{}, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	stream := cipher.NewCTR(block, nonce)
	out := make([]byte, len(plaintext))
	stream.XORKeyStream(out, plaintext)
	return out, nil
}

// AES128CTRDecrypt decrypts ciphertext under key+nonce. Same
// operation as encrypt (CTR is symmetric).
func AES128CTRDecrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	return AES128CTREncrypt(key, nonce, ciphertext)
}

// ConvNonce builds the link-layer nonce for a (convID, msgID)
// pair. It is a thin wrapper that exists for call-site clarity:
// the nonce is fully determined by msgID, so this is just
// NonceFromMsgID(msgID) with a more descriptive name.
func ConvNonce(msgID uint32) []byte {
	return NonceFromMsgID(msgID)
}
