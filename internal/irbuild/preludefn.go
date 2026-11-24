package irbuild

// `impl Option<T>` and `impl Result<T, E>` reached through the QUALIFIED CALL
// spelling, at a statically-known concrete instantiation.
//
// # These are not dictionary-bound
//
// The stdlib index files these declarations as `stdlib generic function`, a
// name that says they need the type-parameter dispatch DICTIONARY. They do not.
// `typeParamsNeedDictionary` (generic.go) is this project's own line for that
// question, and `pub fn is_some<T>(option: Option<T>) -> Bool` has no bound, no
// `where` clause and calls no interface method on `T`. Its body is a `case`.
//
// stdCandidateFor (stdlib.go) decides `generic` from a purely syntactic
// property (`implGeneric || len(TypeParams) > 0 || len(WhereClauses) > 0`),
// and that arm of its switch comes BEFORE `blocked`, so a generic RECEIVER
// makes the whole surface unreachable through the index however scalar the
// instance is. The index cannot supply a type argument; the CALL SITE can,
// which is why this is an arm beside listCallPlan and mapCallPlan rather than
// a registry row.
//
// # Why this is an arm and not a stdlib registry row
//
// lists.go's header states the shape and it applies unchanged: the index can
// NAME `Option.is_some` while what LOWERS
// it is reading the receiver's KIND, which is the only place the concrete type
// argument exists. Placed beside listCallPlan in typeQualCall for the same
// reason, and like it, AHEAD of the index's route: moving that route above
// these arms would turn every lowered call back into the refusal.
//
// # `equal` is not implemented here, and that is the point
//
// `List.equal` shares the `==` operator's lowering, because std/lists.nomi
// defines the method AS `a == b`. The prelude enums are the same situation
// with the same hazard: std says `derive Equatable for Option<T>`, and `==` on
// a prelude instance already lowers. So `equal`, a call of a derived impl the
// machine computes by shape (machineImpls), is lowered by interfaceCall
// (ifacecall.go) as the comparison `a == b` is, and nothing else. A second
// comparator would have to agree with the operator by luck.
//
// The comparator carries three properties this file does not restate: Float
// routes through rt.EqFloat, so `Option.equal(Some(nan), Some(nan))` is True as
// Nomi requires; a List payload is compared by rt.ListEqual; and a
// NAMED payload compares STRUCTURALLY, which is what rt.Equal does and what
// `==` falls back to with no impl.
//
// # What is REFUSED here, by name
//
//   - A method `impl Option<T>` / `impl Result<T, E>` does not declare. Named
//     rather than declined so a typo cannot be read as a missing feature.
//
// Of the HIGHER-ORDER half, `Result.map_err` lowers in irresultmaperr.go, over
// the driver in rt/preludehof.go.
//
// `hash` is not implemented here either: std says `derive Hashable for
// Option<T>`, which writes no function, so `Option.hash(x)` is answered by
// the value's shape, the hash a Map key of the type takes, and
// interfaceCall lowers it so (hashValue).

// preludeFn is one lowered method of `impl Option<T>` or `impl Result<T, E>`.
type preludeFn struct {
	args int
}

// preludeFns is the lowered surface, minus `equal`.
//
// `equal` is absent on purpose and handled separately, for the reason
// listFuncs gives for the same omission: its lowering is the `==` operator's
// (interfaceCall), so a row here would make it a second encoding of one
// semantics.
//
// Keyed `Owner.method`, because the owner is part of the identity: `with_default`
// exists on both enums with different receivers and different tags.
var preludeFns = map[string]preludeFn{
	"Option.is_some":      {args: 1},
	"Option.is_none":      {args: 1},
	"Result.is_ok":        {args: 1},
	"Result.is_err":       {args: 1},
	"Option.with_default": {args: 2},
	"Result.with_default": {args: 2},
	"Option.to_result":    {args: 2},
	"Result.to_option":    {args: 1},
}

// preludeSpecNamed is the spec for one of the two enums this file constructs,
// looked up by Nomi name in the one table that declares them.
//
// A lookup rather than a package-level variable, because preludeSpecs is the
// single source of the shape and a second pointer to a member of it is a second
// thing to keep in step. Panics on a miss: the two names are literals in this
// file and a miss means preludeSpecs lost a member, which is a build-time fact
// and not an input.
func preludeSpecNamed(nomi string) *preludeSpec {
	for i := range preludeSpecs {
		if preludeSpecs[i].nomi == nomi {
			return &preludeSpecs[i]
		}
	}
	panic("irbuild: preludeSpecs has no " + nomi)
}
