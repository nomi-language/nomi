package rt

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"sync"
	"testing"
)

func TestBytesBuilder_AppendsAndKeepsEarlierValues(t *testing.T) {
	b0 := BytesBuilderNew(SmallInt(0))
	b1 := BytesBuilderPush(b0, 1)
	b2 := BytesBuilderPushBytes(b1, "\x02\x03")
	// b1 was appended to once already, so this append branches: b2 keeps
	// its bytes and b3 gets a buffer of its own.
	b3 := BytesBuilderPush(b1, 9)
	b4 := BytesBuilderPushString(b2, "é")
	for _, c := range []struct {
		b    BytesBuilder
		want string
	}{{b0, ""}, {b1, "\x01"}, {b2, "\x01\x02\x03"}, {b3, "\x01\x09"}, {b4, "\x01\x02\x03\xc3\xa9"}} {
		if got := string(BytesBuilderToBytes(c.b)); got != c.want {
			t.Errorf("builder holds %q, want %q", got, c.want)
		}
		if BytesBuilderLength(c.b) != int64(len(c.want)) {
			t.Errorf("length %d, want %d", BytesBuilderLength(c.b), len(c.want))
		}
	}
	if b2.buf != b4.buf {
		t.Errorf("an append at the filled end copied the buffer")
	}
	if b1.buf == b3.buf {
		t.Errorf("an append behind the filled end wrote into the shared buffer")
	}
}

// Building n bytes one append at a time allocates a buffer each time the
// bytes outgrow the last, so the allocations grow with log n and the bytes
// copied stay under 2n.
func TestBytesBuilder_AppendingByteByByteIsLinear(t *testing.T) {
	const n = 1 << 20
	var out Bytes
	allocs := testing.AllocsPerRun(3, func() {
		b := BytesBuilderNew(SmallInt(0))
		for i := range n {
			b = BytesBuilderPush(b, Byte(i))
		}
		out = BytesBuilderToBytes(b)
	})
	if len(out) != n || out[n-1] != byte((n-1)&0xff) {
		t.Fatalf("built %d bytes", len(out))
	}
	// 64 bytes doubling to 2 MiB is 16 buffers, each with its header.
	if allocs > 40 {
		t.Errorf("%v allocations building %d bytes one at a time, want at most 40", allocs, n)
	}
}

func TestBytesBuilder_ToBytesDoesNotCopy(t *testing.T) {
	b := BytesBuilderPushBytes(BytesBuilderNew(SmallInt(16)), "abc")
	if allocs := testing.AllocsPerRun(10, func() { _ = BytesBuilderToBytes(b) }); allocs != 0 {
		t.Errorf("to_bytes allocated %v times", allocs)
	}
}

// Two tasks appending to one builder at once each get their own bytes. Run
// under -race this also checks that neither reads what the other writes.
func TestBytesBuilder_ConcurrentAppendsFromOneValue(t *testing.T) {
	base := BytesBuilderPushBytes(BytesBuilderNew(SmallInt(1024)), "base")
	var wg sync.WaitGroup
	results := make([]Bytes, 8)
	for g := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := base
			for range 100 {
				b = BytesBuilderPush(b, Byte('a'+g))
			}
			results[g] = BytesBuilderToBytes(b)
		}()
	}
	wg.Wait()
	for g, r := range results {
		want := "base" + string(bytes.Repeat([]byte{byte('a' + g)}, 100))
		if string(r) != want {
			t.Errorf("task %d built %q", g, r)
		}
	}
}

