package rt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// SealKeySize is the key length Seal and Open take: AES-256.
const SealKeySize = 32

// Seal encrypts and authenticates data under key with AES-256-GCM, with
// label as additional data that Open must be given again: std/sessions'
// cookie. The result is a fresh random 12-byte nonce followed by the
// ciphertext and its 16-byte tag. A key that is not 32 bytes is an Err.
func Seal(key, data, label Bytes) Result[Bytes, string] {
	aead, err := sealCipher(key)
	if err != nil {
		return Err[Bytes](err.Error())
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(data)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return Err[Bytes]("reading a random nonce: " + err.Error())
	}
	return Ok[Bytes, string](Bytes(aead.Seal(nonce, nonce, []byte(data), []byte(label))))
}

// Open is the data Seal sealed under key with label, or None when sealed
// was made under another key or label, was changed, or is not a sealed
// value at all.
func Open(key, sealed, label Bytes) Maybe[Bytes] {
	aead, err := sealCipher(key)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return None[Bytes]()
	}
	nonce, body := []byte(sealed[:aead.NonceSize()]), []byte(sealed[aead.NonceSize():])
	data, err := aead.Open(nil, nonce, body, []byte(label))
	if err != nil {
		return None[Bytes]()
	}
	return Some(Bytes(data))
}

func sealCipher(key Bytes) (cipher.AEAD, error) {
	if len(key) != SealKeySize {
		return nil, fmt.Errorf("an AES-256 key is %d bytes, got %d", SealKeySize, len(key))
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
