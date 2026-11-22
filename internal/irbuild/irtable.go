package irbuild

// The builder's half of the IR's type table.
//
// This file is the producer side of `internal/ir`'s Table. It turns this
// package's `kind` into an `*ir.Type`: an existential or a concrete type,
// with the value shape the VM holds it as and an enum's `embeds` edges.
//
// # Kind equality is IR type identity
//
// `irTypeOf` interns every tag, including a `tagNamed` with no `*typeDef`, a
// `tagIface` with no `*ifaceDef` and a `tagTypeParam` with no
// `*typeParamDef`, under a placeholder display name, because `ir.Type` says
// its identity is the pointer and the name is for reading. Only `tagInvalid`
// answers nil, and that nil is semantic: it is the shape an operand whose own
// lowering was refused arrives in. The `embeds` edge is registered against
// `kind#{tag: tagNamed, def: e}`, so two IR types are identical exactly when
// two kinds are `==`. TestIRTable_TypeIdentityIsKindEquality is the pin.

import "github.com/nomi-language/nomi/internal/ir"

// irTypes is this compilation unit's IR type and declaration table, minted on
// first use.
//
// Lazily rather than in a constructor because `gen` is built at several sites
// and a table nobody asked for is a map nobody reads.
func (g *gen) irTypes() *ir.Table {
	if g.irTable == nil {
		g.irTable = ir.NewTable()
		g.irTable.SetLinkage(g.linkName, g.linkImport)
	}
	return g.irTable
}

// irKindName is a kind's Nomi name for display, without `kind.nomi()`'s
// preconditions.
//
// `nomi()` dereferences `def`, `iface` or `tp` for its three declaration tags,
// so a kind carrying one of those tags with a nil pointer panics reading it.
// That shape is a bug in this package rather than a program somebody wrote, and
// a panic is the wrong report for it HERE specifically: this name is display
// only — `ir.Type` states its identity is the pointer — so a placeholder
// costs nothing an answer depends on, while a panic would turn a malformed
// kind into a crash where a slot's type is asked for.
//
// The `comp` short-circuit is `nomi()`'s own first line, kept so the two agree
// for every kind either of them can name.
func irKindName(k kind) string {
	if k.comp != nil {
		return k.comp.nomi
	}
	switch k.tag {
	case tagNamed:
		if k.def == nil {
			return "<named>"
		}
	case tagIface:
		if k.iface == nil {
			return "<interface>"
		}
	case tagTypeParam:
		if k.tp == nil {
			return "<type parameter>"
		}
	}
	return k.nomi()
}

// irTypeOf is a kind as an IR type, or nil for `kindInvalid`.
//
// NIL ONLY FOR tagInvalid, and that nil is the semantics rather than a hole:
// it is the shape an operand whose own lowering was refused arrives in, and
// the refusal has already been recorded.
//
// THE TOKEN IS THE KIND VALUE. `kind` is comparable — a tag plus four
// declaration pointers — and its equality IS this package's type identity, so
// interning on it makes IR type identity and kind equality the same relation by
// construction rather than by a mapping somebody has to keep faithful. Every
// tag reaches an entry, malformed or not, which is what makes that sentence
// true for every kind rather than for the well-formed ones; see this file's
// header.
func (g *gen) irTypeOf(k kind) *ir.Type {
	if k.tag == tagInvalid {
		return nil
	}
	if k.tag == tagIface {
		// An existential is the one form whose values do not carry their own
		// concrete type, the only thing `ir.TypeForm` records.
		ty, minted := g.irTypes().Existential(k, irKindName(k))
		if v := g.irValType(k); minted && v != nil {
			ty.SetVal(v)
		}
		return ty
	}
	ty, minted := g.irTypes().Concrete(k, irKindName(k))
	if !minted {
		return ty
	}
	if v := g.irValType(k); v != nil {
		ty.SetVal(v)
	}
	// The `embeds` edges, declared exactly once per interned type. `minted` is
	// what makes "exactly once" checkable rather than remembered: the entry
	// exists before its edges are read.
	if k.tag == tagNamed && k.def != nil && k.def.isEnum {
		for i := range k.def.variants {
			e := k.def.variants[i].embeds
			if e == nil {
				continue
			}
			// `kind#{tag: tagNamed, def: e}` and not the variant's own kind: the
			// subtype's identity in the embeds relation is its declaration and
			// nothing else.
			if sub := g.irTypeOf(kind{tag: tagNamed, def: e}); sub != nil {
				g.irTypes().Embeds(ty, sub)
			}
		}
	}
	return ty
}
