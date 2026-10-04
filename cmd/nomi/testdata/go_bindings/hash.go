package gobindings

import (
	"crypto/sha256"
	"encoding/hex"
)

func SHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}

func HexDecode(text string) ([]byte, error) {
	return hex.DecodeString(text)
}
