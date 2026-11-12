package format

import "testing"

// Nomi has no 1-tuple: `(T)` in a type is grouping, and the parser reads
// any number of grouping parentheses around a type as the type itself.
// nomi fmt drops them. A comment inside them keeps one pair, the type on
// its own line, in every type position; inside a list of types (generic
// arguments, function parameters) the list's own brackets hold it.
func TestFormat_GroupedType(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			"a payload in one pair",
			"enum S<T>\n    Completed(T)\n    Pending\nend\n",
			"enum S<T>\n    Completed T\n    Pending\nend\n",
		},
		{
			"a payload in two pairs",
			"enum S<T>\n    Completed((T))\n    Pending\nend\n",
			"enum S<T>\n    Completed T\n    Pending\nend\n",
		},
		{
			"a payload in three pairs",
			"enum S<T>\n    Completed(((T)))\n    Pending\nend\n",
			"enum S<T>\n    Completed T\n    Pending\nend\n",
		},
		{
			"a payload in two pairs after a space",
			"enum S<T>\n    Completed ((T))\n    Pending\nend\n",
			"enum S<T>\n    Completed T\n    Pending\nend\n",
		},
		{
			"a tuple payload in an extra pair",
			"enum S\n    Pair(((Int, String)))\nend\n",
			"enum S\n    Pair (Int, String)\nend\n",
		},
		{
			"a function payload in an extra pair",
			"enum S\n    F(((Int) -> Int))\nend\n",
			"enum S\n    F (Int) -> Int\nend\n",
		},
		{
			"a comment inside nested payload parentheses",
			"enum S\n    Full((\n        // c\n        Int))\nend\n",
			"enum S\n    Full (\n        // c\n        Int\n    )\nend\n",
		},
		{
			"parameter and return types",
			"fn f(x: ((Int))) -> ((Int))\n    x\nend\n",
			"fn f(x: Int) -> Int\n    x\nend\n",
		},
		{
			"a struct field",
			"struct B\n    v: ((Int))\nend\n",
			"struct B\n    v: Int\nend\n",
		},
		{
			"generic arguments",
			"fn f(xs: List<((Int))>, m: Map<(String), ((Int))>) -> Int\n    0\nend\n",
			"fn f(xs: List<Int>, m: Map<String, Int>) -> Int\n    0\nend\n",
		},
		{
			"a tuple and its elements",
			"fn f(t: ((Int, (String)))) -> Int\n    0\nend\n",
			"fn f(t: (Int, String)) -> Int\n    0\nend\n",
		},
		{
			"a function type, its parameter and its result",
			"fn f(h: (((Int)) -> ((Int)))) -> Int\n    h(1)\nend\n",
			"fn f(h: (Int) -> Int) -> Int\n    h(1)\nend\n",
		},
		{
			"a distinct type",
			"type W ((Int))\n",
			"type W Int\n",
		},
		{
			"a comment in a parameter type",
			"fn f(x: (\n    // c\n    Int)) -> Int\n    x\nend\n",
			"fn f(x: (\n    // c\n    Int\n)) -> Int\n    x\nend\n",
		},
		{
			"a comment in a return type",
			"fn f(x: Int) -> (Int // c\n)\n    x\nend\n",
			"fn f(x: Int) -> (\n    Int // c\n)\n    x\nend\n",
		},
		{
			"a comment in a struct field type",
			"struct B\n    v: (\n        // c\n        Int)\nend\n",
			"struct B\n    v: (\n        // c\n        Int\n    )\nend\n",
		},
		{
			"a comment in a function type's result",
			"fn f(h: (Int) -> (\n    // c\n    Int)) -> Int\n    h(1)\nend\n",
			"fn f(h: (Int) -> (\n    // c\n    Int\n)) -> Int\n    h(1)\nend\n",
		},
		{
			"a comment in a generic argument's parentheses",
			"fn f(xs: List<(\n    // c\n    Int)>) -> Int\n    0\nend\n",
			"fn f(xs: List<\n    // c\n    Int\n>) -> Int\n    0\nend\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
