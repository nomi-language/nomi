package analysis

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// DefaultedCall is what fills the parameters a call leaves out
// (FileAnalysis.CallDefaults, made with the call's placement,
// recordPlacement), or a function value's call fills
// (FileAnalysis.DefaultedRefs): the declaration whose defaults they take,
// and which slots.
type DefaultedCall struct {
	// Decl is the declaration whose parameter list holds the defaults: a
	// `fn` (*ast.FuncDef), a `host fn` (*ast.ExternFunc) or an interface
	// function (*ast.InterfaceMethod). A call of an interface function the
	// checker resolved to an impl (DispatchImpl) takes the impl
	// function's, which an inherited default shares with the interface.
	Decl ast.Node
	// Params is Decl's parameter list; a slot's default is
	// Params[slot].Default, an expression of the declaring file.
	Params []ast.Param
	// File is the path of the file that declares Decl, "" for the stdlib
	// and a file with no path.
	File string
	// Slots are the parameters filled from their defaults, ascending.
	Slots []int
	// From, parallel to Slots, is the declaration whose default fills
	// each slot. It is Decl, except in the record of a call that reaches
	// an impl function the checker resolved (DispatchImpl, Decl that
	// function) which declares no default at a slot its interface
	// function defaults: that slot's is the interface function's
	// (*ast.InterfaceMethod). The default of the function that runs is
	// used, per parameter, and the interface's where it declares none
	// (spec §12 *Item kinds*). SlotParams reads it.
	From []ast.Node
	// Ref, for a call through a local name that copies a declared
	// function (`g = parse` then `g("1")`, calleeCopiesADeclaration), is
	// the expression the copy was made from (`parse`): the call goes
	// through the function that name is, which fills the slots. Nil for a
	// call of the declaration by name.
	Ref ast.Node
}

// recordDefaultedRef notes that node, a declared function named as a value
// where a function of its first n parameters is expected
// (defaultFilledFuncValue), is that shorter function, whose call fills the
// rest from their defaults.
func (c *checker) recordDefaultedRef(node ast.Node, n int) {
	if na, ok := node.(*ast.NamedArg); ok {
		node = na.Value
	}
	node = ungroupExpr(node)
	sym, ok := c.callFuncSym(node)
	if !ok || sym == nil {
		return
	}
	var decl []ast.Param
	switch d := sym.Node.(type) {
	case *ast.FuncDef:
		decl = d.Params
	case *ast.ExternFunc:
		decl = d.Params
	case *ast.InterfaceMethod:
		decl = d.Params
	default:
		return
	}
	var slots []int
	for i := n; i < len(decl); i++ {
		slots = append(slots, i)
	}
	if len(slots) == 0 {
		return
	}
	if c.fa.DefaultedRefs == nil {
		c.fa.DefaultedRefs = map[ast.Node]*DefaultedCall{}
	}
	c.fa.DefaultedRefs[node] = &DefaultedCall{Decl: sym.Node, Params: decl, File: sym.SourceFile, Slots: slots, From: sameFrom(sym.Node, len(slots))}
}

// settleCallDefaults points each recorded call of an interface function
// that the checker resolved to an impl (DispatchImpl) at the impl
// function, once every body is checked and the dispatch records are
// final: the call fills each omitted parameter from the function it
// reaches where that function declares a default, and from the
// interface's where it declares none (From). A call none of whose omitted
// parameters the impl function defaults keeps the interface's record.
func (c *checker) settleCallDefaults() {
	for call, rec := range c.fa.CallDefaults {
		if _, iface := rec.Decl.(*ast.InterfaceMethod); !iface || rec.Ref != nil {
			continue
		}
		pos, named := calleeKey(call.Func)
		if !named {
			continue
		}
		ref := c.fa.References[pos]
		if ref == nil {
			continue
		}
		fd, ok := ref.DispatchImpl.(*ast.FuncDef)
		if !ok || len(fd.Params) != len(rec.Params) {
			continue
		}
		from := make([]ast.Node, len(rec.Slots))
		own := false
		for i, s := range rec.Slots {
			from[i] = rec.Decl
			if fd.Params[s].Default != nil {
				from[i], own = fd, true
			}
		}
		if !own {
			continue
		}
		rec.Decl, rec.Params, rec.From = fd, fd.Params, from
		if p := c.fa.Program(); p != nil {
			if declaring := p.ScopedFunctionFiles[fd]; declaring != nil {
				rec.File = declaring.FilePath
			}
		}
	}
}

// sameFrom is the From of a record whose n slots all take decl's defaults.
func sameFrom(decl ast.Node, n int) []ast.Node {
	from := make([]ast.Node, n)
	for i := range from {
		from[i] = decl
	}
	return from
}

// SlotSource is the declaration whose default fills slot, one of Slots
// (From), and the parameter list holding that default: Params, or the
// interface function's own where From names it and Decl is the impl's.
func (r *DefaultedCall) SlotSource(slot int) (ast.Node, []ast.Param) {
	i := slices.Index(r.Slots, slot)
	if i < 0 || i >= len(r.From) {
		panic("analysis: SlotSource of a slot its record does not fill")
	}
	if im, iface := r.From[i].(*ast.InterfaceMethod); iface && r.From[i] != r.Decl {
		return im, im.Params
	}
	return r.From[i], r.Params
}
