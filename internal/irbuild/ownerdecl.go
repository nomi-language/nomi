package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// The owner of a type-qualified call, by the declaration the checker bound it
// to.
//
// Most of the builder's owner routes key on the owner's NAME in this file's
// tables (g.types, g.ifaces, the std indexes by owner name), and a name is the
// declaration the module scope binds. That is the checker's answer wherever
// the name means the same thing at the call as at module scope. It is not
// where something between the two rebinds it: an `import` at the top of a
// block (`import std/duration.Duration` in a test body, beside a module-level
// `struct Duration`), or an alias of a std type whose own name this file
// declares. There the checker's reference at the owner is the answer, and a
// std owner it names is lowered through the module-qualified route
// `duration.Duration.seconds(1)` takes, which resolves from that module's
// declarations only.

// checkedTypeRef is the declaration the checker resolved the name written at
// line:col to, followed through imports, or nil.
func (g *gen) checkedTypeRef(line, col int) *analysis.Symbol {
	if g.fa == nil {
		return nil
	}
	return resolveSymbol(g.srcFA().References[analysis.Pos{Line: line, Col: col}])
}

// stdTypeModule is the std module (`duration`) that declares the type sym
// names, by the (Origin, Name) identity of its analyzer type.
func stdTypeModule(sym *analysis.Symbol) (string, bool) {
	if sym == nil {
		return "", false
	}
	var origin string
	switch ty := sym.Type.(type) {
	case *analysis.StructType:
		origin = ty.Origin
	case *analysis.EnumType:
		origin = ty.Origin
	case *analysis.DistinctType:
		origin = ty.Origin
	case *analysis.PrimitiveType:
		origin = ty.Origin
	default:
		return "", false
	}
	mod, isStd := strings.CutPrefix(origin, "std/")
	return mod, isStd && mod != ""
}

// stdNameTaken reports whether this file's name-keyed tables bind name to a
// declaration of their own here: a type or interface this file declares or
// mirrors, a generic template, or a type an enclosing block declares. A std
// type's name taken here does not reach that std type by name.
func (g *gen) stdNameTaken(name string) bool {
	if _, isBlockLocal := g.blockLocalNamed(name); isBlockLocal {
		return true
	}
	if g.blockLocalTemplate(name) != nil {
		return true
	}
	if _, local := g.types[name]; local {
		return true
	}
	if _, iface := g.ifaces[name]; iface {
		return true
	}
	return g.genericTemplates[name] != nil
}

// bareStdMethodCall lowers a bare call whose callee the checker resolved to a
// std type's method, brought into scope by a selective import
// (`import std/maybe.Maybe.{with_default}`, then `with_default(m, 0)`). The
// owner is the method's declared owning type, and the call takes the route
// its qualified spelling takes over the call as written, so the checker's
// facts about this call node (its solved signature, the types of its bare
// operands) are the ones every arm reads. Where this file's tables bind the
// owner's name to another declaration (a local `struct Date`), the route is
// the module-qualified one (`calendar.Date.parse`). handled is false when the
// callee is not such a method.
func (bl *irScalarBuilder) bareStdMethodCall(t *ast.Call, callee *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if g.std == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	sym := resolvedBareSymbolAt(g.srcFA(), callee)
	if sym == nil || sym.OwningType == "" || sym.Name == "" || len(g.std.byType[sym.OwningType+"."+sym.Name]) == 0 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	lower := func() (ir.Temp, kind, bool, bool) {
		if g.stdNameTaken(sym.OwningType) {
			mod, found := g.stdMethodModule(sym)
			if !found {
				return ir.NoTemp, kindInvalid, false, false
			}
			if v, k, mobile, ok, handled := bl.stdModuleOwnerCall(t, mod, sym.OwningType, sym.Name, false); handled {
				return v, k, mobile, ok
			}
			return ir.NoTemp, kindInvalid, false, false
		}
		return bl.typeQualCall(t, &ast.TypeIdent{Name: sym.OwningType, Line: callee.Line, Col: callee.Col}, sym.Name)
	}
	var v ir.Temp
	var k kind
	var mobile, ok bool
	if bl.recording > 0 {
		// Inside a VM-only assertion subject the call's rows are recorded
		// after it, as a qualified call's are.
		v, k, mobile, ok = bl.recordedQualCall(t, lower)
	} else {
		v, k, mobile, ok = lower()
	}
	return v, k, mobile, ok, true
}

// stdMethodModule is the std module that declares the method sym, an impl
// member of a std type (`Date.parse`): the module of the index entry whose
// declaration sym is, or for a `host fn`, whose index entry keeps no
// declaration, the one module that declares a member of that spelling.
func (g *gen) stdMethodModule(sym *analysis.Symbol) (string, bool) {
	if g.std == nil || sym == nil {
		return "", false
	}
	fs := g.std.byType[sym.OwningType+"."+sym.Name]
	for _, f := range fs {
		if f.decl != nil && ast.Node(f.decl) == sym.Node {
			return f.module, true
		}
	}
	if _, isFunc := sym.Node.(*ast.FuncDef); isFunc || len(fs) == 0 {
		return "", false
	}
	mod := fs[0].module
	for _, f := range fs[1:] {
		if f.module != mod {
			return "", false
		}
	}
	return mod, true
}

// stdTypeRebind reports whether a block import that binds sym may rebind a
// name the module scope binds to something else: sym is a monomorphic std
// struct, enum or distinct type, which every route the builder takes for the
// name reaches by its declaration (bindBlockImports, stdOwnerByDecl).
func (g *gen) stdTypeRebind(sym *analysis.Symbol) bool {
	if _, isStd := stdTypeModule(sym); !isStd {
		return false
	}
	d := g.importedTypeDef(sym)
	return d != nil && d.foreign == ""
}

// stdOwnerByDecl answers the std type an owner qualifier names, as its own
// name and its module, when the checker bound the qualifier to a std type the
// spelling does not reach by name here: an alias (`import std/iter.Iter as
// It`) of a type whose own name this file takes, or a name this file's tables
// bind to another declaration. rebound is false wherever the name-keyed
// routes reach the same declaration, and inside a std module, whose own types
// are its tables'.
func (g *gen) stdOwnerByDecl(ti *ast.TypeIdent) (owner, module string, rebound bool) {
	if ti == nil || g.stdModule != "" {
		return "", "", false
	}
	sym := g.checkedTypeRef(ti.Line, ti.Col)
	mod, isStd := stdTypeModule(sym)
	if !isStd || sym.Name == "" {
		return "", "", false
	}
	if !g.stdNameTaken(sym.Name) {
		// The bare name reaches the std type; an alias is canonicalized by
		// stdOwnerAlias.
		return "", "", false
	}
	return sym.Name, mod, true
}
