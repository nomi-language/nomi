package stdlibadapters

import (
	"math"

	"github.com/nomi-language/nomi/rt"
)

// bytePrimitiveCases are the argument sets of std/bytes's reads at an
// index, searches, UTF-8 repair and Builder, and of std/int's and
// std/float's machine-word rows: the rows that joined the table with
// rt/bytesread.go, rt/bytesbuilder.go and rt/fixedwidth.go.
func bytePrimitiveCases() map[string][]hostCase {
	blobs := []any{hBytes(""), hBytes("\x00"), hBytes("ab\r\ncd\r\n"), hBytes("\xff\xfe"), hBytes("héllo"),
		hBytes("\x01\x02\x03\x04\x05\x06\x07\x08\x09"), hBytes("\xff\xff\xff\xff\xff\xff\xff\xff"), hBytes("aaaa")}
	needles := []any{hBytes(""), hBytes("\r\n"), hBytes("a"), hBytes("aa"), hBytes("zz"), hBytes("\xff")}
	ill := []any{hBytes("a\xffb"), hBytes("a\xe2\x82"), hBytes("\xe2\x82\xe2\x82\xac"), hBytes("\xed\xa0\x80"), hBytes("\xc0\xaf"), hBytes("\xf0\x9f\x99")}
	var inData, withFrom, byteFrom, holds, at, varints []hostCase
	for _, d := range blobs {
		for _, n := range needles {
			inData = append(inData, hostCase{args: args(d, n)})
			for _, from := range []int64{-1, 0, 3, 100} {
				withFrom = append(withFrom, hostCase{args: args(d, n, hInt(from))})
			}
		}
		for _, b := range []byte{0, 'a', 0xff} {
			holds = append(holds, hostCase{args: args(d, hByte(b))})
			for _, from := range []int64{-5, 0, 1, 100} {
				byteFrom = append(byteFrom, hostCase{args: args(d, hByte(b), hInt(from))})
			}
		}
		for _, i := range []any{hInt(-1), hInt(0), hInt(1), hInt(4), hInt(8), hBig("18446744073709551616")} {
			at = append(at, hostCase{args: args(d, i)})
		}
	}
	for _, v := range []string{"", "\x00", "\x7f", "\xac\x02", "\x80", "\xff\xff\xff\xff\xff\xff\xff\xff\xff\x01",
		"\xff\xff\xff\xff\xff\xff\xff\xff\xff\x7f", "\x03\x04"} {
		for _, i := range []int64{-1, 0, 1} {
			varints = append(varints, hostCase{args: args(hBytes(v), hInt(i))})
		}
	}
	ints := []any{hInt(0), hInt(1), hInt(-1), hInt(255), hInt(0x8000_0001), hInt(1 << 40), hInt(math.MaxInt64),
		hInt(math.MinInt64), hBig("18446744073709551615"), hBig("-1267650600228229401496703205376")}
	floats := []any{hFloat(0), hFloat(math.Copysign(0, -1)), hFloat(1.5), hFloat(-2.5), hFloat(0.1), hFloat(1e39),
		hFloat(1e-46), hFloat(math.Inf(1)), hFloat(math.NaN()), hFloat(math.MaxFloat64)}
	builders := []any{rt.BytesBuilder{}, rt.BytesBuilderPushBytes(rt.BytesBuilderNew(rt.SmallInt(4)), "ab")}
	with := func(rest ...any) []hostCase {
		var out []hostCase
		for _, b := range builders {
			for _, r := range rest {
				out = append(out, hostCase{args: args(b, r)})
			}
		}
		return out
	}
	var widths []hostCase
	for _, n := range ints {
		for _, w := range []int64{1, 8, 32, 64, 100} {
			widths = append(widths, hostCase{args: args(n, hInt(w))})
		}
	}
	return map[string][]hostCase{
		"bytes.Bytes.to_string_lossy":    unary(append(append([]any{}, blobs...), ill...)...),
		"bytes.Bytes.valid_utf8_length":  unary(append(append([]any{}, blobs...), ill...)...),
		"bytes.Bytes.concat_all":         unary(rtList(), rtList(hBytes("ab"), hBytes(""), hBytes("\xff"))),
		"bytes.index_of_from":            withFrom,
		"bytes.index_of_byte_from":       byteFrom,
		"bytes.Bytes.last_index_of":      inData,
		"bytes.Bytes.last_index_of_byte": holds,
		"bytes.index_of_any_from":        withFrom,
		"bytes.Bytes.last_index_of_any":  inData,
		"bytes.Bytes.contains_bytes":     inData,
		"bytes.Bytes.starts_with":        inData,
		"bytes.Bytes.ends_with":          inData,
		"bytes.Bytes.split":              inData,
		"bytes.Bytes.count":              inData,
		"bytes.Bytes.uint8_at":           at,
		"bytes.Bytes.int8_at":            at,
		"bytes.Bytes.uint16_be_at":       at,
		"bytes.Bytes.uint16_le_at":       at,
		"bytes.Bytes.int16_be_at":        at,
		"bytes.Bytes.int16_le_at":        at,
		"bytes.Bytes.uint32_be_at":       at,
		"bytes.Bytes.uint32_le_at":       at,
		"bytes.Bytes.int32_be_at":        at,
		"bytes.Bytes.int32_le_at":        at,
		"bytes.Bytes.uint64_be_at":       at,
		"bytes.Bytes.uint64_le_at":       at,
		"bytes.Bytes.int64_be_at":        at,
		"bytes.Bytes.int64_le_at":        at,
		"bytes.Bytes.float32_be_at":      at,
		"bytes.Bytes.float32_le_at":      at,
		"bytes.Bytes.float64_be_at":      at,
		"bytes.Bytes.float64_le_at":      at,
		"bytes.Bytes.uvarint_at":         varints,
		"bytes.Bytes.varint_at":          varints,
		"bytes.builder_new":              unary(hInt(0), hInt(16), hInt(-1), hBig("18446744073709551616")),
		"bytes.Builder.length":           unary(builders...),
		"bytes.Builder.to_bytes":         unary(builders...),
		"bytes.Builder.push":             with(hByte(0), hByte(0xff)),
		"bytes.Builder.push_bytes":       with(blobs...),
		"bytes.Builder.push_string":      with(hStr(""), hStr("é🙂")),
		"bytes.Builder.push_int8":        with(ints...),
		"bytes.Builder.push_int16_be":    with(ints...),
		"bytes.Builder.push_int16_le":    with(ints...),
		"bytes.Builder.push_int32_be":    with(ints...),
		"bytes.Builder.push_int32_le":    with(ints...),
		"bytes.Builder.push_int64_be":    with(ints...),
		"bytes.Builder.push_int64_le":    with(ints...),
		"bytes.Builder.push_float32_be":  with(floats...),
		"bytes.Builder.push_float32_le":  with(floats...),
		"bytes.Builder.push_float64_be":  with(floats...),
		"bytes.Builder.push_float64_le":  with(floats...),
		"bytes.Builder.push_uvarint":     with(hInt(0), hInt(127), hInt(128), hInt(300), hInt(math.MaxInt64), hBig("18446744073709551615"), hBig("1267650600228229401496703205376")),
		"bytes.Builder.push_varint":      with(ints...),
		"int.Int.wrapping_add_32":        pairs(ints...),
		"int.Int.wrapping_sub_32":        pairs(ints...),
		"int.Int.wrapping_mul_32":        pairs(ints...),
		"int.Int.rotate_left_32":         pairs(ints...),
		"int.Int.rotate_left_64":         pairs(ints...),
		"int.Int.leading_zeros_32":       unary(ints...),
		"int.Int.leading_zeros_64":       unary(ints...),
		"int.Int.trailing_zeros_32":      unary(ints...),
		"int.Int.trailing_zeros_64":      unary(ints...),
		"int.Int.swap_bytes_16":          unary(ints...),
		"int.Int.swap_bytes_32":          unary(ints...),
		"int.Int.swap_bytes_64":          unary(ints...),
		"int.Int.wrap_signed":            widths,
		"float.Float.from_bits":          unary(ints...),
		"float.Float.to_bits_32":         unary(floats...),
		"float.Float.from_bits_32":       unary(ints...),
	}
}
