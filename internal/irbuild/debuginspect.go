package irbuild

import "github.com/nomi-language/nomi/internal/analysis"

// The two questions `Debug.inspect` at a statically known receiver asks.
//
// `Debug.inspect` is a real interface method: it dispatches and honours a
// hand-written `impl Debug`. It is not the rendering of an assertion's
// `values:` row, which is `rt.RowText` (rt/render.go), a structural rendering
// that honours only a hand-written `impl Debug`. The two disagree for shapes
// the corpus contains: a struct's row sorts its fields by name and its Debug
// keeps declaration order. So a Debug rendering is not built out of the
// row's renderer.
//
// Each predicate here answers for the function `Debug.inspect(x)` lowers to:
// std's own `Debug.inspect` at a scalar receiver kind (stdRenderResolves,
// which answers for Display's to_string too), and the impl this module
// registered at a named one (debugNamedInspects).

// debugNamedInspects reports whether this module registered an `impl Debug`
// for the named receiver kind k whose `inspect` lowers at k.
//
// `implsByIface[stdIfaceImplKey(stdDebug)][k]` is the same table `Debug.inspect(x)` consults, so
// a receiver this answers for is exactly a receiver that call already lowers
// for.
func (g *gen) debugNamedInspects(k kind) bool {
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	d := g.implsByIface[stdIfaceImplKey(stdDebug)][k]
	if d == nil || !d.lowerable {
		return false
	}
	it := d.items["inspect"]
	return it != nil && it.lowerable && len(it.params) == 1 && it.params[0] == k && it.result == kindString
}

// stdRenderResolves reports whether std's own rendering function method
// of iface (`Debug.inspect`, `Display.to_string`) resolves at receiver kind
// k, through the index row an interface qualifier with a known receiver
// kind reads.
func (g *gen) stdRenderResolves(iface analysis.StdType, method string, k kind) bool {
	if g.std == nil {
		return false
	}
	f := stdPick(g.std.stdIfaceImpls(iface.Func(method), k), []kind{k})
	return f != nil && f.why == "" && len(f.params) == 1 && f.params[0] == k && f.result == kindString
}
