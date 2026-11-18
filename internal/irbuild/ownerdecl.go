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
// block (`import std::duration.Duration` in a test body, beside a module-level
// `struct Duration`), or an alias of a std type whose own name this file
// declares. There the checker's reference at the owner is the answer, and a
// std owner it names is lowered through the module-qualified route
// `duration.Duration.seconds(1)` takes, which resolves from that module's
// declarations only.

// checkedRef is the declaration the checker resolved the name written at
// line:col to, followed through imports, or nil.
func (g *gen) checkedRef(line, col int) *analysis.Symbol {
	if g.fa == nil {
		return nil
	}
	return resolveSymbol(g.srcFA().References[analysis.Pos{Line: line, Col: col}])
}

// qualifierDecl is the declaration of the type or interface q names: a
// program's, which the checker records (Qualifier.Decl), or a std
// module's, the one its module scope binds the name to. Nil for any other
// qualifier.
func (g *gen) qualifierDecl(q analysis.Qualifier) ast.Node {
	if q.Std.IsZero() || q.Decl != nil {
		return q.Decl
	}
	fa := g.srcFA()
	if fa == nil || fa.Program() == nil {
		return nil
	}
	sym := resolvedScopeSymbol(fa.Program().StdlibModuleScopes[stdShortModule(q.Std.Module)], q.Std.Name)
	if sym == nil {
		return nil
	}
	return sym.Node
}

