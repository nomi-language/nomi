package rt

import (
	"strings"
	"unicode/utf8"
)

// std/bytes.Byte and std/bytes.Bytes, the two `pub host type` declarations,
// and the rules over them.
//
// # Why `Bytes` is a Go `string` and not a `[]byte`
//
// std calls it an "immutable byte buffer", and Go has exactly one immutable
// byte sequence: `string`. Three consequences, all of them the reason rather
// than a convenience:
//
//   - `==` WORKS. std/bytes declares `impl Equatable for Bytes` whose body is
//     `a == b`, and a `[]byte` is not comparable in Go, so that declaration
//     could never lower against a slice representation — it would have to be a
//     hand-written comparator here, which is a second encoding of a rule std
//     already states in Nomi.
//   - IMMUTABILITY IS ENFORCED rather than promised. `BytesSlice` over a
//     `[]byte` returns a slice ALIASING the input, so a later append into the
//     parent's spare capacity mutates a value Nomi calls immutable. A string
//     cannot have the bug.
//   - A slice is one word larger and carries a capacity nothing here may use.
//
// The cost is the conversion at the FFI edge. Inside the VM a value holds
// `rt.Bytes` end to end, and only `StringToBytes` / `BytesToString` cross,
// which are the conversions the operation is anyway.
//
// The VM stores this immutable Bytes value in its string register bank and in
// its boxed values. Mutable Go byte slices are copied at the FFI boundary;
// slicing, concatenation, iteration, decoding, equality and hashing operate on
// this shared storage within the language runtime.

// Byte is std/bytes.Byte: one octet.
//
// int64 would have matched the OTHER host-type-adjacent precedent in this
// package (rt.Codepoint is int64 because std declares `opaque type Codepoint
// Int` and the inner type is part of that declaration). `Byte` is different in
// exactly the way that matters: `pub host type Byte` declares NO inner type, so
// there is no width to match and nothing to truncate against. uint8 is then the
// honest representation — it makes the 0..255 invariant hold by construction in
// Go rather than by a predicate nobody re-checks.
type Byte uint8

// Bytes is std/bytes.Bytes: an immutable byte buffer. See the header.
type Bytes string

// ByteInRange is the `Byte.from_int` domain: 0 through 255, inclusive at both
// ends.
//
// Exported as a predicate rather than folded into ByteFromInt so a caller that
// holds a boxed Int need not carry a second copy of the bounds. An off-by-one
// at either end is precisely the mistake a second copy makes, which is why
// both boundary values and both failures are rows in
// internal/irbuild/testdata/std_host_type_boundary.nomi.
func ByteInRange(n int64) bool { return n >= 0 && n <= 255 }

// ByteFromInt is `Byte.from_int`.
func ByteFromInt(n int64) Maybe[Byte] {
	if !ByteInRange(n) {
		return None[Byte]()
	}
	return Some(Byte(n))
}

// ByteToInt is `Byte.to_int`.
func ByteToInt(b Byte) int64 { return int64(b) }

// BytesLength is `Bytes.length`: the count of OCTETS.
//
// Not characters and not grapheme clusters. "hé" is two graphemes, two Unicode
// scalar values and THREE bytes, and this answers 3 — the distinction the
// fixture's first row exists to pin, because a plausible implementation over
// `[]rune` answers 2 and every other row still passes.
func BytesLength(data Bytes) int64 { return int64(len(data)) }

// BytesAt is `Bytes.at`: the byte at index, or None when out of range.
func BytesAt(data Bytes, index int64) Maybe[Byte] {
	if index < 0 || index >= int64(len(data)) {
		return None[Byte]()
	}
	return Some(Byte(data[index]))
}

// BytesSliceBounds is `Bytes.slice`'s CLAMP, resolved against a length.
//
// std/bytes documents "out-of-range bounds are clamped" as the definition, so
// this is a specified rule and not an implementation detail. Two separate
// decisions live here and both are observable: a negative start clamps up to 0
// and an end past the buffer clamps down to the length, and INVERTED bounds
// (start > end after clamping) answer an EMPTY range rather than trapping or
// reversing. Returning the pair rather than the slice is what lets a caller
// apply it to a `[]byte` without a conversion.
func BytesSliceBounds(length, start, end int64) (int64, int64) {
	if start < 0 {
		start = 0
	}
	if end > length {
		end = length
	}
	if start > end {
		return 0, 0
	}
	return start, end
}

// BytesSlice is `Bytes.slice`.
func BytesSlice(data Bytes, start, end int64) Bytes {
	s, e := BytesSliceBounds(int64(len(data)), start, end)
	return data[s:e]
}

// BytesConcat is `Bytes.concat`, in argument order.
func BytesConcat(a, b Bytes) Bytes { return a + b }

// bytesDecodeString is `Bytes.to_string`'s VALIDITY rule: the decoded text and
// whether the octets were well-formed UTF-8. Go's string(b) conversion always
// succeeds, including on ill-formed UTF-8, so the check is explicit.
func bytesDecodeString(data string) (string, bool) {
	if !utf8.ValidString(data) {
		return "", false
	}
	return data, true
}

// BytesHash is `Hashable.hash` for Bytes: FNV-1a 64-bit over the octets, as
// StringHash is over a String's. Equal Bytes hash equally, which is all the
// Hashable contract asks; a Map key or Set element of Bytes is bucketed by
// HashBytes, the kernel's own hash.
func BytesHash(data Bytes) int64 { return StringHash(string(data)) }