func TestBytesBuilder_FixedWidthWritesAreTheReadsInverse(t *testing.T) {
	values := []int64{0, 1, -1, 127, -128, 255, 256, 65535, -32768, math.MaxInt32, math.MinInt32, 1 << 32, math.MaxInt64, math.MinInt64}
	type form struct {
		width  int
		big    bool
		push   func(BytesBuilder, Int) BytesBuilder
		u      func(Bytes, Int) Maybe[Int]
		s      func(Bytes, Int) Maybe[int64]
		golang func(uint64) []byte
	}
	forms := []form{
		{1, true, BytesBuilderPushInt8, BytesUint8At, BytesInt8At, func(u uint64) []byte { return []byte{byte(u)} }},
		{2, true, BytesBuilderPushInt16BE, BytesUint16BEAt, BytesInt16BEAt, func(u uint64) []byte { return binary.BigEndian.AppendUint16(nil, uint16(u)) }},
		{2, false, BytesBuilderPushInt16LE, BytesUint16LEAt, BytesInt16LEAt, func(u uint64) []byte { return binary.LittleEndian.AppendUint16(nil, uint16(u)) }},
		{4, true, BytesBuilderPushInt32BE, BytesUint32BEAt, BytesInt32BEAt, func(u uint64) []byte { return binary.BigEndian.AppendUint32(nil, uint32(u)) }},
		{4, false, BytesBuilderPushInt32LE, BytesUint32LEAt, BytesInt32LEAt, func(u uint64) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(u)) }},
		{8, true, BytesBuilderPushInt64BE, BytesUint64BEAt, BytesInt64BEAt, func(u uint64) []byte { return binary.BigEndian.AppendUint64(nil, u) }},
		{8, false, BytesBuilderPushInt64LE, BytesUint64LEAt, BytesInt64LEAt, func(u uint64) []byte { return binary.LittleEndian.AppendUint64(nil, u) }},
	}
	for _, f := range forms {
		for _, v := range values {
			b := f.push(BytesBuilderPush(BytesBuilder{}, 0xAA), SmallInt(v))
			data := BytesBuilderToBytes(b)
			if want := append([]byte{0xAA}, f.golang(uint64(v))...); !bytes.Equal([]byte(data), want) {
				t.Errorf("width %d big %v: %d wrote % x, want % x", f.width, f.big, v, data, want)
			}
			shift := 64 - 8*uint(f.width)
			wantU := new(big.Int).SetUint64(uint64(v) << shift >> shift)
			if got := f.u(data, SmallInt(1)); got.Tag != TagSome || got.Some.Big().Cmp(wantU) != 0 {
				t.Errorf("width %d: unsigned read of %d is %v, want %v", f.width, v, got, wantU)
			}
			if got := f.s(data, SmallInt(1)); got.Tag != TagSome || got.Some != int64(uint64(v)<<shift)>>shift {
				t.Errorf("width %d: signed read of %d is %v", f.width, v, got)
			}
			if got := f.u(data, SmallInt(2)); got.Tag == TagSome {
				t.Errorf("width %d: a read past the end answered %v", f.width, got)
			}
			if got := f.s(data, SmallInt(-1)); got.Tag == TagSome {
				t.Errorf("width %d: a read before the start answered %v", f.width, got)
			}
		}
	}
}

func TestBytesBuilder_FloatsRoundTrip(t *testing.T) {
	for _, x := range []float64{0, math.Copysign(0, -1), 1.5, -2.25, math.Inf(1), math.MaxFloat64, 1e-310, 0.1} {
		b := BytesBuilderPushFloat64LE(BytesBuilderPushFloat64BE(BytesBuilder{}, x), x)
		data := BytesBuilderToBytes(b)
		be, le := BytesFloat64BEAt(data, SmallInt(0)), BytesFloat64LEAt(data, SmallInt(8))
		if math.Float64bits(be.Some) != math.Float64bits(x) || math.Float64bits(le.Some) != math.Float64bits(x) {
			t.Errorf("%v read back as %v and %v", x, be.Some, le.Some)
		}
		b32 := BytesBuilderPushFloat32LE(BytesBuilderPushFloat32BE(BytesBuilder{}, x), x)
		d32 := BytesBuilderToBytes(b32)
		want := float64(float32(x))
		if g := BytesFloat32BEAt(d32, SmallInt(0)).Some; math.Float64bits(g) != math.Float64bits(want) {
			t.Errorf("%v as 32 bits read back as %v, want %v", x, g, want)
		}
		if g := BytesFloat32LEAt(d32, SmallInt(4)).Some; math.Float64bits(g) != math.Float64bits(want) {
			t.Errorf("%v as 32 bits little endian read back as %v, want %v", x, g, want)
		}
	}
	if !math.IsNaN(FloatFromBits32(SmallInt(int64(FloatToBits32(math.NaN()))))) {
		t.Errorf("a NaN did not survive 32 bits")
	}
}