// funcDeclAt is the declaration of the module function (a `fn` or a `host
// fn`) the checker resolved the name written at line:col to, or nil when
// the name is anything else: a local, a parameter, a `once`, a type.
func (g *gen) funcDeclAt(line, col int) ast.Node {
	sym := g.checkedRef(line, col)
	if sym == nil || sym.Kind != analysis.SymbolFunction {
		return nil
	}
	return sym.Node
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
	return mod, isStd && mod != "" && !isProgramStd(mod)
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
// (`import std::maybe.Maybe.{with_default}`, then `with_default(m, 0)`). The
// owner is the method's declared owning type, and the call takes the route
// its qualified spelling takes over the call as written, so the checker's
// facts about this call node (its solved signature, the types of its bare
// operands) are the ones every arm reads. Where this file's tables bind the
// owner's name to another declaration (a local `struct Date`), the route is
// the module-qualified one (`calendar.Date.parse`). handled is false when the
// callee is not such a method.
func (bl *irScalarBuilder) bareStdMethodCall(t *ast.Call, callee *ast.Ident, sym *analysis.Symbol) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if g.std == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
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
		// Inside an assertion subject the call's rows are recorded
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
// name reaches by its declaration (bindBlockImports, qualOwner).
func (g *gen) stdTypeRebind(sym *analysis.Symbol) bool {
	if _, isStd := stdTypeModule(sym); !isStd {
		return false
	}
	d := g.importedTypeDef(sym)
	return d != nil && d.foreign == ""
}

// qualOwnerRoute is how qualCallLowered reaches a type-qualified call's
// owner (qualOwner).
type qualOwnerRoute uint8

const (
	// ownerTyped: the owner's routes, by the name qualOwner answers,
	// which names the qualifier's declaration in this file.
	ownerTyped qualOwnerRoute = iota
	// ownerStdModule: a std type this file does not reach by its name;
	// the call is the module-qualified one (`duration.Duration.seconds`).
	ownerStdModule
	// ownerResolved: a program's type this file does not reach by its
	// name; the call is the function of its block the checker resolved
	// it to (resolvedOwnerPlan).
	ownerResolved
)

// qualOwner is how the call t, written `ti.m(...)`, reaches the type or
// interface the checker resolved ti to (analysis.Callee.Qualifier): by the
// name of that declaration where this file's tables bind the name to it,
// else through the declaration's module or its owner function.
func (bl *irScalarBuilder) qualOwner(t *ast.Call, ti *ast.TypeIdent) (route qualOwnerRoute, name, module string) {
	g := bl.g
	if g.isUnchecked(t) || g.isRespelled(t) {
		// Code the checker never checked has no record (markUnchecked),
		// and a copy the builder respelled names an owner the builder's
		// tables bind (isRespelled): its owner is the one the builder
		// wrote.
		return ownerTyped, ti.Name, ""
	}
	q := g.routeCallee(t).Qualifier
	fa := g.srcFA()
	// reaches reports whether name, written here, names the qualifier's
	// declaration through this file's scope and tables.
	reaches := func(name string) bool {
		sym := resolvedTypeSymbol(fa, name)
		return sym != nil && fa.QualifierOfSymbol(sym) == q
	}
	// writtenReaches: the name as written reaches the declaration in the
	// scope at the call, or through an import at the top of a block.
	writtenReaches := false
	if scope := fa.ScopeAt(analysis.Pos{Line: t.Line, Col: t.Col}); scope != nil {
		if sym := resolveSymbol(scope.Lookup(ti.Name)); sym != nil {
			writtenReaches = fa.QualifierOfSymbol(sym) == q
		}
	}
	if _, imported := g.blockImportedNamed(ti.Name); imported {
		writtenReaches = true
	}
	switch {
	case q.Kind == analysis.QualType && !q.Std.IsZero():
		canon := q.Std.Name
		// This file's tables take the name (a program's type of the name,
		// a block-local one, an edited std module's own declarations in a
		// program's lowering): the module-qualified call, which resolves
		// from that module's declarations only.
		if g.stdModule == "" && !isProgramStd(stdShortModule(q.Std.Module)) && g.stdNameTaken(canon) {
			return ownerStdModule, canon, stdShortModule(q.Std.Module)
		}
		// The name reaches another declaration here: the module-qualified
		// call.
		if sym := resolvedTypeSymbol(fa, canon); sym != nil && fa.QualifierOfSymbol(sym) != q {
			return ownerStdModule, canon, stdShortModule(q.Std.Module)
		}
		if _, local := g.blockLocalNamed(canon); local {
			return ownerStdModule, canon, stdShortModule(q.Std.Module)
		}
		// Neither the written name nor the declaration's own reaches it
		// here (a subtype's refinement check in a file that binds no name
		// to the subtype): the module-qualified call.
		if !writtenReaches && !reaches(canon) && !reaches(ti.Name) {
			return ownerStdModule, canon, stdShortModule(q.Std.Module)
		}
		return ownerTyped, canon, ""
	case q.Kind == analysis.QualInterface && !q.Std.IsZero():
		// An alias of the interface (`import std::iter.Iter as It`): its
		// own name, where nothing else here takes it.
		if canon := q.Std.Name; canon != ti.Name && reaches(ti.Name) && !g.stdNameTaken(canon) {
			return ownerTyped, canon, ""
		}
	case (q.Kind == analysis.QualType || q.Kind == analysis.QualInterface) && q.Decl != nil:
		canon := typeDeclName(q.Decl)
		if et, host := q.Decl.(*ast.ExternType); host {
			canon = et.Name
		}
		_, local := g.blockLocalNamed(ti.Name)
		if sym := resolvedTypeSymbol(fa, ti.Name); !local && canon != "" && canon != ti.Name &&
			sym != nil && sym.Kind == analysis.SymbolTypeAlias && fa.QualifierOfSymbol(sym) == q {
			// The written name is a typealias of the declaration (`Tiny`
			// in the parent check `type Bit Tiny` generates, for
			// `typealias Tiny Small`), which no route keys a type's
			// functions on: the declaration's own name where that
			// reaches it, else its owner function.
			if reaches(canon) {
				return ownerTyped, canon, ""
			}
			return ownerResolved, ti.Name, ""
		}
		if local || writtenReaches || reaches(ti.Name) {
			return ownerTyped, ti.Name, ""
		}
		if canon != ti.Name && resolvedTypeSymbol(fa, ti.Name) != nil {
			// The builder wrote another owner over the call (an
			// interface's function for the type it is called for,
			// `Pair.decode` for `Decode.decode`): that owner's routes.
			// The declaration's own name bound here to another type of
			// that name (a literal's check of another file's `Many` in a
			// file declaring its own) is no such owner.
			return ownerTyped, ti.Name, ""
		}
		if canon != "" && reaches(canon) {
			return ownerTyped, canon, ""
		}
		return ownerResolved, ti.Name, ""
	}
	return ownerTyped, ti.Name, ""
}
