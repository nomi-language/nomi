package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Solving a type parameter that appears ONLY inside a `where` bound, from the
// bound SUBJECT's already-solved concrete kind.
//
// # The population, and why nothing else could serve it
//
// `fn first<T, I>(it: I): Maybe<T> where I: Iter<T>` and
// `fn saw_any?<T, I>(it: I): Bool where I: Iter<T>`
// (`11-interfaces-and-impls/type_argument_inference/type_argument_inference_test.nomi`).
// `T` is named by no parameter annotation, and in `saw_any?` by no return
// annotation either. Every channel monoTypeArgs already had is therefore silent
// about it:
//
//	the TURBOFISH        the corpus writes none, and requiring one is a
//	                     language change
//	the ARGUMENTS        `it: I` mentions `I` and not `T`, so unifying it
//	                     binds `I` and says nothing. dict.go's
//	                     dictSolveFromArgs states this case by name and
//	                     declines it: "`where I: Iter<T>` mentions `T` in a
//	                     BOUND rather than in any parameter annotation, so
//	                     `fn first<T, I>(it: I): Maybe<T>` does not solve `T`
//	                     here and must not appear to"
//	the SOLVED RETURN    the checker leaves it OPEN: the CallType
//	                     for `first([10, 20, 30])` is `Maybe<?2>` with `?2`
//	                     unresolved, so the front end does not propagate
//	                     through the bound either
//
// So the bound is the only thing in the program that relates `T` to anything,
// and reading it is the only way this population is served.
//
// # IT IS A READ-BACK, NOT AN INFERENCE, AND THAT IS THE WHOLE SAFETY ARGUMENT
//
// `I` is solved to a CONCRETE kind by the arguments before this runs. Asking
// "what are `Iter`'s type arguments for `*rt.List[int64]`" has a forced answer
// or none: a type conforms to a generic interface at ONE instantiation, and the
// front end has already accepted that it conforms. Nothing here searches,
// widens or guesses, and a subject whose conformance the builder cannot read
// back contributes NOTHING rather than a default.
//
// recoverTypeArgs' header defends the identical step for a struct literal in
// the same words — "that is a read-back, not an inference" — and
// dictSolveFromArgs' does for a nested parameter position. This is the same
// question one door over, so the traversal is `g.unifyTypeParams` and NOT a
// second walk: two unifiers would be two answers to one question, which is the
// resolver-family defect this package records six instances of.
//
// # ADDITIVE BY CONSTRUCTION
//
// It fires only for a type parameter NOTHING ELSE SOLVED (unifyTypeParams is
// first-wins on each name) and only when the read-back answers. So it can turn
// a refusal into a lowering and never the reverse, which is what makes it safe
// to place after every existing source rather than needing a precedence
// argument of its own.
//
// # WHAT IT DOES NOT DO, named rather than left to be discovered
//
// A bound whose conformance has no read-back here — a stdlib generic interface
// with no `impl` in this program and no protocol arm below — contributes
// nothing, and `monoTypeArgs`' own `unsolved` path then reports
// `undetermined type argument` at the call exactly as it does today.
// TestMonoBound_AnUnreadableBoundStillRefuses is that reachability witness.

// boundMentionedParams extends monoTemplateFor's "every type parameter must be
// NAMED in the signature" rule to a `where` bound whose SUBJECT is itself
// solvable.
//
// The old rule's reason, quoted in monoTemplateFor, is that "a type parameter no
// parameter mentions cannot be solved from the arguments and nothing could
// produce a value of it inside the body either". The return half of that was
// already retracted once, for `fn plus<L, R, Out>(lhs: L, rhs: R): Out where
// L: Add<R, Out>` — `Out` is named only by the return. THIS is the same
// retraction one step further: `T` in `where I: Iter<T>` is solvable exactly
// when `I` is, and monoSolveFromBounds is what solves it.
//
// GATED ON THE SUBJECT BEING SOLVABLE, which is what keeps the original half of
// the rule intact. `fn f<T, U>(x: T) where U: Iter<T>` does NOT admit `U`: the
// relation runs the wrong way, `U` is the subject rather than an argument, and
// nothing at a call can determine it. So a parameter named nowhere reachable is
// still refused at its declaration, and this only adds the positions a
// read-back can close.
//
// Iterated to a fixed point for monoSolveFromBounds' reason, and the two must
// agree about which positions are reachable: this decides whether the template
// is CLAIMED and that decides whether it can be SOLVED, so a position admitted
// here and not solved there becomes `undetermined type argument` at the call
// rather than a wrong answer.
func boundMentionedParams(fd *ast.FuncDef, params, mentioned map[string]bool) {
	if len(fd.WhereClauses) == 0 {
		return
	}
	for range len(params) {
		before := len(mentioned)
		for _, wc := range fd.WhereClauses {
			if !mentioned[wc.Name] {
				continue
			}
			for _, b := range wc.Bounds {
				if _, generic := b.(*ast.GenericType); !generic {
					continue
				}
				markMentionedParams(b, params, mentioned)
			}
		}
		if len(mentioned) == before {
			return
		}
	}
}
