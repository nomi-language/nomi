package parser

import (
	"fmt"
	"strings"
	"testing"
)

// `&&` and `||` are the C-family spellings of `and` and `or`. Each is a parse
// error that names the Nomi keyword, at the operator.
func TestCFamilyLogicalOperatorsNameTheKeyword(t *testing.T) {
	for _, c := range []struct {
		src, want string
		col       int
	}{
		{"fn f(a: Bool, b: Bool) -> Bool {\n    a && b\n}\n", "write `a and b`", 7},
		{"fn f(a: Bool, b: Bool) -> Bool {\n    a || b\n}\n", "write `a or b`", 7},
		{"fn f(a: Bool, b: Bool) -> Bool {\n    ok = a || b\n    ok\n}\n", "write `a or b`", 12},
		{"fn f(a: Bool, b: Bool) -> Int {\n    if a || b { 1 } else { 2 }\n}\n", "write `a or b`", 10},
		{"fn f(a: Bool, b: Bool) -> Bool {\n    a || { b }\n}\n", "write `a or b`", 7},
	} {
		err := parseError(t, c.src)
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) = %v, want an error containing %q", c.src, err, c.want)
		}
		if at := fmt.Sprintf("line 2, col %d:", c.col); !strings.HasPrefix(err.Error(), at) {
			t.Errorf("Parse(%q) = %v, want it at line 2, column %d", c.src, err, c.col)
		}
	}
}

// A zero-parameter lambda where nothing precedes it on its line, after a
// `;`, as an argument, or as a lambda's body still parses.
func TestZeroParameterLambdasStillParse(t *testing.T) {
	for _, src := range []string{
		"f = fn -> 1\n",
		"g = fn x = 1 -> fn -> x\n",
		"h = fn -> fn -> 1\n",
		"apply(fn -> 1)\n",
		"fn f() -> () -> Int {\n    x = 1\n    fn -> x\n}\n",
		"fn f() -> () -> Int {\n    x = 1; fn -> x\n}\n",
		"fn f(a: Bool, b: Bool) -> Bool {\n    a\n        fn -> b\n}\n",
	} {
		parse(t, src)
	}
}
