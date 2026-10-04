package rt

import "testing"

// ABSOLUTE pins for the three byte rules.
//
// Two independent reasons, and they cover different rows:
//
//  1. ByteInRange, BytesSliceBounds and BytesDecode are the ONE implementation
//     of each rule. A comparison between two callers detects DISAGREEMENT, and
//     one implementation cannot disagree with itself, so only absolute values
//     say what it answers.
//
//  2. `Bytes.to_string` cannot appear in a builder fixture AT ALL. std
//     declares it twice for one receiver (inherent, returning
//     `Result<String, String>`, and `impl Display`, returning `String`) and
//     Nomi has no return-type overloading, so `addOverload` refuses both as
//     `ambiguous stdlib method`. So the decode rule — the sharpest one in the
//     module — has no other guard anywhere.
func TestByteInRangeIsInclusiveAtBothEnds(t *testing.T) {
	// Both boundaries and both failures. An off-by-one at either end is
	// exactly the mistake an implementation makes silently.
	for _, row := range []struct {
		n    int64
		want bool
	}{
		{-1, false}, {0, true}, {1, true},
		{254, true}, {255, true}, {256, false},
	} {
		if got := ByteInRange(row.n); got != row.want {
			t.Errorf("ByteInRange(%d) = %v, want %v", row.n, got, row.want)
		}
	}
}

func TestBytesSliceBoundsClampsAndEmptiesInvertedRanges(t *testing.T) {
	// std/bytes documents "out-of-range bounds are clamped" as the DEFINITION,
	// so these are specified answers rather than implementation detail.
	for _, row := range []struct {
		name               string
		length, start, end int64
		wantStart, wantEnd int64
	}{
		{"in range", 4, 1, 3, 1, 3},
		{"negative start clamps up", 4, -5, 2, 0, 2},
		{"end past the buffer clamps down", 4, 1, 99, 1, 4},
		{"both ends clamp", 4, -5, 99, 0, 4},
		// INVERTED: empty, and specifically 0,0 rather than a reversed or
		// negative-length range. A Go slice expression with start > end panics,
		// so answering this wrong is a crash and not a wrong value.
		{"inverted is empty", 4, 3, 1, 0, 0},
		{"inverted after clamping is empty", 4, 3, -1, 0, 0},
		{"empty buffer", 0, 0, 5, 0, 0},
	} {
		s, e := BytesSliceBounds(row.length, row.start, row.end)
		if s != row.wantStart || e != row.wantEnd {
			t.Errorf("%s: BytesSliceBounds(%d, %d, %d) = (%d, %d), want (%d, %d)",
				row.name, row.length, row.start, row.end, s, e, row.wantStart, row.wantEnd)
		}
		if s > e {
			t.Errorf("%s: produced start > end, which panics a slice expression", row.name)
		}
	}
}

// TestBytesToStringRejectsIllFormedUTF8 is the pin the fixture could not carry.
//
// The failure is the whole point. Go's `string(b)` conversion NEVER fails — it
// substitutes U+FFFD for each ill-formed sequence — so an implementation that
// reached for the obvious conversion returns a successful, silently corrupted
// string where Nomi's signature says `Result`. Cutting a two-byte character in
// half is the cheapest way to reach it and is exactly what
// `Bytes.slice(String.to_bytes("hé"), 0, 2)` produces.
func TestBytesToStringRejectsIllFormedUTF8(t *testing.T) {
	whole := Bytes("hé")
	if len(whole) != 3 {
		t.Fatalf(`"hé" is %d bytes, not 3 — this test's premise is the byte count`, len(whole))
	}
	if got := BytesToString(whole); got.Tag != TagOk || got.Ok != "hé" {
		t.Errorf("BytesToString(whole) = %+v, want Ok(\"hé\")", got)
	}
	// The first byte of é without its continuation byte.
	if got := BytesToString(whole[:2]); got.Tag != TagErr {
		t.Errorf("BytesToString(half a character) = %+v, want Err; "+
			"a Go string conversion would answer \"h\\ufffd\"", got)
	}
	// A lone continuation byte, which is ill-formed in a different way: not a
	// truncation but a byte that may never start a sequence.
	if got := BytesToString(Bytes([]byte{0xA9})); got.Tag != TagErr {
		t.Errorf("BytesToString(lone continuation byte) = %+v, want Err", got)
	}
	// Empty is valid and decodes to empty. The boundary a `!utf8.Valid` guard
	// gets right and a length-based one does not.
	if got := BytesToString(""); got.Tag != TagOk || got.Ok != "" {
		t.Errorf("BytesToString(empty) = %+v, want Ok(\"\")", got)
	}
}

// TestBytesToStringCarriesTheOneFaultLiteral pins that the Result payload is
// the shared constant rather than a second spelling. Two spellings of one fault
// drift apart.
func TestBytesToStringCarriesTheOneFaultLiteral(t *testing.T) {
	got := BytesToString(Bytes([]byte{0x68, 0xC3}))
	if got.Tag != TagErr {
		t.Fatalf("half a character decoded successfully: %+v", got)
	}
	if got.Err != InvalidUTF8Text {
		t.Errorf("Err payload is %q, want the shared %q", got.Err, InvalidUTF8Text)
	}
	ok := BytesToString(StringToBytes("hé"))
	if ok.Tag != TagOk || ok.Ok != "hé" {
		t.Errorf("BytesToString(valid) = %+v, want Ok(\"hé\")", ok)
	}
}

// TestBytesIsImmutableAcrossASlice is the property the `string` representation
// buys and a `[]byte` one cannot.
//
// A slice of a `[]byte` ALIASES its parent, so a later append into the parent's
// spare capacity mutates a value Nomi calls immutable. This representation
// cannot have the bug, and that is a reason for the choice rather than a
// consequence of it.
func TestBytesIsImmutableAcrossASlice(t *testing.T) {
	parent := StringToBytes("abcd")
	head := BytesSlice(parent, 0, 2)
	grown := BytesConcat(head, StringToBytes("ZZ"))
	if head != Bytes("ab") {
		t.Errorf("the slice changed when a derived value was built: %q", head)
	}
	if parent != Bytes("abcd") {
		t.Errorf("the parent changed: %q", parent)
	}
	if grown != Bytes("abZZ") {
		t.Errorf("BytesConcat = %q, want \"abZZ\"", grown)
	}
}
