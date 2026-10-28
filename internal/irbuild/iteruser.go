package irbuild

import "github.com/nomi-language/nomi/internal/ir"

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
// The order is not load-bearing: the lookup asks only a named kind
// (`tagNamed`), so std's `impl Iter for Set<T>` is never a candidate. The
// block is this file's (`g.implsByIface`), or another file's: a generic
// instance's where any file registered it, a monomorphic type's through the
// program's impl index. A sibling's items are read in the calling file's
// kinds (importKind), and the view references the symbols its owner interns.

// userIterImpl is the `impl Iter` a named source's type has: this file's
// own, or the one another file of the program declares. owner is the gen
// that lowers the block, and d's item kinds are owner's, so a caller in
// another file reads them through importKind.
type userIterImpl struct {
	owner *gen
	d     *implDef
}

// userIterFn is one function of a user source's `impl Iter`: the symbol its
// owner interns for it, and its kind in the calling file's kinds.
type userIterFn struct {
	it  *implItem
	sym *ir.Symbol
	k   kind
	// params and result are the item's, in the calling file's kinds.
	params []kind
	result kind
}

// userIterImplOf finds the `impl Iter` for receiver kind recv: this file's,
// else a sibling file's (`impl Iter<Int> for Chain` in chains.nomi, iterated
// in main.nomi). A generic instance's block is found where any file
// registered it (instanceImplAnywhere); a monomorphic type's through the
// program's impl index. Declines when there is none, or more than one.
func (g *gen) userIterImplOf(recv kind) (userIterImpl, bool) {
	if recv.tag != tagNamed || recv.def == nil {
		// Only a NAMED type can carry a user impl. Every structural kind is
		// either a built-in source with its own arm or nothing this protocol
		// reaches, and a `tagNamed` gate above every lookup is what keeps a
		// Set or a Range from being captured here.
		return userIterImpl{}, false
	}
	if d := g.implsByIface["Iter"][recv]; d != nil {
		return userIterImpl{owner: g, d: d}, true
	}
	if g.files == nil || g.reg == nil || g.fileUnit < 0 {
		return userIterImpl{}, false
	}
	if recv.def.instOrigin != nil || recv.def.genericOf != nil {
		src := recv.def.instOrigin
		if src == nil {
			src = recv.def
		}
		owner, d := g.instanceImplAnywhere(src, "Iter", "each_while")
		if d == nil || owner == nil || owner == g {
			return userIterImpl{}, false
		}
		return userIterImpl{owner: owner, d: d}, true
	}
	if recv.def.decl == nil {
		return userIterImpl{}, false
	}
	var chosen *implMemberSite
	for _, s := range writtenImplMembers(g.files.implMembers[implMemberKey{recv: recv.def.decl, method: "each_while"}]) {
		if s.iface != "Iter" || s.unit == g.fileUnit {
			continue
		}
		if chosen != nil && (chosen.unit != s.unit || chosen.block != s.block) {
			// Two files implement Iter for one type: a coherence error the
			// analyzer rejects. Declining never picks between them.
			return userIterImpl{}, false
		}
		s := s
		chosen = &s
	}
	if chosen == nil || chosen.unit >= len(g.reg.gens) {
		return userIterImpl{}, false
	}
	owner := g.reg.gens[chosen.unit]
	if owner == nil {
		return userIterImpl{}, false
	}
	for _, d := range owner.implOrder {
		if d.decl == chosen.block && d.ifaceName == "Iter" {
			return userIterImpl{owner: owner, d: d}, true
		}
	}
	return userIterImpl{}, false
}

// fn is u's function name as the calling file g sees it, or false when the
// block has no source body for it the builder lowers, or its kinds do not
// cross into g.
func (u userIterImpl) fn(g *gen, name string) (userIterFn, bool) {
	if u.d == nil || !u.d.lowerable {
		// A refused impl block declines rather than reporting: its own
		// declaration was refused at its own position, and iterSource's
		// refusal at the SOURCE position is the one a reader can act on.
		return userIterFn{}, false
	}
	it := u.d.items[name]
	if it == nil || !it.lowerable || irImplSource(it) == nil {
		return userIterFn{}, false
	}
	params := it.params
	result := it.result
	if u.owner != g {
		params = make([]kind, len(it.params))
		for i, p := range it.params {
			ip, ok := g.importKind(p)
			if !ok {
				return userIterFn{}, false
			}
			params[i] = ip
		}
		r, ok := g.importKind(it.result)
		if !ok {
			return userIterFn{}, false
		}
		result = r
	}
	return userIterFn{it: it, sym: u.owner.irCalleeSym(it, u.d.recv.nomi()+"."+it.name),
		k: funcKindIn(g, params, result), params: params, result: result}, true
}

// iterUserEachWhile finds the `each_while` lowered for receiver kind recv,
// in this file or a sibling, and the element type it pushes.
//
// Split from the lowering so that RECOGNISING a user source and LOWERING one
// are two readable steps: every check below is a reason `rt.UserSeq` would not
// be the right lowering, and the caller adds the one reason that is about the
// call rather than about the shape.
func (g *gen) iterUserEachWhile(recv kind) (userIterImpl, userIterFn, kind, bool) {
	u, ok := g.userIterImplOf(recv)
	if !ok {
		return userIterImpl{}, userIterFn{}, kindInvalid, false
	}
	f, ok := u.fn(g, "each_while")
	if !ok || len(f.params) != 2 {
		return userIterImpl{}, userIterFn{}, kindInvalid, false
	}
	if f.params[0] != recv || f.result != kindBool {
		return userIterImpl{}, userIterFn{}, kindInvalid, false
	}
	y := f.params[1]
	if y.tag != tagFunc {
		return userIterImpl{}, userIterFn{}, kindInvalid, false
	}
	yp := funcParams(y)
	if len(yp) != 1 || funcResult(y) != kindBool {
		return userIterImpl{}, userIterFn{}, kindInvalid, false
	}
	return u, f, yp[0], true
}
