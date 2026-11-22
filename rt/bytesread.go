package rt

import (
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
)

// The reads, searches and decodings std/bytes gives a Bytes: a fixed-width
// integer or float at an index, a varint at an index, a byte or a sequence
// found, and UTF-8 checked or repaired. Each reads its operands alone and
// costs at most a pass over them.

// BytesWordAt is the width bytes of data from index i, as an unsigned
// integer in the byte order big says, or with signed as two's complement at
// its width, extended to 64 bits; false when they are not all inside data.
// Every fixed-width read and `Bytes.get` is this one at its width, and the
// VM runs a read whose operands and answer are in registers with it.
func BytesWordAt(data Bytes, i int64, width int, big, signed bool) (uint64, bool) {
	if i < 0 || i > int64(len(data))-int64(width) {
		return 0, false
	}
	var u uint64
	for k := range width {
		b := uint64(data[int(i)+k])
		if big {
			u = u<<8 | b
		} else {
			u |= b << (8 * uint(k))
		}
	}
	if signed {
		shift := 64 - 8*uint(width)
		u = uint64(int64(u<<shift) >> shift)
	}
	return u, true
}

// fixedAt is BytesWordAt unsigned at index at. An index past int64's range
// is past any buffer.
func fixedAt(data Bytes, at Int, width int, big bool) (uint64, bool) {
	return BytesWordAt(data, at.Saturated(), width, big, false)
}

// unsignedAt is fixedAt as std's Maybe of a non-negative Int.
func unsignedAt(data Bytes, at Int, width int, big bool) Maybe[Int] {
	u, ok := fixedAt(data, at, width, big)
	if !ok {
		return None[Int]()
	}
	return Some(UintInt(u))
}

// signedAt is BytesWordAt read as two's complement at its width.
func signedAt(data Bytes, at Int, width int, big bool) Maybe[int64] {
	u, ok := BytesWordAt(data, at.Saturated(), width, big, true)
	if !ok {
		return None[int64]()
	}
	return Some(int64(u))
}

// BytesUint8At and its siblings are `Bytes.uint8_at`, `Bytes.int8_at` and
// the other fixed-width reads: the integer in the width, signedness and
// byte order the name says at index at, or None when its bytes are not all
// inside data.
func BytesUint8At(data Bytes, at Int) Maybe[Int]    { return unsignedAt(data, at, 1, true) }
func BytesInt8At(data Bytes, at Int) Maybe[int64]   { return signedAt(data, at, 1, true) }
func BytesUint16BEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 2, true) }
func BytesUint16LEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 2, false) }
func BytesInt16BEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 2, true)
}
func BytesInt16LEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 2, false)
}
func BytesUint32BEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 4, true) }
func BytesUint32LEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 4, false) }
func BytesInt32BEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 4, true)
}
func BytesInt32LEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 4, false)
}
func BytesUint64BEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 8, true) }
func BytesUint64LEAt(data Bytes, at Int) Maybe[Int] { return unsignedAt(data, at, 8, false) }
func BytesInt64BEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 8, true)
}
func BytesInt64LEAt(data Bytes, at Int) Maybe[int64] {
	return signedAt(data, at, 8, false)
}

// floatAt is the IEEE 754 float of width bytes at index at: a 32-bit one
// widened exactly.
func floatAt(data Bytes, at Int, width int, big bool) Maybe[float64] {
	u, ok := fixedAt(data, at, width, big)
	if !ok {
		return None[float64]()
	}
	if width == 4 {
		return Some(float64(math.Float32frombits(uint32(u))))
	}
	return Some(math.Float64frombits(u))
}

// BytesFloat32BEAt and its siblings are `Bytes.float32_be_at` and the other
// float reads.
func BytesFloat32BEAt(data Bytes, at Int) Maybe[float64] { return floatAt(data, at, 4, true) }
func BytesFloat32LEAt(data Bytes, at Int) Maybe[float64] { return floatAt(data, at, 4, false) }
func BytesFloat64BEAt(data Bytes, at Int) Maybe[float64] { return floatAt(data, at, 8, true) }
func BytesFloat64LEAt(data Bytes, at Int) Maybe[float64] { return floatAt(data, at, 8, false) }

// varintAt is a varint and the index after it, the tuple Bytes.uvarint_at
// and Bytes.varint_at answer: an rt signature spells a Nomi tuple as an
// anonymous struct of F0, F1, ...
type varintAt = struct {
	F0 Int
	F1 int64
}

// BytesUvarintAt is std/bytes's `Bytes.uvarint_at`: the unsigned LEB128
// integer starting at index at and the index after its last byte, or None
// when data ends before a byte without its high bit set. The integer has
// any size.
func BytesUvarintAt(data Bytes, at Int) Maybe[varintAt] {
	if v, next, ok := uvarintAt(data, at); ok {
		return Some(varintAt{v, next})
	}
	return None[varintAt]()
}

