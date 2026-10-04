package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// `Vector<T>` as `rt.Vector[T]` — the `#[…]` literal, the `impl Vector<T>` method
// surface, the five interface impls, and the `Iter` source.
//
// # The kind, and the comparator
//
// The kind is one row in `stdGenHostSpecs`. `stdgenhost.go`, the GENERIC STD
// HOST TYPE family, is anchored on `*ast.ExternType` WITH type parameters,
// which is exactly what `pub host type Vector<T>` is, so the process-wide
// instance interning, the annotation door (`stdGenHostTypeOf`), the inferred
// door (`genHostOfType`) and the refusal reasoning are shared. A Vector is a
// `tagNamed` kind over a shared def, exactly as `Set<T>` is.
//
// std's `impl Comparable for Vector<T>` recurses through
// `Comparable.compare(ha, hb)` at a bare `T`, and this builder never lowers
// that body: it calls `rt.VectorCompare(a, b, cmp)` with `cmp` supplied as an
// argument, which is `sets.go`'s arrangement for `hash, eq` and `ranges.go`'s
// for its comparator. So no dictionary is needed to LOWER it.
//
// `cmp` must be as narrow as the reference answer, not as wide as the builder
// could make it:
//
//	Vector.compare(#["a", "z"], #["b"])   FAULT: Comparable.compare: no implementation for type 'String'
//	Vector.compare(#[#[1]], #[#[2]])      FAULT: Comparable.compare: no implementation for type 'Vector'
//	Vector.compare(#[1], #[2])            Less
//	Vector.compare(#[1.0], #[2.0])        Less
//	Iter.sort(#["c", "a", "b"])           [a, b, c]
//
// Int and Float answer and nothing else does, because `Vector.compare` raises
// the `Comparable.compare` demand INSIDE std's own generic body where no
// concrete conformance is recorded, while `Iter.sort` raises it at a CONCRETE
// call site, which is what records one. The last two lines are the
// discriminator: the same element type, two spellings, two answers.
//
// So `vectorCmp` routes through `orderingOf` and NOT `sortComparator`, and the
// two are deliberately different widths for a reason the type system cannot
// express. The wide resolution would print `Less` for
// `Vector.compare(#["a"], #["b"])` where the recorded answer FAULTS, which an
// output comparison only sees if the fixture contains the row.
//
// # Why this is an owner-keyed arm and not the stdlib path
//
// `sets.go`'s header states the rule and its "what is NOT here" section names
// this file as the case it does not generalize to. Every function of `impl
// Vector<T>` and every member of its five interface impls MENTIONS `T`, so
// `sigNamesTypeParam` (stdrecvgeneric.go) keys all of them `stdlib generic
// function`. The index can NAME `vectors.Vector.push` and cannot supply
// `T`; the CALL SITE can, off the lowered receiver. So the index answers with a
// refusal and this file answers with an implementation, which is listCall's,
// mapCall's and setCall's arrangement reached by the same route.
//
// # `Vector.length` is O(1) and `Vector.at` is indexed, and that is the type's
// whole reason for existing
//
// std's module comment: "use `List<T>` for cons-style recursion and cheap
// prepends; use `Vector<T>` when indexed lookup and push-at-end are the natural
// operations." Those two are precisely the operations a generic `Iter` adapter
// cannot express, because `Iter` has no index. rt/vector.go's header carries the
// representation argument; what this file must not do is route either through a
// walk. `length` reaches `rt.VectorLength` (a field read) and `at` reaches
// `rt.VectorAt` (one bounds test and one index).
//
// # Every lowering here is std's own body with the pipeline removed
//
// sets.go's rule, and for a host type it is sharper rather than weaker: there is
// no Nomi body to compare against for the nine `host fn`s, so the specification
// is rt/vector.go's window layout and the golden records, and the five
// interface impls DO have Nomi bodies that must be
// matched exactly. The two that can go silently wrong are pinned absolutely in
// testdata/vectors.nomi:
//
//   - `impl Hashable for Vector<T>` is `Int.wrapping_add(Int.wrapping_mul(acc,
//     31), …)` from a seed of 19. Both operations WRAP; a checked `*` would trap
//     where the right answer is a number.
//   - `impl Comparable for Vector<T>` is lexicographic with a proper prefix Less,
//     and length must NOT be consulted first — `#[2]` is Greater than `#[1, 9]`.
//
// # What is NOT here
//
// `Vector.new` has no row: std declares `Vector.empty()` and no `new`, which is
// where this differs from `Set`. `Vector.next_item` DOES have one, because std
// declares it as the driver for the `Iter` impl and `vectors_test.nomi`'s doctest
// reads it — where `Set.next_item` does not exist at all.

