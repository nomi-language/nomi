package irbuild

import (
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A DEFAULT OF A STDLIB INTERFACE, LOWERED IN ITS DECLARING MODULE.
//
// `open fn min(a: self, b: self) -> self { case compare(b, a) { ... } }` in
// std/comparable is Nomi written in std/comparable: its names (`compare`,
// `Debug`, a private `host fn` of that file) resolve there, as a program
// interface's default is lowered in its declaring file (ifacedefault.go).
// A std module is lowered once per process, and the receivers are the
// program's, so a std interface's default is a template over `self`,
// instantiated per receiver kind as a generic std function is instantiated
// per type argument (stdinst.go): by a gen of the declaring module's retained view, with
// `self` bound to the receiver (gen.defaultSelf), so every name in the body
// is the declaring module's and every `self` position is the receiver's. A
// bare call of the interface's own function in the body is that function at
// the receiver (stdIfaceOwnCall), which dispatches to the receiver's impl
// wherever it is written: std's, the program's, or a derived one. One
// instance per receiver kind and program, shared by every call.
//
// What reaches it is a call whose record names the default
// (recordedStdDefault): the interface's own function, by its std
// identity, where the receiver's block inherits it or the receiver meets
// the interface by its shape. `Int.min(3, 4)`, `Comparable.min(3, 4)`,
// `Version.min(a, b)` on a derived impl, the interface named through its
// module in a file that declares an interface of its name
// (`json.ToJson.to_json_field(s)`), and the same
// calls inside a generic body, whose record the instance's types answer
// (calleeAt: `T.from_missing()` at a `List<Int>` or a derived struct). So
// do the calls the builder writes with the record a written call has
// (stdIfaceCallRecord): a record field's `ToJson.to_json_field(f)` and a
// dispatch entry's (`stdProgThunk`). A default with no `self` operand (`fn
// from_missing() -> Maybe<self>`) is instantiated at the type the record
// says the call is for (Callee.For). An impl that writes the function (an
// `open` override, `Float.min`) is the record's Decl instead, and is found
// first by every route that finds a written impl function. A default is no slot of an rt dispatch table: the
// anchor of a std interface with a table ignores its defaults
// (stdIfaceSpec.matches), so `Comparable.compare` keeps its table, and a
// default on an erased receiver declines.

// stdIfaceDefaultWhy is the why of a std interface default's template.
const stdIfaceDefaultWhy = "stdlib interface default"

// stdIfaceDefaultSelfParam is the template's one type parameter, the
// receiver. `self` is a keyword, so no type parameter of the program or of
// std can be spelled the same.
const stdIfaceDefaultSelfParam = "self"

// stdIfaceDefaultFuncs holds each default's template, keyed by its
// declaration. A view's AST belongs to one stdlib load, so the key never
// answers for another load's declaration.
var stdIfaceDefaultFuncs sync.Map // *ast.InterfaceMethod -> *stdFunc

// stdIfaceDefaultTemplate is the template of the Nomi-bodied default method
// that the std interface iface declares, or nil when no std module declares
// one by that name, or two do.
func (s *stdInstances) stdIfaceDefaultTemplate(iface, method string) *stdFunc {
	if s == nil || s.std == nil {
		return nil
	}
	var found *stdFunc
	for _, v := range s.std.views {
		f := stdModuleIfaceDefault(v, iface, method)
		if f == nil {
			continue
		}
		if found != nil {
			return nil
		}
		found = f
	}
	return found
}

// stdModuleIfaceDefault is the template of default method of the public
// interface iface that the module of v declares, or nil.
func stdModuleIfaceDefault(v *stdModuleView, iface, method string) *stdFunc {
	if v == nil {
		return nil
	}
	for _, n := range v.nodes {
		decl, ok := n.(*ast.InterfaceDef)
		if !ok || decl.Name != iface || !decl.Public || len(decl.TypeParams) > 0 {
			continue
		}
		for i := range decl.Methods {
			im := &decl.Methods[i]
			if im.Name != method || !stdIfaceNomiDefault(im) || len(im.TypeParams) > 0 || len(im.WhereClauses) > 0 {
				continue
			}
			if _, block := im.Body.(*ast.Block); !block {
				continue
			}
			if f, ok := stdIfaceDefaultFuncs.Load(im); ok {
				return f.(*stdFunc)
			}
			f, _ := stdIfaceDefaultFuncs.LoadOrStore(im, newStdIfaceDefault(v.module, decl, im))
			return f.(*stdFunc)
		}
	}
	return nil
}

// newStdIfaceDefault is the template of default im of decl, declared in
// module. Its declaration is the default's own parameters, result and body,
// as irImplSource presents an inherited body.
func newStdIfaceDefault(module string, decl *ast.InterfaceDef, im *ast.InterfaceMethod) *stdFunc {
	body, _ := im.Body.(*ast.Block)
	fd := &ast.FuncDef{
		Name:           im.Name,
		Params:         im.Params,
		ReturnTypeExpr: im.ReturnTypeExpr,
		Body:           body,
		Line:           im.Line,
		Col:            im.Col,
		Public:         true,
	}
	f := &stdFunc{
		ifaceDefault: im,
		key:          decl.Name + "." + im.Name,
		module:       module,
		iface:        analysis.StdType{Module: module, Name: decl.Name},
		name:         im.Name,
		selfIdx:      -1,
		why:          stdIfaceDefaultWhy,
		decl:         fd,
		node:         im,
		result:       kindUnit,
	}
	for i, p := range im.Params {
		if _, self := p.TypeAnnotation.(*ast.SelfType); self && f.selfIdx < 0 {
			f.selfIdx = i
		}
	}
	return f
}

// bindStdIfaceDefaultSelf binds `self` in g, the instance gen of default f,
// to recv: the interface is g's own declaration of it, which the body's
// bare calls name. It reports false when g declares no such interface.
func (g *gen) bindStdIfaceDefaultSelf(f *stdFunc, recv kind) bool {
	d := g.ifacesByDecl[stdInterfaceDecl(g.fa, f.iface)]
	if d == nil {
		return false
	}
	g.defaultSelf = &irDefaultSelf{iface: d, recv: recv}
	return true
}

// stdIfaceDefaultRef is the std interface whose function the checker
// resolved the callee named at line:col to, when it resolved it to an
// interface's function.
func stdIfaceDefaultRef(fa *analysis.FileAnalysis, line, col int) (iface string, ok bool) {
	if fa == nil {
		return "", false
	}
	sym := resolveSymbol(fa.References[analysis.Pos{Line: line, Col: col}])
	if sym == nil || sym.Kind != analysis.SymbolInterfaceMethod || sym.OwningInterface == "" || sym.SourceFile != "" {
		return "", false
	}
	return sym.OwningInterface, true
}

// stdIfaceDefaultCall lowers `Owner.method(args)`, whose operands are
// lowered into args, as the default of a std interface its record names
// (recordedStdDefault), instantiated at the receiver: the call no written
// impl function answered. handled is false when the record names no such
// default.
func (bl *irScalarBuilder) stdIfaceDefaultCall(t *ast.Call, args irQualArgs) (ir.Temp, kind, bool, bool, bool) {
	if !args.ok {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	f, forType := bl.recordedStdDefault(t)
	if f == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	declined := func(why string) (ir.Temp, kind, bool, bool, bool) {
		bl.g.dc.note(why)
		return ir.NoTemp, kindInvalid, false, false, true
	}
	// recv is the receiver the default is instantiated at, and recvType
	// the checker's type it was read from: the `self` its instance binds.
	// A default with no `self` operand (`Int.from_missing()`) is for the
	// type the record says the call is for.
	var recv kind
	var recvType analysis.Type
	switch {
	case f.selfIdx < 0:
		recv, recvType = bl.g.project(forType), bl.g.checkerTypeArg(forType)
		// kindInvalid: lookup — a type with no kind here declines.
		if recv == kindInvalid {
			return declined("a stdlib interface default with no receiver operand: " + f.key)
		}
	case f.selfIdx >= len(args.kinds):
		return declined("a stdlib interface default with no receiver operand: " + f.key)
	default:
		recv, recvType = args.kinds[f.selfIdx], bl.g.operandCheckerType(t.Args[f.selfIdx])
	}
	if recv.tag == tagIface {
		// An erased receiver: a default is no slot of its table.
		return declined("a stdlib interface default on an erased receiver: " + f.key)
	}
	for i, p := range f.decl.Params {
		if _, self := p.TypeAnnotation.(*ast.SelfType); self && i < len(args.kinds) && args.kinds[i] != recv {
			return declined("a stdlib interface default whose self operands differ: " + f.key)
		}
	}
	if recvType == nil {
		// An operand the builder wrote (a partial's placeholder) has no
		// type of its own; the call's record says which type it is for.
		recvType = bl.g.checkerTypeArg(forType)
	}
	var types []analysis.Type
	if recvType != nil {
		types = []analysis.Type{recvType}
	}
	inst, why := bl.g.stdInsts.instantiateTyped(f, []string{stdIfaceDefaultSelfParam}, []kind{recv}, types, bl.g)
	if inst == nil {
		return declined(why)
	}
	prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
	bl.stdInstPreCall, bl.stdInstPreArgs = t, args
	defer func() { bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs }()
	v, k, mobile, lowered := bl.stdInstCall(t, inst)
	return v, k, mobile, lowered, true
}

// recordedStdDefault is the Nomi-bodied std interface default the call t
// reaches by its record at this instance (calleeAt): a record whose Decl
// is the interface's own function, found by its std identity
// (Callee.Std), which the receiver's block (Callee.Impl) inherits or which
// runs for a receiver that meets the interface by its shape; and the type
// the record says the call is for (Callee.For), as the body being built
// writes it. Nil for any other record: a function an impl writes, a
// program interface's default.
func (bl *irScalarBuilder) recordedStdDefault(t *ast.Call) (*stdFunc, analysis.Type) {
	g := bl.g
	if g.stdInsts == nil || g.stdInsts.std == nil {
		return nil, nil
	}
	c := g.calleeAt(t)
	if c.Std.IsZero() {
		return nil, nil
	}
	if _, inherited := c.Decl.(*ast.InterfaceMethod); !inherited {
		return nil, nil
	}
	f := stdModuleIfaceDefault(g.stdInsts.std.views[stdShortModule(c.Std.Module)], c.Std.Owner, c.Std.Name)
	if f == nil {
		return nil, nil
	}
	return f, g.calleeOf(t).For
}

// stdIfaceOwnCall lowers a bare call, in the body of an interface default
// being instantiated (a std interface's, or a program interface's for another
// file's implementing type, ifacedefault.go), of a function of that interface
// (`compare(b, a)` in `Comparable.min`) as the interface-qualified call it is
// (`Comparable.compare(b, a)`): the receiver's own function, which its impl
// supplies, or another default instantiated at the same receiver. handled is
// false for any other call.
func (bl *irScalarBuilder) stdIfaceOwnCall(t *ast.Call, callee *ast.Ident) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	ds := g.defaultSelf
	stdDefault := g.stdInstCur != nil && g.stdInstCur.f != nil && g.stdInstCur.f.why == stdIfaceDefaultWhy
	if ds == nil || ds.iface == nil || !stdDefault && g.defaultInst == nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if !ownDefaultCall(g.srcFA(), callee, ds) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	// The qualifier has no position: the checker recorded the bare name's
	// reference, the interface's function, where the callee is, and a
	// qualifier read there would be taken for that function's owner.
	call := *t
	call.Func = &ast.FieldAccess{
		Object: &ast.TypeIdent{Name: ds.iface.nomi},
		Field:  &ast.Ident{Name: callee.Name, Line: callee.Line, Col: callee.Col},
		Line:   callee.Line,
		Col:    callee.Col,
	}
	v, k, mobile, ok := bl.lower(&call)
	return v, k, mobile, ok, true
}

// namesStdIfaceDefault reports whether the type-qualified callee, in a body
// of the module fa analyzed, is a std interface's default the checker
// resolved it to: a call a program builds the instance of, so the body is
// built per program too (callsGenericSibling).
func (s *stdInstances) namesStdIfaceDefault(fa *analysis.FileAnalysis, callee *ast.FieldAccess) bool {
	if callee.Field == nil {
		return false
	}
	iface, ok := stdIfaceDefaultRef(fa, callee.Field.Line, callee.Field.Col)
	return ok && s.stdIfaceDefaultTemplate(iface, callee.Field.Name) != nil
}
