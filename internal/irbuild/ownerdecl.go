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
// A name in this file's tables (g.types, g.ifaces, the std indexes by owner
// name) is the declaration the module scope binds, which is not what the name
// means at a call where something between the two rebinds it: an `import` at
// the top of a block (`import std::duration.Duration` in a test body, beside
// a module-level `struct Duration`), an alias, a type reached through its
// module or a directory path. So a qualified call reads its owner from its
// record (analysis.Callee.RouteOwner) and the function from the declaration
// the record names (typeQualCall), and nothing reads the owner's spelling.

// checkedRef is the declaration the checker resolved the name written at
// line:col to, followed through imports, or nil.
func (g *gen) checkedRef(line, col int) *analysis.Symbol {
	if g.fa == nil {
		return nil
	}
	return resolveSymbol(g.srcFA().References[analysis.Pos{Line: line, Col: col}])
}

// ownOwner reports whether the checker resolved the owner of at to a
// declaration of the module being lowered: a call `T.m(...)` by its record
// (routeCallee, analysis.Callee.RouteOwner), a reference `T.m` by the
// reference at T. That is a type or interface one of the program's files
// declares (Qualifier.Decl), or one the std module being lowered declares.
// A std function of the owner's name is then not the one the owner names.
// Any other owner (another std module's type, a type parameter, none) is
// not.
func (g *gen) ownOwner(at ast.Node) bool {
	var q analysis.Qualifier
	switch t := at.(type) {
	case *ast.Call:
		q = g.routeCallee(t).RouteOwner()
	case *ast.FieldAccess:
		ti, isType := t.Object.(*ast.TypeIdent)
		if !isType || g.srcFA() == nil {
			return false
		}
		q = g.srcFA().QualifierOfSymbol(g.checkedRef(ti.Line, ti.Col))
	}
	return q.Decl != nil || g.fa != nil && !q.Std.IsZero() && q.Std.Module == g.fa.Origin
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

// qualOwnerName is the name of what the call t is qualified with, as the
// declaration the checker resolved it to names itself
// (analysis.Callee.RouteOwner): `Error` for `net.Error.of_tls(e)`, `Post`
// for `db::schema.Post.blank()`, `Gadget` for `Doodad.make(3)` where
// `Doodad` is an import alias of `Gadget`. It names a plan
// (`Owner.method`), and a symbol only through the plan's declaration,
// which every plan interns by. "" for a call with no such record.
func (g *gen) qualOwnerName(t *ast.Call) string {
	q := g.routeCallee(t).RouteOwner()
	switch {
	case !q.Std.IsZero():
		return q.Std.Name
	case q.Decl != nil:
		if et, host := q.Decl.(*ast.ExternType); host {
			return et.Name
		}
		return typeDeclName(q.Decl)
	case q.Kind == analysis.QualTypeParam && q.Param != nil:
		return q.Param.Name
	}
	return ""
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

// bareStdMethodCall lowers a bare call whose callee the checker resolved to a
// std type's method, brought into scope by a selective import
// (`import std::maybe.Maybe.{with_default}`, then `with_default(m, 0)`). The
// owner is the method's declared owning type, and the call takes the route
// its qualified spelling takes over the call as written, so the checker's
// facts about this call node (its solved signature, the types of its bare
// operands) are the ones every arm reads. The owner is the record's
// (typeQualCall), so a declaration this file has of the owner's name (a
// local `struct Date`) is not the call's. Whether the callee is such a
// method is the record's too: a std function of an owning type the std
// index holds (Callee.Std, byID). handled is false when it is not.
func (bl *irScalarBuilder) bareStdMethodCall(t *ast.Call) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	if g.std == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	std := g.calleeOf(t).Std
	if std.Owner == "" || g.std.byID[std] == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	lower := func() (ir.Temp, kind, bool, bool) { return bl.typeQualCall(t, std.Name) }
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

// stdTypeRebind reports whether a block import that binds sym may rebind a
// name the module scope binds to something else: sym is a monomorphic std
// struct, enum or distinct type, which every route the builder takes for the
// name reaches by its declaration (the type the checker recorded,
// typeQualCall).
func (g *gen) stdTypeRebind(sym *analysis.Symbol) bool {
	if _, isStd := stdTypeModule(sym); !isStd {
		return false
	}
	d := g.importedTypeDef(sym)
	return d != nil && d.foreign == ""
}
