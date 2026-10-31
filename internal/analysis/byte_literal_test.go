package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// expectLiteralTypes checks every Int literal the checker typed: one written
// in hex (`0x81`) is in a Byte position and must be a Byte, and every other
// one must be an Int.
func expectLiteralTypes(t *testing.T, fa *analysis.FileAnalysis) {
	t.Helper()
	seen := 0
	for node, ty := range fa.ExprTypes {
		lit, ok := node.(*ast.IntLit)
		if !ok || lit.Lexeme == "" {
			continue
		}
		seen++
		want := analysis.TypeInt
		if strings.HasPrefix(lit.Lexeme, "0x") {
			want = analysis.TypeByte
		}
		if analysis.ResolveTypeVar(ty) != want {
			t.Errorf("line %d: `%s` is %v, want %v", lit.Line, lit.Lexeme, ty, want)
		}
	}
	if seen == 0 {
		t.Fatal("no Int literal was typed")
	}
}

// An Int literal where a Byte is expected is a Byte, in each position that
// expects one.
func TestByteLiteralTakesItsTypeFromItsPosition(t *testing.T) {
	cases := []struct{ name, src string }{
		{"binding", "fn f() -> Byte {\n  b: Byte = 0x81\n  b\n}\n"},
		{"list element", "fn f() -> List<Byte> {\n  bytes: List<Byte> = [0x81, 0x00, 0x7F]\n  bytes\n}\n"},
		{"argument", "fn take(b: Byte) -> Byte {\n  b\n}\n\nfn f() -> Byte {\n  take(0x81)\n}\n"},
		{"list argument", "fn f() -> Bytes {\n  Bytes.from_list([0x68, 0x69])\n}\n"},
		{"struct field", "struct Packet {\n  tag: Byte\n  size: Int\n}\n\nfn f() -> Packet {\n  #Packet{tag: 0x07, size: 300}\n}\n"},
		{"tail", "fn f() -> Byte {\n  0xFF\n}\n"},
		{"return", "fn f(early: Bool) -> Byte {\n  if early { return 0x01 }\n  0x02\n}\n"},
		{"default", "fn f(b: Byte = 0x20) -> Byte {\n  b\n}\n"},
		{"once", "once start: Byte = 0x02\n\nfn f() -> Byte {\n  start\n}\n"},
		{"case pattern", "fn f(b: Byte) -> Int {\n  case b {\n    0x00 -> 1\n    0x7F -> 2\n    _ -> 3\n  }\n}\n"},
		{"payload pattern", "fn f(m: Maybe<Byte>) -> Int {\n  case m {\n    Some(0x81) -> 1\n    _ -> 2\n  }\n}\n"},
		{"equality right", "fn f(b: Byte) -> Bool {\n  b == 0x80\n}\n"},
		{"equality left", "fn f(b: Byte) -> Bool {\n  0x80 != b\n}\n"},
		{"list equality", "fn f(bytes: List<Byte>) -> Bool {\n  bytes == [0x81, 0x00]\n}\n"},
		{"bitwise right", "fn f(b: Byte) -> Byte {\n  b &&& 0x7F\n}\n"},
		{"bitwise left", "fn f(b: Byte) -> Byte {\n  0x80 ||| b ^^^ 0x0F\n}\n"},
		{"masked comparison", "fn f(b: Byte) -> Bool {\n  b &&& 0x80 == 0x00\n}\n"},
		{"generic constructor", "fn f() -> Maybe<Byte> {\n  Some(0x81)\n}\n"},
		{"lambda", "fn f(bytes: List<Byte>) -> List<Byte> {\n  bytes |> Iter.map(fn b -> b &&& 0x7F) |> Iter.to_list()\n}\n"},
		{"later list element", "fn f(b: Byte) -> List<Byte> {\n  [b, 0x42]\n}\n"},
		{"later branch", "fn f(b: Byte, keep: Bool) -> Byte {\n  x = if keep { b } else { 0x00 }\n  x\n}\n"},
		{"Int stays Int", "fn f() -> Int {\n  n = 129\n  n + 1\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(c.src)
			expectNoStdlibErrors(t, errs)
			expectLiteralTypes(t, fa)
		})
	}
}

// Hex, binary, octal and underscored literals all fit when their value does.
func TestByteLiteralSpellings(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f() -> List<Byte> {\n  [0xFF, 0b1000_0001, 0o377, 2_55, 0]\n}\n")
	expectNoStdlibErrors(t, errs)
}

// A literal outside 0 to 255 in a Byte position is an error that names the
// range, whatever its spelling and position.
func TestByteLiteralOutsideItsRange(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"decimal", "fn f() -> Byte {\n  300\n}\n", "`300` does not fit in a Byte (0 to 255)"},
		{"hex", "fn f() -> List<Byte> {\n  [0x100]\n}\n", "`0x100` does not fit in a Byte (0 to 255)"},
		{"binary", "fn f() -> Byte {\n  0b1_0000_0000\n}\n", "`0b1_0000_0000` does not fit in a Byte (0 to 255)"},
		{"past 64 bits", "fn f() -> Byte {\n  99999999999999999999999\n}\n", "`99999999999999999999999` does not fit in a Byte (0 to 255)"},
		{"negative", "fn f() -> Byte {\n  -1\n}\n", "`-1` does not fit in a Byte (0 to 255)"},
		{"negative zero", "fn f() -> Byte {\n  -0\n}\n", "a Byte literal has no sign: write `0` for `-0`"},
		{"pattern", "fn f(b: Byte) -> Int {\n  case b {\n    256 -> 1\n    _ -> 2\n  }\n}\n", "`256` does not fit in a Byte (0 to 255)"},
		{"negative pattern", "fn f(b: Byte) -> Int {\n  case b {\n    -2 -> 1\n    _ -> 2\n  }\n}\n", "`-2` does not fit in a Byte (0 to 255)"},
		{"comparison", "fn f(b: Byte) -> Bool {\n  b == 0x1FF\n}\n", "`0x1FF` does not fit in a Byte (0 to 255)"},
		{"operand", "fn f(b: Byte) -> Byte {\n  b ||| 512\n}\n", "`512` does not fit in a Byte (0 to 255)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(c.src)
			expectStdlibError(t, errs, c.want)
		})
	}
}

// Only a literal takes Byte from its position: an Int value never converts.
func TestByteLiteralDoesNotConvertAnInt(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f() -> Byte {\n  n = 0x81\n  b: Byte = n\n  b\n}\n")
	expectStdlibError(t, errs, "type mismatch: expected Byte, got Int")
}
