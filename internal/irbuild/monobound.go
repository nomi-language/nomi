package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// A type parameter that appears ONLY inside a `where` bound:
// `fn saw_any<T, I>(it: I) -> Bool where I: Iter<T>`
// (`11-interfaces-and-impls/type_argument_inference/type_argument_inference_test.nomi`).
// No parameter or result names `T`, so the instantiated signature does not
// show it; the checker solves it from the impl the bound's solved subject
// has (solveFromWhereImpls) and records it with the call's substitution, and
// calleeTypeArgs reads it from there as it reads every other argument.

// boundMentionedParams extends monoTemplateFor's "every type parameter must be
// NAMED in the signature" rule to a `where` bound whose SUBJECT is itself
// named: `T` in `where I: Iter<T>` is determined exactly when `I` is.
//
// The subject must be named, which keeps the rule's point. `fn f<T, U>(x: T)
// where U: Iter<T>` does NOT admit `U`: `U` is the subject rather than an
// argument, and nothing at a call can determine it. Iterated to a fixed
// point, so a parameter a bound admits can in turn be a subject.
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
