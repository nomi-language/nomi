package stdcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// cbcHMAC is AES-CBC with HMAC-SHA-2 as one authenticated algorithm, RFC
// 7518 section 5.2's construction, as a cipher.AEAD so it seals and opens
// through the same entry points as the AEADs Go has. Its key is the MAC key
// followed by the encryption key, of equal length: 32 bytes for
// AES_128_CBC_HMAC_SHA_256, 48 for AES_192_CBC_HMAC_SHA_384 and 64 for
// AES_256_CBC_HMAC_SHA_512. The nonce is the 16-byte IV. The plaintext is
// padded with PKCS #7, and the tag is the HMAC over the associated data,
// the IV, the ciphertext and the associated data's length in bits as 64
// big-endian bits, cut to half the hash's size. Plain CBC never leaves
// this type: Open checks the tag, in constant time, before it decrypts, so
// every failure, a bad tag or bad padding under a good tag, takes one path
// and answers nothing about the plaintext.
type cbcHMAC struct {
	mac, enc []byte
	hash     func() hash.Hash
	tagSize  int
}

// newCBCHMAC is the construction its key's length names.
func newCBCHMAC(key []byte) (cipher.AEAD, error) {
	var h func() hash.Hash
	switch len(key) {
	case 32:
		h = sha256.New
	case 48:
		h = sha512.New384
	case 64:
		h = sha512.New
	default:
		return nil, fmt.Errorf("an AES-CBC-HMAC key is 32, 48 or 64 bytes, got %d", len(key))
	}
	half := len(key) / 2
	return cbcHMAC{mac: key[:half], enc: key[half:], hash: h, tagSize: half}, nil
}

func (c cbcHMAC) NonceSize() int { return aes.BlockSize }

// Overhead is the most a seal adds: a whole block of padding and the tag.
func (c cbcHMAC) Overhead() int { return aes.BlockSize + c.tagSize }

func (c cbcHMAC) block() cipher.Block {
	b, err := aes.NewCipher(c.enc)
	if err != nil {
		panic(err) // the key's length was checked
	}
	return b
}

// tag is the HMAC over associated, iv, ciphertext and associated's length
// in bits, cut to the tag's size.
func (c cbcHMAC) tag(associated, iv, ciphertext []byte) []byte {
	m := hmac.New(c.hash, c.mac)
	m.Write(associated)
	m.Write(iv)
	m.Write(ciphertext)
	var bits [8]byte
	binary.BigEndian.PutUint64(bits[:], uint64(len(associated))*8)
	m.Write(bits[:])
	return m.Sum(nil)[:c.tagSize]
}

func (c cbcHMAC) Seal(dst, nonce, plaintext, associated []byte) []byte {
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	ciphertext := make([]byte, len(plaintext)+pad)
	copy(ciphertext, plaintext)
	for i := len(plaintext); i < len(ciphertext); i++ {
		ciphertext[i] = byte(pad)
	}
	cipher.NewCBCEncrypter(c.block(), nonce).CryptBlocks(ciphertext, ciphertext)
	dst = append(dst, ciphertext...)
	return append(dst, c.tag(associated, nonce, ciphertext)...)
}

var errOpen = errors.New("cipher: message authentication failed")

func (c cbcHMAC) Open(dst, nonce, sealed, associated []byte) ([]byte, error) {
	if len(sealed) < c.tagSize {
		return nil, errOpen
	}
	ciphertext, tag := sealed[:len(sealed)-c.tagSize], sealed[len(sealed)-c.tagSize:]
	if !hmac.Equal(tag, c.tag(associated, nonce, ciphertext)) {
		return nil, errOpen
	}
	// The tag holds, so the ciphertext is the one sealed under this key,
	// and a wrong length or padding below is no oracle: only a holder of
	// the key could make one.
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errOpen
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(c.block(), nonce).CryptBlocks(plaintext, ciphertext)
	pad := int(plaintext[len(plaintext)-1])
	if pad < 1 || pad > aes.BlockSize {
		return nil, errOpen
	}
	for _, b := range plaintext[len(plaintext)-pad:] {
		if int(b) != pad {
			return nil, errOpen
		}
	}
	return append(dst, plaintext[:len(plaintext)-pad]...), nil
}
