package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// defOf is the def the gen that declares o holds for its declaration, found
// by the declaration (the gen's defsByDecl), or nil when that gen has none:
// it has not declared its types, or o is a second declaration of one name,
// which its own file refuses.
//
// Under substVerify the declaring gen's table by name (`types`) must answer
// the same def for the declaration's name, until every reader finds a def
// by its declaration (package doc, "What a name names").
func (r *typeRegistry) defOf(o *typeOwner) *typeDef {
	owner := r.gens[o.unit]
	if owner == nil {
		return nil
	}
	d := owner.defsByDecl[o.decl]
	if substVerify {
		if byName := owner.types[o.nomi]; byName != d {
			panic(fmt.Sprintf("irbuild: %s declares %s, whose def by its declaration is %s and by its name %s",
				o.fileKey, o.nomi, defLabel(d), defLabel(byName)))
		}
	}
	return d
}

// ifaceDefOf is defOf for an interface declaration (ifacesByDecl).
func (r *typeRegistry) ifaceDefOf(o *typeOwner) *ifaceDef {
	owner := r.gens[o.unit]
	if owner == nil {
		return nil
	}
	d := owner.ifacesByDecl[o.decl]
	if substVerify {
		if byName := owner.ifaces[o.nomi]; byName != d {
			panic(fmt.Sprintf("irbuild: %s declares interface %s, whose def by its declaration is %s and by its name %s",
				o.fileKey, o.nomi, ifaceLabel(d), ifaceLabel(byName)))
		}
	}
	return d
}

// ifaceOfDecl is this gen's def of the interface declaration decl: its
// own (ifacesByDecl, decl read as this gen's module declares it,
// ownDecl), or its mirror of another program file's (mirrorIface, the one
// def any spelling of it reaches here). Nil for a declaration this gen
// holds no def of: none, or a std module's that this gen does not lower.
func (g *gen) ifaceOfDecl(decl ast.Node) *ifaceDef {
	if isNilNode(decl) {
		return nil
	}
	if d := g.ifacesByDecl[g.ownDecl(decl)]; d != nil {
		return d
	}
	if g.reg != nil {
		if o := g.reg.byDecl[decl]; o != nil && o.isIface && o.unit != g.fileUnit {
			return g.mirrorIface(o)
		}
	}
	return nil
}

// declNode is the declaration the checker's nominal type (origin, name)
// names: the node name resolves to in the module scope of the file whose
// analyzer origin is origin, this gen's own file or, through the registry
// (typeRegistry.fileOf), another program file. Nil when no such file
// binds the name; a name the file imports resolves to the declaration in
// the file it is imported from.
func (g *gen) declNode(origin, name string) ast.Node {
	if origin == "" {
		return nil
	}
	fa := g.fa
	if fa == nil || fa.Origin != origin {
		if g.reg == nil {
			return nil
		}
		f, ok := g.reg.fileOf[origin]
		if !ok {
			return nil
		}
		fa = f.fa
	}
	sym := resolvedTypeSymbol(fa, name)
	if sym == nil {
		return nil
	}
	return sym.Node
}

// typeDeclDef is this gen's def of the struct, enum or distinct type the
// checker's symbol sym declares, lowerable or not: its own def or a block's
// (defsByDecl), its mirror of another program file's (mirrorOf), or the def
// a std type's checker type projects to. A `typealias` names its target's.
// A generic declaration's def is its shell. Nil for any other symbol.
func (g *gen) typeDeclDef(sym *analysis.Symbol) *typeDef {
	sym = resolveSymbol(sym)
	if sym == nil {
		return nil
	}
	if _, isParam := sym.Type.(*analysis.TypeParam_); isParam {
		// A type parameter declares no type; what it stands for in an
		// instance is project's to say.
		return nil
	}
	switch sym.Kind {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType:
	case analysis.SymbolTypeAlias:
		return g.nominalDef(sym.Type)
	default:
		return nil
	}
	if d := g.defsByDecl[sym.Node]; d != nil {
		return d
	}
	if o := g.ownerOfSymbol(sym); o != nil {
		if o.isIface {
			return nil
		}
		return g.mirrorOf(o)
	}
	return g.nominalDef(sym.Type)
}

