package rt

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"unicode/utf8"
	"unsafe"
)

// std/bytes's operations over every byte of a Bytes at once: XOR, filling,
// reversing and decoding a code point at an offset. A Nomi loop over a Bytes
// costs about 100 ns a byte, so a parser or a protocol that needs one of these
// per byte calls the host function, which runs at memory speed.
//
// Each reads its operands through BytesView, which aliases the string's
// storage without a copy and is only read, and builds its answer in a fresh
// slice that becomes the answer's storage through ownedBytes, with no second
// copy: nothing writes the slice after that.

// BytesView is data's bytes, aliased with no copy, for a Go function that
// only reads them: a hash's Write, which the io.Writer contract forbids to
// change or keep them. Writing to them would change an immutable value.
func BytesView(data Bytes) []byte {
	return unsafe.Slice(unsafe.StringData(string(data)), len(data))
}

// ownedBytes is b as a Bytes over the same storage. The caller never writes
// b again.
func ownedBytes(b []byte) Bytes {
	if len(b) == 0 {
		return ""
	}
	return Bytes(unsafe.String(&b[0], len(b)))
}

// ByteCount is n as a length to allocate for fn's answer. A length past
// maxStringBytes does not fit in memory a program can have, so it traps
// naming fn and n rather than running out of memory, which ends the
// process with Go's own report. std declares such lengths NonNegativeInt.
func ByteCount(fn string, n Int) int {
	c, ok := n.Int64()
	if !ok || c > maxStringBytes {
		Trap(fmt.Sprintf("%s: %s bytes are more than memory holds", fn, n))
	}
	return int(c)
}

// BytesXor is `Bytes.xor`: each byte of a XOR the byte of b at the same
// index, or None when their lengths differ.
func BytesXor(a, b Bytes) Option[Bytes] {
	if len(a) != len(b) {
		return None[Bytes]()
	}
	out := make([]byte, len(a))
	subtle.XORBytes(out, BytesView(a), BytesView(b))
	return Some(ownedBytes(out))
}

// BytesXorRepeating is `Bytes.xor_repeating`: data with each byte XORed
// with key's bytes repeated from its start, as a WebSocket frame's payload
// is masked. An empty key leaves data as it is.
func BytesXorRepeating(data, key Bytes) Bytes {
	if len(key) == 0 || len(data) == 0 {
		return data
	}
	out := make([]byte, len(data))
	XorRepeating(out, BytesView(data), BytesView(key), 0)
	return ownedBytes(out)
}

// XorRepeating writes into dst each byte of src XORed with key repeated,
// starting key at index phase. dst is at least as long as src, and may be
// src itself; key is not empty. It is the one kernel behind
// `Bytes.xor_repeating` and a WebSocket frame's masking
// (internal/websocket).
func XorRepeating(dst, src, key []byte, phase int) {
	// XOR a stretch of the key repeated at a time, which subtle.XORBytes
	// does a machine word or a vector at a time, rather than a byte. The
	// stretch is whole copies of the key, so each starts at the same phase,
	// and for a key of up to 256 bytes it is on the stack.
	var local [256]byte
	n := len(key)
	span := max(len(local)/n, 1) * n
	if len(src) < span {
		span = len(src)
	}
	var block []byte
	if span <= len(local) {
		block = local[:span]
	} else {
		block = make([]byte, span)
	}
	for i := range block {
		block[i] = key[(phase+i)%n]
	}
	for i := 0; i < len(src); i += span {
		end := min(i+span, len(src))
		subtle.XORBytes(dst[i:end], src[i:end], block[:end-i])
	}
}

// BytesFill is `Bytes.fill`: length copies of the byte b.
func BytesFill(length Int, b Byte) Bytes {
	n := ByteCount("Bytes.fill", length)
	if b == 0 {
		return ownedBytes(make([]byte, n))
	}
	return ownedBytes(bytes.Repeat([]byte{byte(b)}, n))
}

// BytesReverse is `Bytes.reverse`: data's bytes in the opposite order.
func BytesReverse(data Bytes) Bytes {
	out := make([]byte, len(data))
	for i, j := 0, len(data)-1; j >= 0; i, j = i+1, j-1 {
		out[i] = data[j]
	}
	return ownedBytes(out)
}

// codePointAt is a code point and its length in bytes, the tuple
// Bytes.code_point_at answers: an rt signature spells a Nomi tuple as an
// anonymous struct of F0, F1, ...
type codePointAt = struct {
	F0 Codepoint
	F1 int64
}

// BytesCodePointAt is `Bytes.code_point_at`: the code point whose UTF-8
// encoding starts at offset and the count of its bytes, 1 to 4, or None
// when offset is outside data or the bytes there are not a well-formed
// UTF-8 sequence (a stray continuation byte, an overlong form, a surrogate,
// a value past U+10FFFF or a sequence data ends inside).
func BytesCodePointAt(data Bytes, offset Int) Option[codePointAt] {
	i := offset.Saturated()
	if i < 0 || i >= int64(len(data)) {
		return None[codePointAt]()
	}
	if c := data[i]; c < utf8.RuneSelf {
		return Some(codePointAt{Codepoint(c), 1})
	}
	r, size := utf8.DecodeRuneInString(string(data[i:]))
	if r == utf8.RuneError && size <= 1 {
		return None[codePointAt]()
	}
	return Some(codePointAt{Codepoint(r), int64(size)})
}
