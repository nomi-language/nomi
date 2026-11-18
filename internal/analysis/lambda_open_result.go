package analysis

import "slices"

// A lambda checked against an expected function type whose result is an
// interface (spec §7 *Lambdas*): `Route.to_with(route, fn _, p -> Ok(page(p)))`
// against `(Request, P) -> ToResponse`. The body's `Ok(...)` makes a
// `Result<Response, E>` whose E nothing names, and the interface erases the
// lambda's own result type, so nothing after the body can name it either.
// An inference variable the body leaves open is a type of which the body made
// no value (undetermined.go), so it is `Infallible`, the type with no values:
// the lambda's result is `Result<Response, Infallible>`, which implements
// `ToResponse` through `impl ToResponse for Infallible`. It is sound for any
// interface an impl for Infallible can meet, since that impl is only ever
// handed a value of the type, and there is none.
//
// A result that is not an interface keeps its variables: a concrete
// `Result<T, MyError>` already solves E through unification, and a generic
// result is solved by the call around the lambda.

// closeLambdaResultVars sets each variable still open in bodyTy to
// Infallible when expectedReturn is an interface.
func closeLambdaResultVars(expectedReturn, bodyTy Type) {
	if expectedReturn == nil || bodyTy == nil {
		return
	}
	if _, ok := resolveTypeVar(expectedReturn).(*InterfaceType); !ok {
		return
	}
	for _, v := range openTypeVars(bodyTy, nil) {
		v.Resolved = TypeInfallible
	}
}

// openTypeVars appends each unsolved variable in t to out, once.
func openTypeVars(t Type, out []*TypeVar) []*TypeVar {
	walkType(t, func(p Type) bool {
		switch tt := p.(type) {
		case *TypeVar:
			if tt.Resolved == nil && !slices.Contains(out, tt) {
				out = append(out, tt)
			}
		case *DistinctType:
			// Beside its type arguments, a distinct type holds the type
			// it wraps.
			out = openTypeVars(tt.Inner, out)
		}
		return true
	})
	return out
}
