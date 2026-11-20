package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A USER type as an `Iter` source: `impl Iter for Tree`.
//
// # The protocol's extension point
//
// Every other source is built in (a List, a String, a Map, a Set, a Vector, a
// Range) and each has a hand-written `rt` walk. `impl Iter for MyType` is the
// case where the walk is the programmer's.
//
// The receiver is not erased: `Iter.to_list(tree)` has a receiver whose static
// kind is `Tree`, and `g.implsByIface[stdIfaceImplKey(stdIter)][Tree]` names exactly one impl
// block. So the `each_while` to call is chosen at build time, and the view
// (an `ir.Iter` over `ir.IterOverUserImpl`, userView in iriterbody.go) holds
// an `ir.RefFunc` to it as an operand. The VM walks the view through
// rt.UserSeq, calling that function with the source and a yield callback.
//
// # What is checked
//
// iterUserEachWhile confirms the block supplies the protocol shape, which is
// what the VM's call assumes:
//
//	each_while(collection: self, yield: (T) -> Bool): Bool
//
// The receiver parameter must be the source's own kind, the second parameter a
// one-argument function answering Bool, and the result Bool. The view's
// element kind is the yield parameter's one parameter, and the VM passes
// `each_while` exactly a source and a yield callback, so an impl of another
// shape would give the sequence a wrong element kind or be called with the
// wrong arguments. A mismatch declines, and the source is refused at its own
// position, keeping one gap under one key. The checker holds an impl's
// `each_while` to the interface's signature, so on a checked program the
// shape tests pass; the reachable decline is u.fn's, an impl that did not
// lower or whose kinds do not cross into the calling file.
//
// The element type is read from the `yield` parameter rather than from the
// interface declaration's `T`: `Iter<T>`'s type parameter is not solved at
// this call site, while the impl block has already committed to a concrete
// element, `yield: (Int) -> Bool` for a Tree of Ints.
//
// # Where it sits
//
// The checker records which impl block a written source's view walks
// (analysis.IterView.Impl), and userIterImplByBlock finds where that block
// is lowered: this file's (`g.implsByIface`), or another file's, a generic
// instance's where any file registered it. A source with no expression the
// checker typed (an instance of a type parameter bounded by Iter, a
// `flat_map` callback's result) finds its impl by its kind
// (userIterImplOf), which asks only a named kind (`tagNamed`), so std's
// `impl Iter for Set<T>` is never a candidate. A sibling's items are read in
// the calling file's kinds (importKind), and the view references the
// symbols its owner interns.

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
	if d := g.implsByIface[stdIfaceImplKey(stdIter)][recv]; d != nil {
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
		owner, d := g.instanceImplAnywhere(src, stdIfaceImplKey(stdIter), "each_while")
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
		if s.iface != stdIfaceImplKey(stdIter) || s.unit == g.fileUnit {
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
		if d.decl == chosen.block && d.ifaceKey == stdIfaceImplKey(stdIter) {
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
		// declaration was refused at its own position, and the caller's
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
	return userIterFn{sym: u.owner.irImplSym(it),
		k: funcKindIn(g, params, result), params: params, result: result}, true
}

// iterUserEachWhile is the `each_while` u, the `impl Iter` of receiver
// kind recv, lowers, and the element type it pushes.
//
// Split from the lowering so that recognising a user source and lowering one
// are two steps. Its one caller is userSource (iriterbody.go), which makes
// the element kind answered here the view's and adds the reasons that are
// about the element and the overrides.
//
// The arity tests also guard this function's own indexing: without
// `len(f.params) != 2` the reads of f.params[0] and f.params[1] would panic
// on a shorter signature, and without the `tagFunc` and `len(yp) != 1` tests
// funcParams(y) and yp[0] would panic on a yield that is not a one-parameter
// function. The file comment says which declines a checked program reaches.
func (g *gen) iterUserEachWhile(u userIterImpl, recv kind) (userIterFn, kind, bool) {
	f, ok := u.fn(g, "each_while")
	if !ok || len(f.params) != 2 {
		return userIterFn{}, kindInvalid, false
	}
	if f.params[0] != recv || f.result != kindBool {
		return userIterFn{}, kindInvalid, false
	}
	y := f.params[1]
	if y.tag != tagFunc {
		return userIterFn{}, kindInvalid, false
	}
	yp := funcParams(y)
	if len(yp) != 1 || funcResult(y) != kindBool {
		return userIterFn{}, kindInvalid, false
	}
	return f, yp[0], true
}

// userIterImplByBlock is block, the `impl Iter` the checker recorded for a
// source of kind recv (analysis.IterView.Impl), as the gen that lowers it
// holds it: this file's, the instance of a generic block where any file
// registered it (instanceImplAnywhere), or a sibling file's.
func (g *gen) userIterImplByBlock(recv kind, block *ast.ImplBlock) (userIterImpl, bool) {
	if recv.tag != tagNamed || recv.def == nil || block == nil {
		return userIterImpl{}, false
	}
	if d := g.implsByIface[stdIfaceImplKey(stdIter)][recv]; d != nil && d.decl == block {
		return userIterImpl{owner: g, d: d}, true
	}
	if g.reg == nil {
		return userIterImpl{}, false
	}
	if recv.def.instOrigin != nil || recv.def.genericOf != nil {
		src := recv.def.instOrigin
		if src == nil {
			src = recv.def
		}
		owner, d := g.instanceImplAnywhere(src, stdIfaceImplKey(stdIter), "each_while")
		if d == nil || owner == nil || owner == g || d.decl != block {
			return userIterImpl{}, false
		}
		return userIterImpl{owner: owner, d: d}, true
	}
	for _, owner := range g.reg.gens {
		if owner == nil || owner == g {
			continue
		}
		for _, d := range owner.implOrder {
			if d.decl == block && d.ifaceKey == stdIfaceImplKey(stdIter) {
				return userIterImpl{owner: owner, d: d}, true
			}
		}
	}
	return userIterImpl{}, false
}
