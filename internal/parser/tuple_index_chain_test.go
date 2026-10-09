package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `t.1.0` reads as `(t.1).0`, although the lexer reads `1.0` as a Float:
// each index is its own field access, positioned at its own digits and dot.
func TestTupleIndexChain_FloatTokenSplits(t *testing.T) {
	expr := parseExpr(t, "t.1.0.x")
	outer, ok := expr.(*ast.FieldAccess)
	if !ok || outer.Field.Name != "x" {
		t.Fatalf("want .x outermost, got %#v", expr)
	}
	second, ok := outer.Object.(*ast.FieldAccess)
	if !ok || second.Field.Name != "0" || second.Col != 4 || second.Field.Col != 5 {
		t.Fatalf("want .0 at col 4 (index at 5), got %#v", outer.Object)
	}
	first, ok := second.Object.(*ast.FieldAccess)
	if !ok || first.Field.Name != "1" || first.Col != 2 || first.Field.Col != 3 {
		t.Fatalf("want .1 at col 2 (index at 3), got %#v", second.Object)
	}
	if id, ok := first.Object.(*ast.Ident); !ok || id.Name != "t" {
		t.Fatalf("want t innermost, got %#v", first.Object)
	}
}

// A Float with an exponent, a separator or a suffix is not two indices.
func TestTupleIndexChain_OtherFloatsAreStillErrors(t *testing.T) {
	for _, src := range []string{"t.1e3", "t.1_0.0", "t.1.5d"} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("Parse(%q) succeeded; want the field-name error", src)
		}
	}
}

// A tuple index is plain decimal digits with no leading zero, so each
// position has one spelling: `t.01`, `t.00`, `t.1_0` and `t.0x1` are parse
// errors, after a value and in an accessor alike, rather than other names
// for `t.1` or fields no tuple has.
func TestTupleIndex_OneSpellingPerPosition(t *testing.T) {
	for _, src := range []string{
		"t.00", "t.01", "t.1_0", "t.0x1", "t.0b1", "t.0o1",
		"t.1.00", "t.01.1", "t.1 .00", "0 .000",
		"f(.01)", "f(.0x1)", "f(.x.01)", "f(.1.01)",
	} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "tuple index") {
			t.Errorf("Parse(%q) = %v; want the tuple index error", src, err)
		}
	}
	for _, src := range []string{"t.0", "t.10", "t.1.0", "t.10.20", "f(.0)", "f(.10)", "f(.x.10)"} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("Parse(%q): %v", src, err)
		}
	}
}

// `.1.0` in an accessor is two indices, as `t.1.0` is, and the same path
// as `.1 .0`, which the formatter writes as `.1.0`.
func TestTupleIndex_AccessorSplitsFloatToken(t *testing.T) {
	for _, src := range []string{"f(.1.0)", "f(.1 .0)", "f(.x.1.0)", "f(.x.1 .0)"} {
		call, ok := parseExpr(t, src).(*ast.Call)
		if !ok || len(call.Args) != 1 {
			t.Fatalf("Parse(%q): want a call with one argument", src)
		}
		acc, ok := call.Args[0].(*ast.FieldAccessor)
		if !ok {
			t.Fatalf("Parse(%q): want an accessor argument, got %T", src, call.Args[0])
		}
		want := ".1.0"
		if strings.Contains(src, "x") {
			want = ".x.1.0"
		}
		if got := acc.Spelling(); got != want {
			t.Errorf("Parse(%q): accessor %q, want %q", src, got, want)
		}
	}
}
