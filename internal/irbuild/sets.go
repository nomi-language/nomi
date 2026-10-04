package irbuild

// `Set<T>` as `rt.Set[T]` — the `#{…}` literal, the `impl Set<T>` method
// surface, and the `Iter` source.
//
// # Why this is an owner-keyed arm and not the stdlib path
//
// lists.go's header states the rule and this file is one of its cases: an
// unrouted generic owner refuses under the index's own `stdlib generic
// function` / `stdlib function outside the scalar subset` key.
//
// stdgenstruct.go supplies the kind. What it cannot supply is a
// LOWERING, because every function of `impl Set<T>` is generic over T:
// `pub fn size<T>(s: Set<T>): Int` mentions T inside `Set<T>` rather than bare,
// which is exactly the shape generic.go refuses ("the box would be at the wrong
// DEPTH"). So the index answers with a refusal and this arm answers with an
// implementation, reading the element type off the LOWERED RECEIVER — information
// the index does not carry and by design will not. That is mapCall's and
// listCall's arrangement, reached by the same route.
//
// # Every arm here is std's own body with the pipeline removed
//
// rt/set.go's header carries the argument in full and the consequence for this
// file is a rule: an arm may only lower to an rt call whose observable behaviour —
// ORDER and DEDUPLICATION included — is what std/sets.nomi's Nomi body produces.
// The differential fixture (testdata/sets.nomi) is what holds the two equal, and
// it pins the order absolutely because both sides derive it independently.
//
// # `Set.size` is O(1) and that is a requirement
//
// std documents it as such and contrasts it with the generic `Iter.count`:
// "The number of elements. O(1) — the set's own count; the generic count is
// `Iter.count`." It reaches `rt.MapSize`, a field read, through `rt.SetSize`.
// `Iter.count` over a Set is O(1) too, but for a different reason and through a
// different route — `known_count`, which `impl Iter for Set<T>` overrides — so
// the two are separate lowerings and the fixture reads both.
//
// # What is NOT here
//
// `Set.next_item` has no row: std declares no such function for a Set. `Vector`
// is a different type with a different answer — `pub host type Vector<T>` with
// nine `host fn`s and no Nomi body anywhere — so nothing in this file generalizes
// to it; see vectors.go.

// setFn is one `Set.` function this builder lowers.
//
// The argument SHAPE is `sets` set-typed leading arguments followed by
// `args - sets` element-typed ones, which covers every row std declares and is
// what lets one lowering path serve all eight. `Set.remove` has a row even
// though no corpus file calls it, because it is the exact peer of `Set.insert`
// and the two are one line of rt each — the ordering rule they share is the thing
// worth having tested, and the fixture reads both.
type setFn struct {
	rtCall string
	// args is the Nomi arity, and sets is how many of them are Sets.
	args int
	sets int
	// result is how the result kind is derived: "set", "int" or "bool".
	result string
	// ops records whether the rt call takes the element's `hash, eq` pair.
	// `Set.size` is the one row that does not: it reads a stored count.
	ops bool
}

// setFuncs is the lowered surface of `impl Set<T>`, which is all nine of its
// functions — eight here and `new` beside them, whose result type comes from
// context rather than from an argument. See setNewCall.
//
// Use across tests/: size 12 sites, contains? 11, insert 1, union 1,
// intersection 1, difference 1, subset? 1, remove 0, new 0.
var setFuncs = map[string]setFn{
	"size":         {rtCall: "rt.SetSize", args: 1, sets: 1, result: "int"},
	"contains?":    {rtCall: "rt.SetContains", args: 2, sets: 1, result: "bool", ops: true},
	"insert":       {rtCall: "rt.SetInsert", args: 2, sets: 1, result: "set", ops: true},
	"remove":       {rtCall: "rt.SetRemove", args: 2, sets: 1, result: "set", ops: true},
	"union":        {rtCall: "rt.SetUnion", args: 2, sets: 2, result: "set", ops: true},
	"intersection": {rtCall: "rt.SetIntersection", args: 2, sets: 2, result: "set", ops: true},
	"difference":   {rtCall: "rt.SetDifference", args: 2, sets: 2, result: "set", ops: true},
	"subset?":      {rtCall: "rt.SetSubset", args: 2, sets: 2, result: "bool", ops: true},
}

// setKindOf is the Set kind for element kind elem, or kindInvalid.
//
// One constructor, so the literal, the annotation, `Iter.to_set` and every
// method result reach the SAME interned def for one element type — which is the
// identity rule stdgenstruct.go exists to state, applied here rather than
// restated.
func (g *gen) setKindOf(elem kind) kind {
	if setSpec == nil {
		// Unreachable while the spec table has its `Set` row, and honoured
		// rather than asserted: a missing row must refuse everything, not panic
		// the compiler.
		return kindInvalid
	}
	k, ok := g.genStructInstance(setSpec, elem)
	if !ok {
		return kindInvalid
	}
	return k
}

// setElem reads a Set kind's element kind, and reports whether k is a Set.
//
// By SPEC POINTER through genStructOf, never by rendered name: a user type
// spelled `Set` has its own def and answers false, which is the half a name check
// gets wrong.
func setElem(k kind) (kind, bool) {
	spec, args, ok := genStructOf(k)
	if !ok || spec != setSpec || len(args) != 1 {
		return kindInvalid, false
	}
	return args[0], true
}

// --- the literal -------------------------------------------------------------

// --- the call arm ------------------------------------------------------------

// --- Iter, equality, hashing, rendering --------------------------------------
