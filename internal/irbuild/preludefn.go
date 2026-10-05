package irbuild

// `impl Maybe<T>` and `impl Result<T, E>` reached through the QUALIFIED CALL
// spelling, at a statically-known concrete instantiation.
//
// # These are not dictionary-bound
//
// The stdlib index files these declarations as `stdlib generic function`, a
// name that says they need the type-parameter dispatch DICTIONARY. They do not.
// `typeParamsNeedDictionary` (generic.go) is this project's own line for that
// question, and `pub fn some?<T>(maybe: Maybe<T>): Bool` has no bound, no
// `where` clause and calls no interface method on `T`. Its body is a `case`.
//
// stdCandidateFor (stdlib.go) decides `generic` from a purely syntactic
// property (`implGeneric || len(TypeParams) > 0 || len(WhereClauses) > 0`),
// and that arm of its switch comes BEFORE `blocked`, so a generic RECEIVER
// makes the whole surface unreachable through the index however scalar the
// instance is. The index cannot supply a type argument; the CALL SITE can,
// which is why this is an arm beside listCall and mapCall rather than a
// registry row.
//
// # Why this is an arm and not a stdlib registry row
//
// lists.go's header states the shape and it applies unchanged: the index can
// NAME `Maybe.some?` while what LOWERS
// it is reading the receiver's KIND, which is the only place the concrete type
// argument exists. Placed beside listCall in implCall for the same reason, and
// like it, AHEAD of stdlibCall: moving stdlibCall above these arms would turn
// every lowered call back into the refusal.
//
// # `equal?` is not implemented here, and that is the point
//
// collections.go's listEqual is "the ONE lowering of Nomi list equality, shared
// by the `==` operator and by `List.equal?`", because std/lists.nomi defines the
// method AS `a == b`. The prelude enums are the same situation with the same
// hazard: std says `derive Equatable for Maybe<T>`, and `==` on a prelude
// instance already lowers through g.valueEqual (native.go's equality arm, via
// the `left.k.def != nil` path). So `equal?` routes to g.valueEqual and nothing
// else. A second comparator would have to agree with the operator by luck.
//
// The comparator carries three properties this file does not restate: Float
// routes through rt.EqFloat, so `Maybe.equal?(Some(nan), Some(nan))` is True as
// Nomi requires and not false as Go's IEEE `==` would answer; a List payload
// recurses into rt.ListEqual rather than comparing cons cells by ADDRESS; and a
// NAMED payload compares STRUCTURALLY, which is what rt.Equal does and what
// `==` falls back to with no impl.
//
// # What is REFUSED here, by name
//
//   - A method `impl Maybe<T>` / `impl Result<T, E>` does not declare. Named
//     rather than declined so a typo cannot be read as a missing feature.
//
// The HIGHER-ORDER half (`map`, `flat_map`, `map_err`) lowers in
// preludehof.go: the callback's result type is `funcResult(cb.k)`.
//
// `hash` is not maps.go's key hashing. maps.go's valueHash is the STRUCTURAL,
// un-overridable hash that buckets a Map key, and rt/hash.go's own file
// comment says nothing observes its number, while `Hashable.hash(m)` is
// observable. `Maybe.hash(Some(4))` is 4 and `Result.hash(Err("bad"))` is
// 31 + StringHash("bad"); routing either to valueHash would produce a
// different Int for the same program. See preludeHashCall below.

// preludeFnRecv names which prelude enum a method's FIRST argument instantiates.
type preludeFnRecv uint8

const (
	recvMaybe preludeFnRecv = iota
	recvResult
)

// preludeFnResult says how a call's result kind is derived from the receiver.
type preludeFnResult uint8

const (
	// resBool is `Bool`, independent of the receiver's type arguments.
	resBool preludeFnResult = iota
	// resPayload is the receiver's FIRST type argument: `Maybe<T>` -> T,
	// `Result<T, E>` -> T. It is also the type the second argument coerces to.
	resPayload
	// resResultOfMaybe is `Result<T, E>` where T is the receiving Maybe's
	// argument and E is the second argument's own kind.
	resResultOfMaybe
	// resMaybeOfResult is `Maybe<T>` where T is the receiving Result's first
	// argument. The E is discarded, which is what the conversion means.
	resMaybeOfResult
)

// preludeFn is one lowered method of `impl Maybe<T>` or `impl Result<T, E>`.
type preludeFn struct {
	recv preludeFnRecv
	args int
	res  preludeFnResult
	// variant, when non-empty, names the variant a PREDICATE tests for. The tag
	// is then read off the receiver's own typeDef rather than written here, so
	// a reordered preludeSpec cannot desync this table from case.go's test.
	variant string
	// rtCall, when non-empty, is the rt function the call lowers to. Used for
	// the four methods that SELECT between two values: Go has no conditional
	// expression, so they cannot be emitted inline.
	rtCall string
}

// preludeFns is the lowered surface, minus `equal?`.
//
// `equal?` is absent on purpose and handled separately, for the reason
// listFuncs gives for the same omission: its lowering is g.valueEqual, shared
// with the `==` operator, so a row here would have made it a second encoding of
// one semantics.
//
// Keyed `Owner.method`, because the owner is part of the identity: `with_default`
// exists on both enums with different receivers and different tags.
var preludeFns = map[string]preludeFn{
	"Maybe.some?":         {recv: recvMaybe, args: 1, res: resBool, variant: "Some"},
	"Maybe.none?":         {recv: recvMaybe, args: 1, res: resBool, variant: "None"},
	"Result.ok?":          {recv: recvResult, args: 1, res: resBool, variant: "Ok"},
	"Result.err?":         {recv: recvResult, args: 1, res: resBool, variant: "Err"},
	"Maybe.with_default":  {recv: recvMaybe, args: 2, res: resPayload, rtCall: "rt.MaybeWithDefault"},
	"Result.with_default": {recv: recvResult, args: 2, res: resPayload, rtCall: "rt.ResultWithDefault"},
	"Maybe.to_result":     {recv: recvMaybe, args: 2, res: resResultOfMaybe, rtCall: "rt.MaybeToResult"},
	"Result.to_maybe":     {recv: recvResult, args: 1, res: resMaybeOfResult, rtCall: "rt.ResultToMaybe"},
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

// preludeArgUsable reports whether k can be a prelude type argument read off an
// argument expression.
//
// The four UNTYPED literal kinds are excluded for bindableTypeArg's reason,
// stated there: `[]`, a bare `None`, `Map.empty()` and `#{}` have no type of their own,
// so binding one would fix the artifact's element type from a literal whose real
// type the program decides elsewhere. Here that would make
// `Maybe.to_result(m, [])` produce a `Result<T, List<Unit>>`.
func preludeArgUsable(k kind) bool {
	switch k.tag {
	case tagEmptyList, tagEmptyMap, tagBareNone, tagEmptySet, tagEmptyVector:
		return false
	}
	// kindInvalid: lookup — a refused operand; the caller reports it by name.
	return k != kindInvalid
}
