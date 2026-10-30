package parser

import (
	"errors"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// An Int is exact at any size, so an integer literal of any length is an
// IntLit, in every place a literal is read: an expression, a case arm's
// pattern, a nested pattern and a negative pattern. A value that fits int64
// is Value; a larger one is Big. A negative pattern is the negated value, so
// -9223372036854775808 is the int64 math.MinInt64.
func TestIntLiteralOfAnySize(t *testing.T) {
	cases := []struct {
		name, src, want string
		big             bool
	}{
		{"expression", "fn f() -> Int {\n    186709961001538790100634132976990\n}", "186709961001538790100634132976990", true},
		{"underscores", "fn f() -> Int {\n    9_223_372_036_854_775_808\n}", "9223372036854775808", true},
		{"the largest int64", "fn f() -> Int {\n    9223372036854775807\n}", "9223372036854775807", false},
		{"hex", "fn f() -> Int {\n    0xFFFFFFFFFFFFFFFFFF\n}", "4722366482869645213695", true},
		{"binary", "fn f() -> Int {\n    0b11111111111111111111111111111111111111111111111111111111111111111\n}",
			"36893488147419103231", true},
		{"case arm", "fn f(n: Int) -> Int {\n    case n {\n        99999999999999999999 -> 1\n        _ -> 0\n    }\n}", "99999999999999999999", true},
		{"nested pattern", "fn f(p: #(Int, Int)) -> Int {\n    case p {\n        #(_, 99999999999999999999) -> 1\n        _ -> 0\n    }\n}", "99999999999999999999", true},
		{"negative pattern", "fn f(p: #(Int, Int)) -> Int {\n    case p {\n        #(_, -9223372036854775808) -> 1\n        _ -> 0\n    }\n}", "-9223372036854775808", false},
		{"negative pattern past int64", "fn f(n: Int) -> Int {\n    case n {\n        -9223372036854775809 -> 1\n        _ -> 0\n    }\n}", "-9223372036854775809", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := Parse(lexer.Lex(tc.src))
			if err != nil {
				t.Fatalf("the literal is rejected: %v", err)
			}
			var lits []*ast.IntLit
			for _, n := range file {
				ast.Inspect(n, func(n ast.Node) bool {
					if lit, ok := n.(*ast.IntLit); ok {
						lits = append(lits, lit)
					}
					return true
				})
			}
			var found *ast.IntLit
			for _, lit := range lits {
				if lit.Decimal() == tc.want {
					found = lit
				}
			}
			if found == nil {
				t.Fatalf("no literal of value %s among %d", tc.want, len(lits))
			}
			if (found.Big != nil) != tc.big {
				t.Errorf("Big set %v, want %v", found.Big != nil, tc.big)
			}
		})
	}
}

// A malformed literal is still reported as one.
func TestIntLiteralMalformed(t *testing.T) {
	err := parseError(t, "fn f() -> Int {\n    0b1__1\n}")
	var pe ParseError
	if !errors.As(err, &pe) || pe.Message != `invalid integer "0b1__1"` {
		t.Errorf("error %v, want invalid integer \"0b1__1\"", err)
	}
}