func TestBytesBuilder_VarintsMatchGoAndRoundTrip(t *testing.T) {
	two100 := new(big.Int).Lsh(big.NewInt(1), 100)
	unsigned := []Int{SmallInt(0), SmallInt(1), SmallInt(127), SmallInt(128), SmallInt(300), SmallInt(math.MaxInt64),
		IntOf(IntFromUint64(math.MaxUint64)), BigIntOf(two100), BigIntOf(new(big.Int).Sub(two100, big.NewInt(1)))}
	for _, n := range unsigned {
		data := BytesBuilderToBytes(BytesBuilderPushUvarint(BytesBuilder{}, n))
		if n.Big().IsUint64() {
			if want := binary.AppendUvarint(nil, n.Big().Uint64()); !bytes.Equal([]byte(data), want) {
				t.Errorf("uvarint %s wrote % x, want % x", n, data, want)
			}
		}
		m := BytesUvarintAt(data, SmallInt(0))
		if got, next := m.Some.F0, m.Some.F1; m.Tag != TagSome || next != int64(len(data)) || got.Big().Cmp(n.Big()) != 0 {
			t.Errorf("uvarint %s read back as %s ending at %d of %d", n, got, next, len(data))
		}
	}
	for _, v := range []int64{0, -1, 1, -64, 64, math.MaxInt64, math.MinInt64} {
		data := BytesBuilderToBytes(BytesBuilderPushVarint(BytesBuilder{}, SmallInt(v)))
		if want := binary.AppendVarint(nil, v); !bytes.Equal([]byte(data), want) {
			t.Errorf("varint %d wrote % x, want % x", v, data, want)
		}
		m := BytesVarintAt(data, SmallInt(0))
		if got, next := m.Some.F0, m.Some.F1; m.Tag != TagSome || next != int64(len(data)) || got.Value() != Value(v) {
			t.Errorf("varint %d read back as %s", v, got)
		}
	}
	big := BigIntOf(new(big.Int).Neg(two100))
	data := BytesBuilderToBytes(BytesBuilderPushVarint(BytesBuilder{}, big))
	if got := BytesVarintAt(data, SmallInt(0)).Some.F0; got.Big().Cmp(big.Big()) != 0 {
		t.Errorf("varint -2^100 read back as %s", got)
	}
	for _, bad := range []Bytes{"", "\x80", "\xff\xff"} {
		if m := BytesUvarintAt(bad, SmallInt(0)); m.Tag != TagNone {
			t.Errorf("uvarint over % x ended at %d", bad, m.Some.F1)
		}
	}
}

func TestBytes_Searches(t *testing.T) {
	data := Bytes("ab\r\nc\r\n")
	check := func(what string, got Maybe[int64], want int64) {
		t.Helper()
		if want < 0 && got.Tag == TagSome || want >= 0 && (got.Tag != TagSome || got.Some != want) {
			t.Errorf("%s = %v, want %d", what, got, want)
		}
	}
	check("index_of", BytesIndexOf(data, "\r\n", SmallInt(0)), 2)
	check("index_of from 3", BytesIndexOf(data, "\r\n", SmallInt(3)), 5)
	check("index_of from past", BytesIndexOf(data, "\r\n", SmallInt(99)), -1)
	check("index_of empty", BytesIndexOf(data, "", SmallInt(4)), 4)
	check("index_of_byte", BytesIndexOfByte(data, 'c', SmallInt(-5)), 4)
	check("last_index_of", BytesLastIndexOf(data, "\r\n"), 5)
	check("last_index_of_byte", BytesLastIndexOfByte(data, 'a'), 0)
	check("last_index_of_byte missing", BytesLastIndexOfByte(data, 'z'), -1)
	if BytesCount(data, "\r\n") != 2 || BytesCount("aaaa", "aa") != 2 || BytesCount("abc", "") != 4 {
		t.Errorf("count")
	}
	split := func(d, sep Bytes) []string {
		var out []string
		for l := BytesSplit(d, sep); l != nil; l = l.Tail {
			out = append(out, string(l.Head))
		}
		return out
	}
	for _, c := range []struct {
		d, sep Bytes
		want   []string
	}{{"a,b,,c", ",", []string{"a", "b", "", "c"}}, {"", ",", []string{""}}, {"ab", "", []string{"a", "b"}}, {"", "", []string{""}}, {",", ",", []string{"", ""}}} {
		if got := split(c.d, c.sep); len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) || got[len(got)-1] != c.want[len(c.want)-1] {
			t.Errorf("split(%q, %q) = %q, want %q", c.d, c.sep, got, c.want)
		}
	}
}