// uvarintAt is BytesUvarintAt's integer and index, and false for its None.
func uvarintAt(data Bytes, at Int) (Int, int64, bool) {
	i := at.Saturated()
	if i < 0 || i >= int64(len(data)) {
		return SmallInt(0), 0, false
	}
	var u uint64
	var shift uint
	for k := int(i); k < len(data); k++ {
		b := data[k]
		if shift == 63 && b > 1 || shift > 63 {
			return uvarintBig(data, int(i))
		}
		u |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return UintInt(u), int64(k + 1), true
		}
		shift += 7
	}
	return SmallInt(0), 0, false
}

// uvarintBig is uvarintAt for an integer past 64 bits: the groups gathered
// into a big.Int from the most significant one down.
func uvarintBig(data Bytes, start int) (Int, int64, bool) {
	end := start
	for end < len(data) && data[end] >= 0x80 {
		end++
	}
	if end == len(data) {
		return SmallInt(0), 0, false
	}
	z := new(big.Int)
	for k := end; k >= start; k-- {
		z.Lsh(z, 7)
		z.Or(z, big.NewInt(int64(data[k]&0x7f)))
	}
	return BigIntOf(z), int64(end + 1), true
}

// BytesVarintAt is std/bytes's `Bytes.varint_at`: BytesUvarintAt's integer
// decoded from zigzag.
func BytesVarintAt(data Bytes, at Int) Maybe[varintAt] {
	u, next, ok := uvarintAt(data, at)
	if !ok {
		return None[varintAt]()
	}
	if x, small := u.Int64(); small {
		// A varint of at most 63 bits: its zigzag decoding fits int64.
		return Some(varintAt{SmallInt(int64(uint64(x)>>1) ^ -(x & 1)), next})
	}
	v := u.Value()
	half, _ := IntShr(v, int64(1))
	if IntAnd(v, int64(1)) == int64(0) {
		return Some(varintAt{IntOf(half), next})
	}
	return Some(varintAt{IntOf(IntNot(half)), next})
}

// from is a search's start index clamped into data, as Bytes.slice clamps
// its bounds.
func searchFrom(data Bytes, from Int) int {
	f := from.Saturated()
	switch {
	case f < 0:
		return 0
	case f > int64(len(data)):
		return len(data)
	}
	return int(f)
}

// found is i, an index of data[start:] or -1, as std's Maybe of an index of
// data.
func found(start, i int) Maybe[int64] {
	if i < 0 {
		return None[int64]()
	}
	return Some(int64(start + i))
}

// BytesIndexOf is `Bytes.index_of`: the first index at or after from where
// needle starts in data. An empty needle is found at from.
func BytesIndexOf(data, needle Bytes, from Int) Maybe[int64] {
	start := searchFrom(data, from)
	return found(start, strings.Index(string(data[start:]), string(needle)))
}

// BytesIndexOfByte is `Bytes.index_of_byte`: the first index at or after
// from that holds b.
func BytesIndexOfByte(data Bytes, b Byte, from Int) Maybe[int64] {
	start := searchFrom(data, from)
	return found(start, strings.IndexByte(string(data[start:]), byte(b)))
}

// byteSet is a set of bytes as 256 bits, built on the stack from a set's
// bytes in one pass over them.
type byteSet [4]uint64

func byteSetOf(set Bytes) (s byteSet) {
	for i := 0; i < len(set); i++ {
		b := set[i]
		s[b>>6] |= 1 << (b & 63)
	}
	return s
}

func (s *byteSet) has(b byte) bool { return s[b>>6]&(1<<(b&63)) != 0 }

// BytesIndexOfAny is `Bytes.index_of_any`: the first index at or after
// from that holds one of the bytes of set. Each byte of set counts on its
// own, unlike Go's bytes.IndexAny, which reads its set as UTF-8. A set of
// one byte is BytesIndexOfByte's search.
func BytesIndexOfAny(data, set Bytes, from Int) Maybe[int64] {
	start := searchFrom(data, from)
	switch len(set) {
	case 0:
		return None[int64]()
	case 1:
		return found(start, strings.IndexByte(string(data[start:]), set[0]))
	}
	s := byteSetOf(set)
	for i := start; i < len(data); i++ {
		if s.has(data[i]) {
			return Some(int64(i))
		}
	}
	return None[int64]()
}

// BytesLastIndexOfAny is `Bytes.last_index_of_any`: the last index that
// holds one of the bytes of set.
func BytesLastIndexOfAny(data, set Bytes) Maybe[int64] {
	switch len(set) {
	case 0:
		return None[int64]()
	case 1:
		return found(0, strings.LastIndexByte(string(data), set[0]))
	}
	s := byteSetOf(set)
	for i := len(data) - 1; i >= 0; i-- {
		if s.has(data[i]) {
			return Some(int64(i))
		}
	}
	return None[int64]()
}

