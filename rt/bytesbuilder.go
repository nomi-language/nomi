package rt

import (
	"math"
	"sync/atomic"
	"unsafe"
)

// std/bytes.Builder: a byte buffer built by appending, as a value.
//
// # The representation
//
// A Builder is a window over an append-only buffer: the buffer and the
// count of its bytes the value holds. Appending to a value whose count is
// where the buffer's filled part ends writes the new bytes into the
// buffer's spare capacity and answers a value with a larger count over the
// same buffer. The bytes an earlier value holds are never written again,
// so the earlier value still holds what it held: nothing a program can
// observe changes, and no ownership analysis is needed for the write.
//
// Appending to a value whose count is short of the filled end (a value the
// program kept and appended to before, `a = push(b, 1)` and then
// `push(b, 2)`) cannot write into the buffer, whose next bytes belong to
// the other value. It copies the value's bytes into a new buffer, as a
// value that outgrows its buffer does. Each value that appends first from
// a given count claims that part of the buffer with a compare-and-swap of
// its filled length, so two tasks appending to one Builder at once each
// get a buffer of their own.
//
// A new buffer holds twice what the value needs, so appending n bytes one
// call at a time copies fewer than 2n bytes in all: appending is amortized
// constant time per byte, the cost model Go's append has. A Builder kept
// and appended to from one count again and again copies each time, which
// is the price of the branch, and the only way to pay more than linear.
//
// `to_bytes` answers a Bytes over the buffer's first n bytes without a
// copy: those bytes are never written again, so a Go string over them is
// immutable, as a string must be. The Bytes keeps the buffer alive, up to
// twice the bytes it holds.
type BytesBuilder struct {
	buf *bytesBuffer
	n   int
}

// bytesBuffer is a Builder's storage: data's length is its capacity, and
// filled the bytes some value has claimed.
type bytesBuffer struct {
	filled atomic.Int64
	data   []byte
}

// BytesBuilderNew is `Builder.new`: an empty builder whose first buffer
// holds capacity bytes. A capacity past what memory holds is clamped, as
// a hint it is.
func BytesBuilderNew(capacity Int) BytesBuilder {
	c := capacity.Saturated()
	if c <= 0 {
		return BytesBuilder{}
	}
	if c > maxBuilderHint {
		c = maxBuilderHint
	}
	return BytesBuilder{buf: &bytesBuffer{data: make([]byte, c)}}
}

// maxBuilderHint bounds the buffer a capacity hint allocates before any
// byte is written: 64 MiB.
const maxBuilderHint = 64 << 20

// grow answers where b's next k bytes go: a slice of k bytes to write, and
// the builder that holds them once written.
func (b BytesBuilder) grow(k int) ([]byte, BytesBuilder) {
	if k > math.MaxInt-b.n {
		Trap("Builder: the bytes do not fit in memory")
	}
	end := b.n + k
	if buf := b.buf; buf != nil && end <= len(buf.data) && buf.filled.CompareAndSwap(int64(b.n), int64(end)) {
		return buf.data[b.n:end], BytesBuilder{buf: buf, n: end}
	}
	size := 2 * end
	if size < 64 {
		size = 64
	}
	next := &bytesBuffer{data: make([]byte, size)}
	if b.n > 0 {
		copy(next.data, b.buf.data[:b.n])
	}
	next.filled.Store(int64(end))
	return next.data[b.n:end], BytesBuilder{buf: next, n: end}
}

// BytesBuilderLength is `Builder.length`: the bytes b holds.
func BytesBuilderLength(b BytesBuilder) int64 { return int64(b.n) }

// BytesBuilderToBytes is `Builder.to_bytes`: b's bytes, with no copy.
func BytesBuilderToBytes(b BytesBuilder) Bytes {
	if b.n == 0 {
		return ""
	}
	return Bytes(unsafe.String(&b.buf.data[0], b.n))
}

// BytesBuilderPush is `Builder.push`: b with one more byte.
func BytesBuilderPush(b BytesBuilder, x Byte) BytesBuilder {
	dst, out := b.grow(1)
	dst[0] = byte(x)
	return out
}

// BytesBuilderPushBytes is `Builder.push_bytes`: b with data after it.
func BytesBuilderPushBytes(b BytesBuilder, data Bytes) BytesBuilder {
	dst, out := b.grow(len(data))
	copy(dst, data)
	return out
}

// BytesBuilderPushString is `Builder.push_string`: b with text's UTF-8
// after it.
func BytesBuilderPushString(b BytesBuilder, text string) BytesBuilder {
	dst, out := b.grow(len(text))
	copy(dst, text)
	return out
}

// pushFixed appends the low width bytes of u, most significant first when
// big, least significant first otherwise.
func (b BytesBuilder) pushFixed(u uint64, width int, big bool) BytesBuilder {
	dst, out := b.grow(width)
	putFixed(dst, u, width, big)
	return out
}

