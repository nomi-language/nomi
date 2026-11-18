package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A lambda parameter's DEFAULT value, lowered.
//
// # Absence is a static fact, never a value
//
// A default lowered inside the closure behind a zero-value test cannot
// distinguish "the argument was omitted" from "the argument was explicitly the
// type's zero value". Both are reachable in Nomi source and Nomi tells them
// apart:
//
//	fi = fn x = 5 -> x        fi(0)     -> 0      fi()  -> 5
//	fs = fn s = "hi" -> s     fs("")    -> ""     fs()  -> "hi"
//	fb = fn b = True -> b     fb(False) -> False  fb()  -> True
//
// So which slots a call omits is decided from the CALL's shape at build time
// and from nothing else. There is no presence flag at run time and no test on
// the incoming value anywhere in this file.
//
// # Why the default is lowered at the declaration and not at the call
//
// Lowering the default expression at each omitting call site is wrong for a
// lambda, in two ways:
//
//  1. A SHADOW between the declaration and the call.
//
//     x = 5 ; f = fn a = x -> a
//     f()                       -> 5
//     if True { x = 7 ; f() }   -> 5, with the inner x observably 7
//
//     A default lowered at the call site inside that block resolves `x` to
//     the inner binding and answers 7.
//
//  2. The lambda ESCAPES its declaring function.
//
//     fn build() -> () -> Int { n = 41 ; fn a = n + 1 -> a }
//     h = build() ; h()         -> 42
//
//     `n` is a local of `build` and is not in scope at the call site at all,
//     so there is no `n` for a call-site default to read.
//
// Both are instances of one rule: a default is evaluated in a fresh scope off
// the function's own closure, not the caller's. For a module `fn` that scope
// is the declaring module. A lambda's closure is a lexical position that a
// call site cannot rebuild, so the expression is lowered where that position
// is.
//
// # The encoding: a sibling supplier, not extra parameters
//
// Each defaulted slot gets a supplier: a second closure, built beside the
// lambda's own `ir.FuncValue`, whose body is the default expression
// (irLambdaGroup.suppliers, irlambda.go). An omitting call calls the supplier
// and passes its answer to the lambda; a supplying call passes its argument.
//
// The supplier sits beside the lambda and adds no presence parameter to it, so
// the lambda's function kind stays its Nomi type. A mask in the signature
// would change `(Int) -> Int` into a kind no `(Int) -> Int` position accepts.
// The shape is reachable: `apply2(fn x = 9 -> x)` is 1, a defaulted lambda passed
// where a plain function is expected, its default unused.
//
// Further properties this encoding reproduces:
//
//   - PER-OMITTING-CALL evaluation. A default whose expression prints fires on
//     the omitting calls only, once each — because the supplier is CALLED at
//     the call, not read from a variable initialized once.
//   - A default may name an EARLIER PARAMETER. `p = fn a: Int, b = a + 1 ->` gives
//     p(3) = 304 and p(3, 9) = 309, so the supplier takes the earlier slots as
//     its own parameters and the call passes the arguments it already lowered.
//   - An omitted default passed to later suppliers is materialized once per
//     call. Later defaults and the final call share that value, so a chain of
//     dependent defaults cannot repeat an earlier default's effects.
//   - A REBIND between declaration and call is NOT observed. `x = 5;
//     f = fn a = x -> a` then `x = 99` answers 5 both times: the default's
//     supplier is a closure, and a closure captures the value a name held when
//     it was made (spec §7 *Closures*).
//
// # What is refused, and why the supplier does not ride on the value
//
// The supplier is a separate local, so it does NOT travel on the func value. A
// short call whose callee the builder cannot trace back to a lambda literal
// therefore has no supplier to call, and is refused by name rather than lowered
// as an unseeded call.
//
// Bundling, wrapping every lowered lambda in a pair of the function and its
// suppliers so the supplier rides along, would change the representation of
// every Nomi lambda to serve the one caller that needs it.

// lambdaParamNames is a lambda literal's parameter names, positionally, for the
// callable-metadata entry.
//
// A destructuring parameter or bare `_` contributes no callable name. A
// descriptive discard such as `_label` remains a named argument label even
// though it introduces no local binding in the body.
func lambdaParamNames(t *ast.Lambda, arity int) []string {
	names := make([]string, arity)
	for i := range arity {
		if i >= len(t.Params) {
			break
		}
		p := t.Params[i]
		if p.Destructure != nil || p.Name == "_" {
			continue
		}
		names[i] = p.Name
	}
	return names
}

// namedArgNode returns the first argument written by NAME, or nil. It is how
// the value-call path phrases a declined plan: a named argument present means
// the decline is about placement, and its absence means it is about arity.
func namedArgNode(args []ast.Node) ast.Node {
	for _, a := range args {
		if _, isNamed := a.(*ast.NamedArg); isNamed {
			return a
		}
	}
	return nil
}