func TestBytes_Utf8(t *testing.T) {
	for _, c := range []struct {
		in    Bytes
		valid int64
		lossy string
	}{
		{"héllo", 6, "héllo"},
		{"a\xffb", 1, "a�b"},
		{"a\xe2\x82", 1, "a�"},
		{"\xe2\x82\xe2\x82\xac", 0, "�€"},
		{"\xed\xa0\x80", 0, "���"},
		{"\xf0\x9f\x99", 0, "�"},
		{"\xc0\xaf", 0, "��"},
	} {
		if got := BytesValidUTF8Length(c.in); got != c.valid {
			t.Errorf("valid_utf8_length(% x) = %d, want %d", c.in, got, c.valid)
		}
		if got := BytesToStringLossy(c.in); got != c.lossy {
			t.Errorf("to_string_lossy(% x) = %q, want %q", c.in, got, c.lossy)
		}
	}
}

func TestInt_FixedWidthBits(t *testing.T) {
	if IntRotateLeft32(SmallInt(0x80000001), SmallInt(1)) != 3 || IntRotateLeft32(SmallInt(3), SmallInt(-1)) != 0x80000001 {
		t.Errorf("rotate_left_32")
	}
	if IntRotateLeft32(SmallInt(-1), SmallInt(5)) != 0xFFFFFFFF {
		t.Errorf("rotate_left_32 of -1")
	}
	if IntRotateLeft64(SmallInt(math.MinInt64), SmallInt(1)) != 1 {
		t.Errorf("rotate_left_64")
	}
	if IntRotateLeft32(SmallInt(1), BigIntOf(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 70), big.NewInt(3)))) != 8 {
		t.Errorf("rotate_left_32 by a big count")
	}
	if IntLeadingZeros32(SmallInt(1)) != 31 || IntLeadingZeros32(SmallInt(0)) != 32 || IntLeadingZeros64(SmallInt(-1)) != 0 {
		t.Errorf("leading_zeros")
	}
	if IntTrailingZeros32(SmallInt(8)) != 3 || IntTrailingZeros64(SmallInt(0)) != 64 {
		t.Errorf("trailing_zeros")
	}
	if IntSwapBytes16(SmallInt(0x1234)) != 0x3412 || IntSwapBytes32(SmallInt(0x12345678)) != 0x78563412 {
		t.Errorf("swap_bytes")
	}
	if IntWrapAdd32(SmallInt(0xFFFFFFFF), SmallInt(2)) != 1 || IntWrapSub32(SmallInt(0), SmallInt(1)) != 0xFFFFFFFF ||
		IntWrapMul32(SmallInt(0x10000), SmallInt(0x10000)) != 0 {
		t.Errorf("wrapping 32")
	}
	two100 := BigIntOf(new(big.Int).Lsh(big.NewInt(1), 100))
	for _, c := range []struct {
		n     Int
		width int64
		want  string
	}{{SmallInt(255), 8, "-1"}, {SmallInt(128), 8, "-128"}, {SmallInt(127), 8, "127"}, {SmallInt(-129), 8, "127"},
		{IntOf(IntFromUint64(math.MaxUint64)), 64, "-1"}, {two100, 101, "-1267650600228229401496703205376"},
		{two100, 200, two100.String()}, {SmallInt(-5), 1000, "-5"}} {
		if got := IntWrapSigned(c.n, SmallInt(c.width)); got.String() != c.want {
			t.Errorf("wrap_signed(%s, %d) = %s, want %s", c.n, c.width, got, c.want)
		}
	}
}