// putFixed writes the low width bytes of u into dst.
func putFixed(dst []byte, u uint64, width int, big bool) {
	for i := range width {
		shift := 8 * uint(i)
		if big {
			shift = 8 * uint(width-1-i)
		}
		dst[i] = byte(u >> shift)
	}
}

// low64Of is n's low 64 bits, two's complement.
func low64Of(n Int) uint64 { return n.Low64() }

// BytesBuilderPushInt8 is `Builder.push_int8`: n's low 8 bits.
func BytesBuilderPushInt8(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 1, true)
}

// BytesBuilderPushInt16BE and its siblings are `Builder.push_int16_be` and
// the other fixed-width writes: n's low bits, two's complement, in the
// width and byte order the name says.
func BytesBuilderPushInt16BE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 2, true)
}

func BytesBuilderPushInt16LE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 2, false)
}

func BytesBuilderPushInt32BE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 4, true)
}

func BytesBuilderPushInt32LE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 4, false)
}

func BytesBuilderPushInt64BE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 8, true)
}

func BytesBuilderPushInt64LE(b BytesBuilder, n Int) BytesBuilder {
	return b.pushFixed(low64Of(n), 8, false)
}

// BytesBuilderPushFloat32BE and its siblings are `Builder.push_float32_be`
// and the other float writes: x's IEEE 754 bits, x rounded to the nearest
// 32-bit float for the 32-bit forms (Float32Bits).
func BytesBuilderPushFloat32BE(b BytesBuilder, x float64) BytesBuilder {
	return b.pushFixed(uint64(Float32Bits(x)), 4, true)
}

func BytesBuilderPushFloat32LE(b BytesBuilder, x float64) BytesBuilder {
	return b.pushFixed(uint64(Float32Bits(x)), 4, false)
}

func BytesBuilderPushFloat64BE(b BytesBuilder, x float64) BytesBuilder {
	return b.pushFixed(math.Float64bits(x), 8, true)
}

func BytesBuilderPushFloat64LE(b BytesBuilder, x float64) BytesBuilder {
	return b.pushFixed(math.Float64bits(x), 8, false)
}

// BytesBuilderPushUvarint is `Builder.push_uvarint`: n in unsigned LEB128,
// seven bits a byte, least significant group first, each byte but the
// last with its high bit set. n is a NonNegativeInt of any size; a
// negative one, which only rt's own callers could pass, faults.
func BytesBuilderPushUvarint(b BytesBuilder, n Int) BytesBuilder {
	if n.Sign() < 0 {
		Trap("Builder.push_uvarint: " + n.String() + " is negative")
	}
	if x, ok := n.Int64(); ok {
		var tmp [10]byte
		k := putUvarint(tmp[:], uint64(x))
		dst, out := b.grow(k)
		copy(dst, tmp[:k])
		return out
	}
	// Past 64 bits: the magnitude's bytes, most significant first, read
	// seven bits at a time from the least significant end.
	z := n.Big()
	length := z.BitLen()
	magnitude := z.Bytes()
	bit := func(i int) byte { return magnitude[len(magnitude)-1-i/8] >> (i % 8) & 1 }
	groups := (length + 6) / 7
	dst, out := b.grow(groups)
	for g := range groups {
		var v byte
		for j := range 7 {
			if i := 7*g + j; i < length {
				v |= bit(i) << j
			}
		}
		if g < groups-1 {
			v |= 0x80
		}
		dst[g] = v
	}
	return out
}

// putUvarint writes u in unsigned LEB128 into dst and answers its length.
func putUvarint(dst []byte, u uint64) int {
	i := 0
	for u >= 0x80 {
		dst[i] = byte(u) | 0x80
		u >>= 7
		i++
	}
	dst[i] = byte(u)
	return i + 1
}

// BytesBuilderPushVarint is `Builder.push_varint`: n zigzag-encoded (0, -1,
// 1, -2 as 0, 1, 2, 3: 2n for n >= 0, -2n - 1 otherwise), then in unsigned
// LEB128, as protobuf's sint fields and Go's binary.AppendVarint write it.
func BytesBuilderPushVarint(b BytesBuilder, n Int) BytesBuilder {
	return BytesBuilderPushUvarint(b, zigzag(n))
}

// zigzag is n's zigzag code.
func zigzag(n Int) Int {
	if x, small := n.Int64(); small {
		// 2n or -2n - 1 of an int64, exactly, in 64 bits.
		return UintInt(uint64(x<<1) ^ uint64(x>>63))
	}
	twice := IntAdd(n.Value(), n.Value())
	if n.Sign() < 0 {
		return IntOf(IntSub(IntNeg(twice), int64(1)))
	}
	return IntOf(twice)
}