// vectorFn is one `Vector.` function this builder lowers.
//
// The argument SHAPE is `vecs` vector-typed leading arguments followed by the
// rest, whose wanted kinds are named per row rather than assumed to be the
// element type. `Set`'s table could say "the rest are elements" because every one
// of its rows is; `Vector.at(v, index)` and `Vector.set(v, index, item)` take an
// `Int` in a non-leading position, so the shape has to be spelled out.
type vectorFn struct {
	rtCall string
	// rest are the wanted kinds of the arguments AFTER the vector-typed ones, in
	// order. "elem" is the element type and "int" is `Int`.
	rest []string
	// vecs is how many leading arguments are Vectors.
	vecs int
	// result is how the result kind is derived. "vector" is `Vector<T>`, "int"
	// `Int`, "bool" `Bool`, "string" `String`, "ordering" the
	// shared `Ordering`, "maybeElem" `Maybe<T>`, "maybeVector"
	// `Maybe<Vector<T>>`, "nextItem" `Maybe<(T, Vector<T>)>`.
	result string
	// ops records which element operation the rt call needs appended: "" none,
	// "eq", "hash", "cmp", or "render" for a rendering.
	ops string
}

// vectorFuncs is the lowered surface of `std/vectors.nomi`: the six functions of
// `impl Vector<T>` other than `empty`, plus the five interface members the module
// declares for the type.
//
// `empty` is beside them rather than in the table, because its result type comes
// from context and not from an argument — setNewCall's split, for its reason.
//
// The five interface members are here under their METHOD names because that is
// how a program spells them: `Vector.equal?(a, b)`, `Vector.to_string(v)`,
// `Vector.inspect(v)`, `Vector.hash(v)`, `Vector.compare(a, b)` — all five appear
// in `06-collections/vectors_test.nomi`. Reaching them through `implCall`'s
// interface routes instead would need `implsByIface["Equatable"][vectorKind]` to
// hold an entry, and std's impl is generic so nothing registers one; that is the
// same split lists.go describes for `List.equal?`.
var vectorFuncs = map[string]vectorFn{
	"length":    {rtCall: "rt.VectorLength", vecs: 1, result: "int"},
	"at":        {rtCall: "rt.VectorAt", vecs: 1, rest: []string{"int"}, result: "maybeElem"},
	"push":      {rtCall: "rt.VectorPush", vecs: 1, rest: []string{"elem"}, result: "vector"},
	"set":       {rtCall: "rt.VectorSet", vecs: 1, rest: []string{"int", "elem"}, result: "maybeVector"},
	"concat":    {rtCall: "rt.VectorConcat", vecs: 2, result: "vector"},
	"next_item": {rtCall: "rt.VectorNextItem", vecs: 1, result: "nextItem"},

	"equal?":    {rtCall: "rt.VectorEqual", vecs: 2, result: "bool", ops: "eq"},
	"compare":   {rtCall: "rt.VectorCompare", vecs: 2, result: "ordering", ops: "cmp"},
	"hash":      {rtCall: "rt.VectorHash", vecs: 1, result: "int", ops: "hash"},
	"to_string": {rtCall: "rt.FormatVector", vecs: 1, result: "string", ops: "display"},
	"inspect":   {rtCall: "rt.FormatVector", vecs: 1, result: "string", ops: "debug"},
}

// vectorSpec is the `Vector` row of stdGenHostSpecs, resolved by (origin, name)
// so a reorder of that table cannot repoint every caller here silently —
// setSpec's form and its reason.
var vectorSpec = genHostSpecFor("std/vectors", "Vector")

// vectorKindOf is the Vector kind for element kind elem, or kindInvalid.
//
// One constructor, so the literal, the annotation, a method result and an
// inferred position all reach the SAME interned def for one element type — the
// identity rule stdgenhost.go exists to state, applied rather than restated.
func (g *gen) vectorKindOf(elem kind) kind {
	k, ok := g.genHostInstance(vectorSpec, elem)
	if !ok {
		return kindInvalid
	}
	return k
}

// vectorElem reads a Vector kind's element kind, and reports whether k is a
// Vector.
//
// By SPEC POINTER through genHostOf, never by rendered name: a user type spelled
// `Vector` has its own def and answers false, which is the half a name check gets
// wrong.
func vectorElem(k kind) (kind, bool) {
	spec, args, ok := genHostOf(k)
	if !ok || spec != vectorSpec || len(args) != 1 {
		return kindInvalid, false
	}
	return args[0], true
}

// --- the literal -------------------------------------------------------------

// --- element operations ------------------------------------------------------

// --- the call arm ------------------------------------------------------------

// vectorResultKind is the kind a row's result string names.
func (g *gen) vectorResultKind(tag string, vk, elem kind, at ast.Node) (kind, bool) {
	switch tag {
	case "vector":
		return vk, true
	case "int":
		return kindInt, true
	case "bool":
		return kindBool, true
	case "string":
		return kindString, true
	case "ordering":
		return stdEnumKind(stdEnumOrdering), true
	case "maybeElem":
		return g.maybeOf(elem, at)
	case "maybeVector":
		return g.maybeOf(vk, at)
	case "nextItem":
		pair := g.tupleKind([]kind{elem, vk})
		// kindInvalid: reports — an unrepresentable tuple part, named by tupleKind.
		if pair == kindInvalid {
			return kindInvalid, false
		}
		return g.maybeOf(pair, at)
	}
	return kindInvalid, false
}

// --- Iter, equality, rendering ----------------------------------------------
