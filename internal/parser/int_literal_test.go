package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// An integer literal past Int's range is an error at the literal that says
// it is too large and gives the limit, in every place a literal is read: an
// expression, a case arm's pattern, a nested pattern and a negative
// pattern. A base-10 literal gets a hint naming the Decimal literal, and the
// smallest Int written as a negation gets one naming `Int.min_value`, or in
// a pattern, which cannot name it, the guard that matches it.
func TestIntLiteralTooLargeForInt(t *testing.T) {
	const tooLarge = " is too large for an `Int`, a 64-bit integer: an integer literal is at most 9223372036854775807"
	const minValue = "the smallest `Int`, -9223372036854775808, cannot be written as a literal; write `Int.min_value`"
	const minValuePattern = "the smallest `Int`, -9223372036854775808, cannot be written as a literal, and a pattern cannot name `Int.min_value`; match it with a guard: `n when n == Int.min_value -> ...`"
	cases := []struct {
		name, src, lexeme string
		col               int
		hints             []string
	}{
		{"expression", "fn f() -> Int {\n    186709961001538790100634132976990\n}", "186709961001538790100634132976990", 5,
			[]string{"for an exact number this large, write a Decimal literal: `186709961001538790100634132976990d`"}},
		{"underscores", "fn f() -> Int {\n    9_223_372_036_854_775_808\n}", "9_223_372_036_854_775_808", 5,
			[]string{"for an exact number this large, write a Decimal literal: `9_223_372_036_854_775_808d`"}},
		{"one past the largest", "fn f() -> Int {\n    9223372036854775808\n}", "9223372036854775808", 5,
			[]string{"for an exact number this large, write a Decimal literal: `9223372036854775808d`"}},
		{"the smallest Int", "fn f() -> Int {\n    -9223372036854775808\n}", "9223372036854775808", 6,
			[]string{minValue}},
		{"negative", "fn f() -> Int {\n    -99999999999999999999\n}", "99999999999999999999", 6,
			[]string{"for an exact number this large, write a Decimal literal: `99999999999999999999d`"}},
		{"hex", "fn f() -> Int {\n    0xFFFFFFFFFFFFFFFFFF\n}", "0xFFFFFFFFFFFFFFFFFF", 5, nil},
		{"binary", "fn f() -> Int {\n    0b11111111111111111111111111111111111111111111111111111111111111111\n}",
			"0b11111111111111111111111111111111111111111111111111111111111111111", 5, nil},
		{"case arm", "fn f(n: Int) -> Int {\n    case n {\n        99999999999999999999 -> 1\n        _ -> 0\n    }\n}", "99999999999999999999", 9,
			[]string{"for an exact number this large, write a Decimal literal: `99999999999999999999d`"}},
		{"nested pattern", "fn f(p: #(Int, Int)) -> Int {\n    case p {\n        #(_, 99999999999999999999) -> 1\n        _ -> 0\n    }\n}", "99999999999999999999", 14,
			[]string{"for an exact number this large, write a Decimal literal: `99999999999999999999d`"}},
		{"negative pattern", "fn f(p: #(Int, Int)) -> Int {\n    case p {\n        #(_, -9223372036854775808) -> 1\n        _ -> 0\n    }\n}", "9223372036854775808", 15,
			[]string{minValuePattern}},
		{"the smallest Int as an arm", "fn f(n: Int) -> Int {\n    case n {\n        -9223372036854775808 -> 1\n        _ -> 0\n    }\n}", "9223372036854775808", 10,
			[]string{minValuePattern}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseError(t, tc.src)
			var pe ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("error is %T (%v), want ParseError", err, err)
			}
			if want := "`" + tc.lexeme + "`" + tooLarge; pe.Message != want {
				t.Errorf("message %q, want %q", pe.Message, want)
			}
			if pe.Col != tc.col {
				t.Errorf("error at col %d, want %d (the literal)", pe.Col, tc.col)
			}
			if len(pe.Hints) != len(tc.hints) {
				t.Fatalf("hints %q, want %q", pe.Hints, tc.hints)
			}
			for i := range tc.hints {
				if pe.Hints[i] != tc.hints[i] {
					t.Errorf("hint %d %q, want %q", i, pe.Hints[i], tc.hints[i])
				}
			}
		})
	}
}

// A malformed literal is still reported as one, and the largest Int parses.
func TestIntLiteralMalformedAndLargest(t *testing.T) {
	err := parseError(t, "fn f() -> Int {\n    0b1__1\n}")
	var pe ParseError
	if !errors.As(err, &pe) || pe.Message != `invalid integer "0b1__1"` {
		t.Errorf("error %v, want invalid integer \"0b1__1\"", err)
	}
	if _, err := Parse(lexer.Lex("fn f() -> Int {\n    9223372036854775807\n}")); err != nil {
		t.Errorf("the largest Int literal is rejected: %v", err)
	}
}
