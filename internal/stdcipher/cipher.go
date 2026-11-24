package stdcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Engines, as std/cipher's `engine` numbers an Algorithm's construction.
const (
	engineAESGCM            = 0
	engineChaCha20Poly1305  = 1
	engineXChaCha20Poly1305 = 2
	engineAESCBCHMAC        = 3
)

// Seal is `std::cipher::host.seal`: plaintext encrypted under key with
// nonce by the engine's AEAD, associated authenticated with it, as the
// ciphertext followed by the tag.
func Seal(engine int64, key, nonce, plaintext, associated []byte) []byte {
	return aead(engine, key).Seal(nil, nonce, plaintext, associated)
}

// Open is `std::cipher::host.open`: the plaintext sealed is the ciphertext
// and tag of, under key, nonce and associated, or nil when the tag does not
// verify. Every way the bytes can fail to be the ones sealed takes the one
// path, the AEAD's own constant-time check.
func Open(engine int64, key, nonce, sealed, associated []byte) *[]byte {
	plaintext, err := aead(engine, key).Open(nil, nonce, sealed, associated)
	if err != nil {
		return nil
	}
	if plaintext == nil {
		plaintext = []byte{}
	}
	return &plaintext
}

// AESCTR is `std::cipher::host.aes_ctr`: data XORed with the AES-CTR
// keystream of key (16, 24 or 32 bytes) from the 16-byte counter block.
// Encrypting and decrypting are the same operation, and nothing is
// authenticated.
func AESCTR(key, counter, data []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	cipher.NewCTR(block, counter).XORKeyStream(out, data)
	return out
}

// aead is the engine's AEAD over key. std/cipher checked the key's length,
// so an error here is a broken invariant.
func aead(engine int64, key []byte) cipher.AEAD {
	var (
		a   cipher.AEAD
		err error
	)
	switch engine {
	case engineAESGCM:
		var block cipher.Block
		if block, err = aes.NewCipher(key); err == nil {
			a, err = cipher.NewGCM(block)
		}
	case engineChaCha20Poly1305:
		a, err = chacha20poly1305.New(key)
	case engineXChaCha20Poly1305:
		a, err = chacha20poly1305.NewX(key)
	case engineAESCBCHMAC:
		a, err = newCBCHMAC(key)
	default:
		err = fmt.Errorf("no cipher engine %d", engine)
	}
	if err != nil {
		panic(err)
	}
	return a
}