// BytesToString is `Bytes.to_string`.
//
// The error payload is produced HERE so there is one of it: rt/trap.go's rule for fault text applied
// to a `Result` payload rather than to a trap.
func BytesToString(data Bytes) Result[string, string] {
	text, valid := bytesDecodeString(string(data))
	if !valid {
		return Err[string, string](InvalidUTF8Text)
	}
	return Ok[string, string](text)
}

// InvalidUTF8Text is the `Bytes.to_string` failure payload. One literal,
// because two spellings of one fault drift apart.
const InvalidUTF8Text = "invalid UTF-8"

// StringToBytes is `String.to_bytes`: the string's UTF-8 octets.
//
// Total in the direction that matters — a Nomi String is already valid UTF-8,
// so this cannot fail, which is why the Nomi signature returns `Bytes` rather
// than a `Result`. It lives here rather than in rt/text.go because the TYPE it
// produces is declared here and the file that owns a type owns its
// constructors.
func StringToBytes(s string) Bytes { return Bytes(s) }

// InspectByte is how a `Byte` reads in an assertion's `values:` row, and it is
// the ONLY spelling of it: RowText calls this, as it does InspectUnit.
//
// It is the plain decimal octet — `97`, not `Byte(97)` and not `'a'` — MEASURED
// rather than derived from the type's name. That matters because `Byte` has
// BOTH a std `impl Display` (decimal) and a std `impl Debug`
// (`Display.to_string(b)`, also decimal), and a `values:` row is neither of
// them: it is RowText's structural rendering. The three
// happen to agree here, which is exactly why reaching for the wrong one would
// have gone unnoticed.
func InspectByte(b Byte) string { return FormatInt(int64(b)) }

// InspectBytes is how a `Bytes` reads in an assertion's `values:` row, and it is
// the ONLY spelling of it: RowText calls this.
//
// `<<97, 98>>`, and empty is `<<>>`. This one is NOT derived and that is the
// whole reason it exists as a named function: the derived rendering for an
// rt-opaque leaf is the BARE TYPE NAME, so a `Bytes` operand would read
// `Bytes` rather than `<<97, 98>>`. A wrong answer rather than a missing one.
//
// std/bytes also writes `impl Debug for Bytes` producing the same text. That is
// an AGREEMENT, not a competition: the row is RowText, which calls this, and
// the Nomi body is a separate path that happens to spell the same format. This
// function is the one Go spelling; the Nomi one is a std-source fact.
func InspectBytes(data Bytes) string {
	if len(data) == 0 {
		return "<<>>"
	}
	var b strings.Builder
	b.WriteString("<<")
	for i := range len(data) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(FormatInt(int64(data[i])))
	}
	b.WriteString(">>")
	return b.String()
}

// BytesToList is `Bytes.to_list`: the octets as a Nomi `List<Byte>`.
//
// Left unbound when the `Byte`/`Bytes` rows landed, on the stated ground that
// "`to_list` with no way to traverse the result finishes nothing". That ground
// is spent: `rt.BytesSeq` gives a `Bytes` an `Iter` source (see
// internal/irbuild/iterext.go), and `*rt.List[rt.Byte]` is a neutral structural
// instance interned process-wide, so the result is both traversable and
// nameable in a signature.
//
// Built back-to-front because rt.List is a cons list: prepending from the last
// octet is one allocation per element with no reversal pass.
func BytesToList(data Bytes) *List[Byte] {
	var out *List[Byte]
	for i := len(data) - 1; i >= 0; i-- {
		out = Cons(Byte(data[i]), out)
	}
	return out
}

// BytesFromList is `Bytes.from_list`. A nil list IS the empty list, so the
// empty buffer needs no special case.
func BytesFromList(items *List[Byte]) Bytes {
	var b strings.Builder
	if items != nil {
		b.Grow(int(items.Len))
	}
	for cur := items; cur != nil; cur = cur.Tail {
		b.WriteByte(byte(cur.Head))
	}
	return Bytes(b.String())
}

// HashByte and HashBytes are the STRUCTURAL hash of a `Byte` and a `Bytes` —
// the one that buckets a Map key, not `Hashable.hash`. See rt/hash.go's header
// for why those are different functions.
//
// They live here rather than in rt/hash.go because the file that owns a type
// owns its rules, which is StringToBytes' stated reason one screen up.
//
// # WHY THESE EXIST AT ALL, since the record said they should not
//
// internal/irbuild's `valueHash` had no arm for either type and `valueEqual` had
// none either, and maps.go says why in as many words: `namedHash` declines at
// `case d.rtOpaque` because "rt makes no such claim for `Byte`/`Bytes`", and
// `valueEqual`'s matching decline adds that "equality without a matching hash
// is a wrong BUCKET rather than a wrong answer. The pair moves together or not
// at all."
//
// That is exactly right, and the way to satisfy it is to MAKE THE CLAIM rather
// than to work around it. So rt states both rules here, rt.HashDecimal's
// shape, and Hash calls them.
//
// The pair moves together: irbuild gained BOTH arms in the same change, and
// `Bytes`'s equality is `rt.Eq[Bytes]`, a Go `==` on a string, which is the
// same byte equality Equal uses.
func HashByte(b Byte) uint64 { return uint64(b) }

// HashBytes is FNV-1a over the octets. Routed through StringHash — Nomi's `String.hash`, the same
// FNV — because a second FNV loop in this package is the "one FNV, not two"
// rule rt/hash.go states. `rt.Bytes` IS a Go string, so this is not a
// conversion.
func HashBytes(data Bytes) uint64 { return uint64(StringHash(string(data))) }
