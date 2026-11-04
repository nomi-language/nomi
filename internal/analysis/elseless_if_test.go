package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An `if` with no `else` whose branch has a value is a Maybe. The mismatch
// a forgotten `else` now leads to says where the Maybe came from, at a
// function's result, a `return`, a binding's annotation, an argument and a
// statement before a block's end. Each case fails if the front end starts
// admitting its source.
func TestElselessIfMismatchHint(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, help string
	}{
		{
			"function result",
			"fn label(n: Int) -> String {\n    if n > 0 { \"positive\" }\n}\n",
			"return type mismatch: expected String, got Maybe<String>",
			"an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to return a String",
		},
		{
			"else-if chain as the result",
			"fn label(n: Int) -> String {\n    if n > 0 { \"positive\" } else if n < 0 { \"negative\" }\n}\n",
			"return type mismatch: expected String, got Maybe<String>",
			"an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to return a String",
		},
		{
			"return",
			"fn label(n: Int) -> Int {\n    return if n > 0 { 1 }\n}\n",
			"return type mismatch: expected Int, got Maybe<Int>",
			"an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to return an Int",
		},
		{
			"annotation",
			"fn label(n: Int) -> Int {\n    x: Int = if n > 0 { 1 }\n    x\n}\n",
			"type mismatch: expected Int, got Maybe<Int>",
			"an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to make it an Int",
		},
		{
			"argument",
			"fn take(s: String) -> String {\n    s\n}\n\nfn label(n: Int) -> String {\n    take(if n > 0 { \"a\" })\n}\n",
			"argument 1: expected String, got Maybe<String>",
			"an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to pass a String",
		},
		{
			"statement before the end",
			"fn label(n: Int) -> Int {\n    if n > 0 { 5 }\n    n\n}\n",
			"non-final expression has type Maybe<Int>, but only Unit results can be ignored — write `_ = ...` if intentional, use `dbg ...` to inspect it, or move it to the end to return it",
			"an `if` without `else` whose branch has a value is a `Maybe`; to run the branch only for its effect, bind its value to `_` inside the branch, or add an `else` and use the `if`'s value",
		},
		{
			"Unit function's end",
			"fn label(n: Int) {\n    if n > 0 { 5 }\n}\n",
			"return type mismatch: expected Unit, got Maybe<Int>",
			"an `if` without `else` whose branch has a value is a `Maybe`; to run the branch only for its effect, bind its value to `_` inside the branch",
		},
		{
			"branches disagree",
			"fn label(n: Int) -> Maybe<Int> {\n    if n > 0 { 1 } else if n < 0 { \"a\" }\n}\n",
			"if/else branch type mismatch: then is Int, else is String",
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(tc.src)
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if tc.help == "" {
					for _, h := range e.Hints {
						if strings.Contains(h, "without `else`") {
							t.Fatalf("hint %q, want none", h)
						}
					}
					return
				}
				for _, h := range e.Hints {
					if h == tc.help {
						return
					}
				}
				t.Fatalf("hints %q, want %q among them", e.Hints, tc.help)
			}
			t.Fatalf("the front end accepts this, or reports something else: no %q error; got %v", tc.message, errs)
		})
	}
}

// The forms an `if` with no `else` now takes are accepted, with the type
// the `if` keyword's hover shows: a Maybe of the branches' value, Unit when
// a branch is Unit or leaves, and no flattening of a branch that is a Maybe.
// The expected type reaches the branch, so a dot-variant there resolves.
func TestElselessIfTypes(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"value", "fn f(c: Bool) -> Maybe<Int> {\n    if c { 5 }\n}\n", "Maybe<Int>"},
		{"else-if chain", "fn f(n: Int) -> Maybe<String> {\n    if n > 0 { \"a\" } else if n < 0 { \"b\" }\n}\n", "Maybe<String>"},
		{"chain whose first branch returns", "fn f(n: Int) -> Maybe<String> {\n    if n > 0 {\n        return None\n    } else if n < 0 {\n        \"b\"\n    }\n}\n", "Maybe<String>"},
		{"nested", "fn f(a: Bool, b: Bool) -> Maybe<Maybe<Int>> {\n    if a {\n        if b { 1 }\n    }\n}\n", "Maybe<Maybe<Int>>"},
		{"a Maybe branch", "fn f(c: Bool, m: Map<String, Int>) -> Maybe<Maybe<Int>> {\n    if c { Map.get(m, \"k\") }\n}\n", "Maybe<Maybe<Int>>"},
		{"expected type", "fn f(c: Bool) -> Maybe<Ordering> {\n    if c { .Less }\n}\n", "Maybe<Ordering>"},
		{"unannotated binding", "fn f(c: Bool) -> Maybe<Int> {\n    x = if c { 5 }\n    x\n}\n", "Maybe<Int>"},
		{"pattern if", "fn f(m: Maybe<Int>) -> Maybe<Int> {\n    if Some(n) = m { n + 1 }\n}\n", "Maybe<Int>"},
		{"returning branch", "fn f(n: Int) -> Result<Int, String> {\n    if n < 0 { return Err(\"neg\") }\n    Ok(n)\n}\n", "Unit"},
		{"Unit branch", "fn g() {\n}\n\nfn f(c: Bool) {\n    if c { g() }\n}\n", "Unit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(tc.src)
			if len(errs) > 0 {
				t.Fatalf("the front end rejects this: %v", errs)
			}
			line := 0
			for i, l := range strings.Split(tc.src, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "if ") || strings.Contains(l, "= if ") {
					line = i + 1
					break
				}
			}
			var got analysis.Type
			for pos, sym := range fa.References {
				if pos.Line == line && sym.ControlFlow != nil && sym.ControlFlow.Keyword == "if" {
					got = sym.ControlFlow.OutputTy
					break
				}
			}
			if got == nil || got.String() != tc.want {
				t.Fatalf("the `if` on line %d is %v, want %s", line, got, tc.want)
			}
		})
	}
}
