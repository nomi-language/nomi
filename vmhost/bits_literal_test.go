package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A backtick Bits literal is the pattern its handler parses, checked when
// the program loads.
func TestBitsLiteral_IsTheParsedPattern(t *testing.T) {
	const src = "import {\n" +
		"    std/bits.Bits\n" +
		"    std/io\n" +
		"}\n\n" +
		"once lead = Bits`110a_aaaa`\n\n" +
		"fn main() {\n" +
		"    io.print(Bits.matches(lead, 0b1100_0011))\n" +
		"    io.print(Debug.inspect(Bits.field(lead, 0b1100_0011)))\n" +
		"    io.print(Debug.inspect(lead))\n" +
		"}\n"
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects a valid Bits literal: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "True\nSome(3)\nBits`110a_aaaa`\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A malformed backtick Bits is a compile error at the literal, worded by
// bits.Error's Display.
func TestBitsLiteral_InvalidTextIsACompileError(t *testing.T) {
	for _, c := range []struct{ literal, message string }{
		{"Bits``", "typed literal Bits`` is invalid: a bit pattern needs at least one bit: `0`, `1`, `x` or a field letter"},
		{"Bits`__`", "typed literal Bits`__` is invalid: a bit pattern needs at least one bit: `0`, `1`, `x` or a field letter"},
		{"Bits`10x2`", "typed literal Bits`10x2` is invalid: `2` at index 3 is not a bit: a bit pattern is written in `0`, `1`, `x`, one lowercase field letter and `_`"},
		{"Bits`10X0`", "typed literal Bits`10X0` is invalid: `X` at index 2 is not a bit: a bit pattern is written in `0`, `1`, `x`, one lowercase field letter and `_`"},
		{"Bits`aa_bb`", "typed literal Bits`aa_bb` is invalid: `b` at index 3 names a second field: this pattern's field is `a`, and a pattern has one"},
		{"Bits`aa0a`", "typed literal Bits`aa0a` is invalid: `a` at index 3 splits field `a`: write its bits together"},
	} {
		src := "import std/bits.Bits\n\nfn main() {\n    _ = " + c.literal + "\n}\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.literal, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.literal, ds[0].Message, c.message)
		}
		if ds[0].Code != InvalidLiteralCode {
			t.Errorf("%s: code = %q, want %q", c.literal, ds[0].Code, InvalidLiteralCode)
		}
	}
}
