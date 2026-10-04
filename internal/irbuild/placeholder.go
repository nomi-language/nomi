package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The `_` placeholder, which is TWO operations under one spelling — and one
// shared primitive that lets both be desugarings rather than second lowerings.
//
// # The two operations
//
//	10 |> divide(100, _)     is  divide(100, 10)          a SPLICE
//	add(1, _)                is  |y| add(1, y)            a CLOSURE
//
// They refused under one key (`placeholder`) at two sites — pipe.go's pipeCall
// and sugar.go's slotRoutedArg, reached from directCall — and nothing about
// them is shared except the token. The first places one already-computed
// value; the second builds a function value whose parameters are the holes.
//
// # `injected`, and why it is the whole of the mechanism
//
// Both lowerings need the same thing and the builder had no way to say it: put
// an ALREADY-LOWERED value at an argument position and let the ordinary call
// path take it from there. Without that, each would have had to re-derive
// argument coercion, slot routing, defaults and assertion recording — which is
// pipe.go's own stated reason for being a desugaring, and the reason its header
// gives for not reimplementing evalPipe's 350 lines.
//
// So `injected` binds a Go expression to a scope entry and hands back an
// `*ast.Ident` that resolves to it. Everything downstream is unchanged: the
// spliced call is an ordinary call and `g.call` decides everything about it.
//
// The scope KEY is unforgeable as Nomi source — it contains spaces and a `|`,
// which no Nomi identifier may hold — so a synthetic binding can never shadow
// a user's name and a user's name can never resolve to one. A unique suffix
// per injection makes nesting fall out: `x |> f(y |> g(_), _)` gives the inner
// and the outer hole two different keys, so neither can be read for the other.
//
// # Order and count, which are what a value-only fixture cannot see
//
// A pipe evaluates the piped value FIRST — before the callee is resolved and
// before any sibling argument — and substitutes that ONE value at EVERY
// placeholder position. So the left operand is forced into a temporary here,
// ahead of the spliced call, and the same temporary is named at each hole.
// Splicing the operand's NODE instead would evaluate it in the wrong position
// and once per hole, and would answer correctly while doing so.
//
// A partial application evaluates the BOUND arguments eagerly, at creation,
// and leaves the holes as parameters of the result. So the bound arguments
// are forced into temporaries OUTSIDE the built closure and
// captured; evaluating them inside would run them once per invocation and
// again answer correctly.
//
// testdata/placeholder.nomi pins both, and pins them as ORDER and COUNT rather
// than as values for exactly that reason.

// placeholderSlots is the written positions holding a bare `_`.
func placeholderSlots(args []ast.Node) []int {
	var out []int
	for i, a := range args {
		if _, isPlaceholder := a.(*ast.Placeholder); isPlaceholder {
			out = append(out, i)
		}
	}
	return out
}

// hasNamedArg reports whether any argument is written by name.
func hasNamedArg(args []ast.Node) bool {
	for _, a := range args {
		if _, isNamed := a.(*ast.NamedArg); isNamed {
			return true
		}
	}
	return false
}