// BytesLastIndexOf is `Bytes.last_index_of`: the last index where needle
// starts in data. An empty needle is found at the end.
func BytesLastIndexOf(data, needle Bytes) Maybe[int64] {
	return found(0, strings.LastIndex(string(data), string(needle)))
}

// BytesLastIndexOfByte is `Bytes.last_index_of_byte`: the last index that
// holds b.
func BytesLastIndexOfByte(data Bytes, b Byte) Maybe[int64] {
	return found(0, strings.LastIndexByte(string(data), byte(b)))
}

// BytesContainsBytes is `Bytes.contains_bytes`.
func BytesContainsBytes(data, needle Bytes) bool {
	return strings.Contains(string(data), string(needle))
}

// BytesStartsWith is `Bytes.starts_with`.
func BytesStartsWith(data, prefix Bytes) bool {
	return strings.HasPrefix(string(data), string(prefix))
}

// BytesEndsWith is `Bytes.ends_with`.
func BytesEndsWith(data, suffix Bytes) bool {
	return strings.HasSuffix(string(data), string(suffix))
}

// BytesCount is `Bytes.count`: how many times needle occurs in data
// without overlapping, from the left. An empty needle occurs between every
// two bytes and at both ends: length + 1 times.
func BytesCount(data, needle Bytes) int64 {
	if len(needle) == 0 {
		return int64(len(data)) + 1
	}
	return int64(strings.Count(string(data), string(needle)))
}

// BytesSplit is `Bytes.split`: the parts of data between occurrences of
// separator, found from the left without overlapping. Each part is a slice
// of data, not a copy. An empty separator splits data into its bytes; an
// empty data is one empty part, as `String.split` answers.
func BytesSplit(data, separator Bytes) *List[Bytes] {
	var parts []Bytes
	if len(separator) == 0 {
		if len(data) == 0 {
			parts = []Bytes{""}
		}
		for i := range len(data) {
			parts = append(parts, data[i:i+1])
		}
	} else {
		rest := data
		for {
			i := strings.Index(string(rest), string(separator))
			if i < 0 {
				parts = append(parts, rest)
				break
			}
			parts = append(parts, rest[:i])
			rest = rest[i+len(separator):]
		}
	}
	var out *List[Bytes]
	for i := len(parts) - 1; i >= 0; i-- {
		out = Cons(parts[i], out)
	}
	return out
}

// BytesConcatAll is `Bytes.concat_all`: the parts one after another, in
// one copy.
func BytesConcatAll(parts *List[Bytes]) Bytes {
	n := 0
	for cur := parts; cur != nil; cur = cur.Tail {
		n += len(cur.Head)
	}
	var b strings.Builder
	b.Grow(n)
	for cur := parts; cur != nil; cur = cur.Tail {
		b.WriteString(string(cur.Head))
	}
	return Bytes(b.String())
}

// BytesValidUTF8Length is `Bytes.valid_utf8_length`: how many bytes at the
// start of data are well-formed UTF-8. It is the length when all of data
// is, and otherwise the index of the first byte that is not part of a
// well-formed sequence.
func BytesValidUTF8Length(data Bytes) int64 {
	s := string(data)
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return int64(i)
		}
		i += size
	}
	return int64(len(s))
}

// BytesToStringLossy is `Bytes.to_string_lossy`: data as UTF-8, with each
// maximal ill-formed subsequence replaced by U+FFFD, the practice Unicode
// recommends (Unicode 15, section 3.9, "U+FFFD Substitution of Maximal
// Subparts") and the WHATWG Encoding Standard's decoder follows: a
// truncated sequence is one replacement, and a byte that can begin no
// sequence is one each.
func BytesToStringLossy(data Bytes) string {
	s := string(data)
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size != 1 {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		b.WriteRune(utf8.RuneError)
		i += maximalSubpart(s[i:])
	}
	return b.String()
}

// maximalSubpart is the length of the ill-formed sequence s starts with:
// the lead byte and the continuation bytes after it that a well-formed
// sequence with that lead could have, or 1 when the lead begins none.
func maximalSubpart(s string) int {
	lead := s[0]
	var need int
	lo, hi := byte(0x80), byte(0xbf)
	switch {
	case lead >= 0xc2 && lead <= 0xdf:
		need = 1
	case lead == 0xe0:
		need, lo = 2, 0xa0
	case lead >= 0xe1 && lead <= 0xec, lead == 0xee, lead == 0xef:
		need = 2
	case lead == 0xed:
		need, hi = 2, 0x9f
	case lead == 0xf0:
		need, lo = 3, 0x90
	case lead >= 0xf1 && lead <= 0xf3:
		need = 3
	case lead == 0xf4:
		need, hi = 3, 0x8f
	default:
		return 1
	}
	n := 1
	for k := 0; k < need && n < len(s); k++ {
		c := s[n]
		if c < lo || c > hi {
			break
		}
		lo, hi = 0x80, 0xbf
		n++
	}
	return n
}
