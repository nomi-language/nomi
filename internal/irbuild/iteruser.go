package irbuild

// A USER type as an `Iter` source: `impl Iter for Tree`.
//
// # This is the protocol's documented extension point, and it was the one
// source the builder could not drive
//
// Every other source is built in — a List, a String, a Map, a Set, a Vector, a
// Range — and each has a hand-written `rt` walk. `impl Iter for MyType` is the
// case where the walk is the PROGRAMMER'S, and iterSource refused it by name
// (`Iter over an unlowered source`, detail `Tree`) with the recorded reason
// that it "needs the protocol method called through dispatch".
//
// That reason turned out to be one word too strong, and correcting it is what
// makes this small. Dispatch is what you need when the receiver is ERASED. Here
// it is not: `Iter.to_list(tree)` has a receiver whose static kind is `Tree`,
// and `g.implsByIface["Iter"][Tree]` names exactly one impl block. So the
// `each_while` to call is chosen at COMPILE time and handed to rt.UserSeq as a
// Go func value. No table, no TypeID, no box — the same single closure a List
// pays, and nothing per element.
//
// # What is checked, and why each check is not paranoia
//
// The block is confirmed to supply the protocol SHAPE rather than merely a
// member of the right name, because `rt.UserSeq`'s signature is the shape:
//
//	each_while(collection: self, yield: (T) -> Bool): Bool
//
// The receiver parameter must be the source's own kind, the second parameter a
// one-argument function answering Bool, and the result Bool. If any of those is
// otherwise, the impl is not this protocol and lowering it as one would emit Go
// that does not compile — which the sweep reads as a codegen bug rather than as
// a refusal. So a mismatch DECLINES, and iterSource then reports its own
// long-standing refusal at the source position, keeping one gap under one key.
//
// The element type is read from the `yield` parameter rather than from the
// interface declaration's `T`, and that is the load-bearing choice: `Iter<T>`'s
// type parameter is not solved at this call site, while the impl block has
// already committed to a concrete element — `yield: (Int) -> Bool` for a Tree
// of Ints. Reading the impl is reading the answer; reading the interface would
// be reading the question.
//
// # Where it sits
//
// Asked from iterSourceExt, after the built-in sources and before iterSource's
// refusal.
//
// The order is not load-bearing: moving the arm above the vector/set/range
// asks changes nothing, because `g.implsByIface` holds the impl blocks of the MODULE BEING
// LOWERED, so std's `impl Iter for Set<T>` is never a candidate here and a Set
// declines before its element type is read. iterext.go's call site carries the
// full reasoning.

// iterUserEachWhile finds the `each_while` this module lowers for receiver kind
// recv, and the element type it pushes.
//
// Split from the lowering so that RECOGNISING a user source and LOWERING one
// are two readable steps: every check below is a reason `rt.UserSeq` would not
// be the right lowering, and the caller adds the one reason that is about the
// call rather than about the shape.
func (g *gen) iterUserEachWhile(recv kind) (*implItem, kind, bool) {
	if recv.tag != tagNamed {
		// Only a NAMED type can carry a user impl. Every structural kind is
		// either a built-in source with its own arm or nothing this protocol
		// reaches, and a `tagNamed` gate above every lookup is what keeps a
		// Set or a Range from being captured here.
		return nil, kindInvalid, false
	}
	byRecv, known := g.implsByIface["Iter"]
	if !known {
		return nil, kindInvalid, false
	}
	d := byRecv[recv]
	if d == nil || !d.lowerable {
		// A refused impl block declines rather than reporting: its own
		// declaration was refused at its own position, and iterSource's
		// refusal at the SOURCE position is the one a reader can act on.
		return nil, kindInvalid, false
	}
	it := d.items["each_while"]
	if it == nil || !it.lowerable || len(it.params) != 2 {
		return nil, kindInvalid, false
	}
	if it.params[0] != recv || it.result != kindBool {
		return nil, kindInvalid, false
	}
	y := it.params[1]
	if y.tag != tagFunc {
		return nil, kindInvalid, false
	}
	yp := funcParams(y)
	if len(yp) != 1 || funcResult(y) != kindBool {
		return nil, kindInvalid, false
	}
	return it, yp[0], true
}
