package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// The `impl List<T>` method surface, reached type-qualified: `List.equal?(xs,
// ys)`, `List.head(xs)`, `List.tail(xs)`, `List.concat(a, b)`.
//
// # Why this is an owner-keyed arm and not the stdlib path
//
// These are stdlib functions with ordinary declarations in std/lists.nomi, and
// stdlib.go's index sees them: collectStdCandidates reads the receiver with
// analysis.TypeExprBaseName, so the index holds `lists.List.head` and
// `byType["List.head"]` resolves.
//
// What the index resolves TO for these declarations is a refusal: every method
// of `impl List<T>` is generic over T, so its entry carries
// `why == "stdlib generic function"` and its parameter kinds are kindInvalid.
// The index answers with a scalar-subset signature or nothing, and
// `*rt.List[T]` is not in the scalar subset. So stdlib.go can NAME these
// functions while this arm is what LOWERS them, by reading the receiver's kind
// off the lowered argument, which is information the index does not carry and
// by design will not.
//
// The ordering that makes both true is implCall's: listCall, mapCall and
// iterCall are consulted BEFORE stdlibCall, so a `List.` call reaches the rt
// implementation and never the index's generic refusal. Deleting this arm
// would replace a lowering with a refusal.
//
// # The receiver kind is visible here, which narrows the trap
//
// Because this arm dispatches on the lowered receiver, a `List.` call over
// something that is not a list refuses as `List function over a non-List` naming
// the kind, rather than vanishing into `qualified call`. An UNROUTED generic
// owner — `Set`, `Vector`, `Range` — refuses under the index's own `stdlib
// generic function` / `stdlib function outside the scalar subset` naming its
// key, unless its own owner-keyed arm (sets.go, vectors.go, ranges.go) claims
// the call first.

// listFn is one `List.` function this builder lowers: the rt symbol, the arity,
// and how the result kind is derived.
//
// Deliberately not every function std/lists.nomi declares. `List.next_item` is
// not reached anywhere in the corpus, so it is absent rather than written and
// unexercised, and `List.hash` is absent for the same reason even though
// `rt.HashList` already exists for the map-key path. `List.compare` is handled
// beside `equal?` below; its `Ordering` result is an anchored, rt-declared,
// process-wide type (stdenum.go).
type listFn struct {
	rtCall string
	args   int
	// result names how the result kind is derived from the element kind. See
	// listResultKind.
	result string
}

// listFuncs is the lowered surface of `impl List<T>`, minus equality.
//
// `equal?` is absent on purpose and handled separately: its lowering is
// collections.go's listEqual, shared with the `==` operator, because
// std/lists.nomi:125 defines the method AS `a == b`. Giving it a row here would
// make it a second encoding of one semantics.
//
// Use across tests/: equal? 39 sites, head 4, tail 2,
// concat 1.
var listFuncs = map[string]listFn{
	"head":   {rtCall: "rt.ListHead", args: 1, result: "maybe-elem"},
	"tail":   {rtCall: "rt.ListTail", args: 1, result: "maybe-list"},
	"concat": {rtCall: "rt.ListConcat", args: 2, result: "list"},
}

// listOrders reports whether a List of kind k has Nomi's list order, shared by
// `List.compare` and by `<` / `>` / `<=` / `>=` on two Lists, refusing by name
// when its element has none.
//
// Shared rather than agreed-upon, exactly as listEqual is and for the same
// reason: `[1, 2] < [1, 3]` and `List.compare([1, 2], [1, 3])` are the SAME
// operation — the operators desugar to `Comparable.compare` followed by an
// Ordering match (std/comparable.nomi), and std's `impl Comparable for List<T>`
// is what both reach.
func (g *gen) listOrders(k kind, at ast.Node) bool {
	if !g.elemOrders(k.comp.parts[0], at) {
		g.reject("ordering on a List of an undispatchable element", k.nomi(), at)
		return false
	}
	return true
}

// elemOrders reports whether a List's element of kind k has an order this
// builder resolves: Int and Float.
//
// # Int and Float
//
// `impl Comparable for List<T>`'s body calls `Comparable.compare(ha, hb)` at
// the TYPE PARAMETER T, so the checker records no concrete conformance and the
// manifest bridge pre-loads no impl. What that dispatch then does is whatever
// happens to be loaded for other reasons. In a program importing only std/io
// and std/lists:
//
//	List.compare([1], [2])        Less
//	List.compare([1.0], [2.0])    Less
//	List.compare(["a"], ["b"])    FAULT: Comparable.compare: no implementation for type 'String'
//	List.compare([True], [False]) FAULT: ... for type 'bool.Bool'
//	List.compare([[1]], [[2]])    FAULT: ... for type 'List'
//
// Int and Float are the two that answer unconditionally, so they are the two
// this serves. Everything else refuses BY NAME.
//
// The element still goes through `Comparable.compare` and not through `<`:
// std/float's `compare` is a TOTAL order with NaN at the top, which is what
// makes `Iter.sort` terminate and is deliberately NOT what `<` on two Floats
// does.
func (g *gen) elemOrders(k kind, at ast.Node) bool {
	return (k == kindInt || k == kindFloat) && g.compareResolves(at, k, stdEnumKind(stdEnumOrdering))
}

// listResultKind turns a listFn's result tag into a kind.
func (g *gen) listResultKind(tag string, elem, recv kind, at ast.Node) (kind, bool) {
	switch tag {
	case "list":
		return recv, true
	case "maybe-elem":
		return g.maybeOf(elem, at)
	case "maybe-list":
		return g.maybeOf(recv, at)
	}
	return kindInvalid, false
}

// maybeOf is `Maybe<inner>`, asked of prelude.go rather than assembled here so
// there is one shape of Maybe in the builder and not two. mapResultKind's rule.
func (g *gen) maybeOf(inner kind, at ast.Node) (kind, bool) {
	a, anchored := g.preludeByName["Maybe"]
	if !anchored {
		g.reject("List function without the prelude Maybe", inner.nomi(), at)
		return kindInvalid, false
	}
	k := g.preludeInstance(a, []kind{inner})
	// kindInvalid: reports — rejects an element type Maybe cannot be instantiated at.
	if k == kindInvalid {
		g.reject("List function over an unrepresentable element type", inner.nomi(), at)
		return kindInvalid, false
	}
	return k, true
}