// writtenTypeDef is this gen's def of the declaration the written type te
// names, by the type the checker recorded for it (recordedTypeExpr,
// nominalDef), or nil.
func (g *gen) writtenTypeDef(te ast.TypeExpr) *typeDef {
	rec, _ := g.recordedTypeExpr(te)
	return g.nominalDef(rec)
}

// nominalDef is this gen's def of the declaration the checker's nominal type
// t names, lowerable or not, an instance's template's shell for a generic
// one: a program file's declaration (programTypeDef), or the def a std
// type projects to. Nil for any other type.
func (g *gen) nominalDef(t analysis.Type) *typeDef {
	if d, program := g.programTypeDef(t); program || d != nil {
		return d
	}
	if k := g.project(t); k.tag == tagNamed {
		return k.def
	}
	return nil
}

// programTypeDef is this gen's def of the declaration a program file makes
// that the checker's nominal type t names, lowerable or not, an instance's
// template's shell for a generic one: a type a block of this file declares
// (blockLocalOfType, blockLocalTemplateOf), one this file declares
// (declNode, defsByDecl), or another program file's mirror (siblingOwner,
// mirrorOf). program is false when t names no program file's declaration:
// a std type, or no nominal type.
func (g *gen) programTypeDef(t analysis.Type) (d *typeDef, program bool) {
	if g == nil || g.fa == nil {
		return nil, false
	}
	t = analysis.ResolveClosed(t)
	var origin, name string
	var params []*analysis.TypeParam_
	switch ty := t.(type) {
	case *analysis.StructType:
		origin, name, params = ty.Origin, ty.Name, ty.TypeParamDefs
	case *analysis.EnumType:
		origin, name, params = ty.Origin, ty.Name, ty.TypeParamDefs
	case *analysis.DistinctType:
		if ty.Subtype {
			// A subtype is its base (subtype.go) and declares no
			// type of its own.
			return nil, true
		}
		origin, name, params = ty.Origin, ty.Name, ty.TypeParamDefs
	default:
		return nil, false
	}
	if bd := g.blockLocalOfType(t); bd != nil {
		return bd, true
	}
	if tpl := g.blockLocalTemplateOf(params); tpl != nil {
		return g.defsByDecl[tpl.decl], true
	}
	switch {
	case origin == "" || analysis.IsBodyOrigin(origin) || g.fa.Origin == "":
		return nil, true
	case origin == g.fa.Origin:
		// A std module's own host or opaque type has no def of the
		// module's: its projection answers it.
		d := g.defsByDecl[g.declNode(origin, name)]
		return d, d != nil
	}
	if o := g.siblingOwner(origin, name); o != nil {
		return g.mirrorOf(o), true
	}
	return nil, false
}

// siblingDecl is the registry's record of the declaration (origin, name)
// names when the program file with that origin declares it itself, or nil.
func (g *gen) siblingDecl(origin, name string) *typeOwner {
	if g.reg == nil {
		return nil
	}
	f, ok := g.reg.fileOf[origin]
	if !ok {
		return nil
	}
	o := g.reg.byDecl[g.declNode(origin, name)]
	if o == nil || o.unit != f.unit {
		return nil
	}
	return o
}

// defLabel names a def for a verifier's message: its name and the line of
// its declaration, and whether it mirrors another file's.
func defLabel(d *typeDef) string {
	if d == nil {
		return "none"
	}
	return declLabelOf(d.nomi, d.decl, d.foreign)
}

func ifaceLabel(d *ifaceDef) string {
	if d == nil {
		return "none"
	}
	return declLabelOf(d.nomi, d.decl, d.foreign)
}

func declLabelOf(name string, decl ast.Node, foreign string) string {
	s := name
	if !isNilNode(decl) {
		s += fmt.Sprintf(" (line %d)", decl.LineNum())
	}
	if foreign != "" {
		s += ", a mirror from " + foreign
	}
	return s
}
