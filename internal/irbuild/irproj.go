package irbuild

// Projections a destructuring prologue reads a pattern's names off: a struct
// field, a record field, a tuple slot, a variant's payload, and the value a
// distinct wraps. Each is one `ir.Proj`, appended to the prologue and typed
// with the kind the declaration this package already resolved gives it.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irProjExpr records one `ir.Proj` into the open prologue and answers its
// value.
func (g *gen) irProjExpr(p *ir.Proj, k kind) expr {
	g.irAppend(p)
	if irProjKindObserved != nil {
		irProjKindObserved("irproj.go irProjExpr", p, k)
	}
	irSideSet(&g.irPro.sides, p.Dst(), irScalarSide{k: k})
	return g.irBind(p.Dst(), k)
}

// irProjField reads a declared struct field.
//
// The FIELD is what the Symbol is interned on, not the struct: two same-named
// fields of two structs are two declarations, and a read names one of them.
func (g *gen) irProjField(at ast.Node, obj expr, f *fieldDef, name string) expr {
	p := ir.NewProjField(g.irNodePos(at), g.irMint(), g.irHold(obj),
		g.irTypes().Symbol(f, name), name, irParamShape(f.k))
	return g.irProjExpr(p, f.k)
}

// irProjRecordField reads an anonymous struct's field BY NAME. No Symbol,
// because a record has no declaration to be one: the name is the identity.
func (g *gen) irProjRecordField(at ast.Node, obj expr, name string, k kind) expr {
	p := ir.NewProjRecordField(g.irNodePos(at), g.irMint(), g.irHold(obj), name, irParamShape(k))
	return g.irProjExpr(p, k)
}

// irProjSlot reads one component of a tuple by index.
func (g *gen) irProjSlot(at ast.Node, obj expr, i int, k kind) expr {
	p := ir.NewProjSlot(g.irNodePos(at), g.irMint(), g.irHold(obj), i, irParamShape(k))
	return g.irProjExpr(p, k)
}

// irProjPayload reads payload i of a KNOWN variant — the tag has already been
// established by a pattern match, a `try` or an attach.
func (g *gen) irProjPayload(at ast.Node, subj expr, d *typeDef, v *variantDef, i int, k kind) expr {
	if v.kind == "embedded" && v.embeds != nil && i == 0 {
		// An `embeds` variant's value is the embedded value itself in the VM,
		// so its payload is read as that value (irvariantpattern.go's
		// variantPayload).
		p := ir.NewProjPayloadEmbed(g.irNodePos(at), g.irMint(), g.irHold(subj),
			g.irTypeSym(d), v.nomi, g.irTypeSym(v.embeds), irParamShape(k))
		return g.irProjExpr(p, k)
	}
	if v.kind == "struct" && i < len(v.payloads) && v.payloads[i].nomi != "" {
		// A struct-shaped variant's payload is keyed by its field name
		// (`fn span(Cell.Span{hi, lo})`), as a case arm reads it.
		p := ir.NewProjPayloadField(g.irNodePos(at), g.irMint(), g.irHold(subj),
			g.irTypeSym(d), v.nomi, v.payloads[i].nomi, i, irParamShape(k))
		return g.irProjExpr(p, k)
	}
	p := ir.NewProjPayload(g.irNodePos(at), g.irMint(), g.irHold(subj),
		g.irTypeSym(d), v.nomi, i, irParamShape(k))
	return g.irProjExpr(p, k)
}

// irProjInner reads the value a distinct type wraps.
func (g *gen) irProjInner(at ast.Node, arg expr, d *typeDef, want kind) expr {
	p := ir.NewProjInner(g.irNodePos(at), g.irMint(), g.irHold(arg),
		g.irTypeSym(d), irParamShape(want))
	return g.irProjExpr(p, want)
}
