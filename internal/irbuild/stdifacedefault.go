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
// What reaches it is a call the checker resolved to the interface's
// function (a SymbolInterfaceMethod reference) that no impl writes:
// `Int.min(3, 4)`, `Comparable.min(3, 4)`, `Version.min(a, b)` on a derived
// impl, and the same calls inside a generic body once its type argument is
// known. So do calls the builder writes, which carry no reference: a
// record field's `ToJson.to_json_field(f)` and a dispatch entry's
// (`stdProgThunk`), qualified by the interface, and a derived body's
// `T.from_missing()` at a kind no impl of which writes it
// (`stdIfaceDefaultAtKind`), found by its unique name. A default with no
// `self` operand (`fn from_missing() -> Maybe<self>`) takes its receiver
// from the qualifier, the call's type argument or its checked result. An impl that writes the function (an `open` override, `Float.min`)
// is found first by every route that finds a written impl function, so it
// takes precedence. A default is no slot of an rt dispatch table: the
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

// stdIfaceDefaultOwner is the std interface that declares a Nomi-bodied
// default named method, or "" when none does or two do.
func (s *stdInstances) stdIfaceDefaultOwner(method string) string {
	if s == nil || s.std == nil {
		return ""
	}
	found := ""
	for _, v := range s.std.views {
		for _, n := range v.nodes {
			decl, ok := n.(*ast.InterfaceDef)
			if !ok || stdModuleIfaceDefault(v, decl.Name, method) == nil {
				continue
			}
			if found != "" && found != decl.Name {
				return ""
			}
			found = decl.Name
		}
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
		iface:        decl.Name,
		name:         im.Name,
		pub:          true,
		selfIdx:      -1,
		why:          stdIfaceDefaultWhy,
		decl:         fd,
		result:       kindUnit,
	}
	for i, p := range im.Params {
		f.paramNames = append(f.paramNames, p.Name)
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
	d := g.ifaces[f.iface]
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

// irSynthesizedStdIfaceCall reports whether callee is an interface-qualified
// callee the builder wrote rather than the source: at column 0
// (synthIfaceCall) or at line 1, column 1 (stdProgThunk), where the checker
// never records a reference.
func irSynthesizedStdIfaceCall(callee *ast.FieldAccess) bool {
	if _, named := callee.Object.(*ast.TypeIdent); !named {
		return false
	}
	return callee.Field.Col == 0 || (callee.Field.Line == 1 && callee.Field.Col == 1)
}

// stdIfaceDefaultCall lowers `Owner.method(args)`, whose operands are
// lowered into args, as the default of a std interface at its receiver:
// the call no written impl function answered. handled is false when the
// call does not name such a default.
func (bl *irScalarBuilder) stdIfaceDefaultCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	fa, qualified := t.Func.(*ast.FieldAccess)
	if !qualified || fa.Field == nil || !args.ok || bl.g.stdInsts == nil {
		return no()
	}
	iface, ok := stdIfaceDefaultRef(bl.g.srcFA(), fa.Field.Line, fa.Field.Col)
	switch {
	case ok:
	case irSynthesizedStdIfaceCall(fa):
		// A call the builder wrote, where the checker recorded nothing: a
		// record field's `ToJson.to_json_field(f)` (irrecordjson.go), or a
		// std interface value's dispatch entry (stdProgThunk). The
		// qualifier is the std interface's own name, as the builder's
		// other calls of std impls by name are.
		iface = owner
	// kindInvalid: sentinel — kindInvalid marks defaultRecv and instKind as unbound; no operand is judged.
	case bl.defaultRecv != kindInvalid || (bl.instCall == t && bl.instKind != kindInvalid):
		// A call the builder respelled with its owner at a kind
		// (stdIfaceDefaultAtKind; stdKindQualCall for
		// `FromJson.from_missing<List<Int>>()` or `T.from_missing()` with T
		// a `List<Int>`), whose owner no reference names: the one std
		// interface with a default of the name, as a container's std
		// template is found by its name.
		if iface = bl.g.stdInsts.stdIfaceDefaultOwner(method); iface == "" {
			return no()
		}
	default:
		return no()
	}
	if d, local := bl.g.ifaceNamed(iface); local && d != nil && bl.g.stdModule == "" {
		// The program's own interface of the name, which the checker's
		// reference does not tell from std's by name alone.
		return no()
	}
	f := bl.g.stdInsts.stdIfaceDefaultTemplate(iface, method)
	if f == nil {
		return no()
	}
	declined := func(why string) (ir.Temp, kind, bool, bool, bool) {
		bl.g.dc.note(why)
		return ir.NoTemp, kindInvalid, false, false, true
	}
	var recv kind
	switch {
	// kindInvalid: sentinel — kindInvalid marks defaultRecv as unbound; no operand is judged.
	case f.selfIdx < 0 && bl.defaultRecv != kindInvalid:
		recv = bl.defaultRecv
	case f.selfIdx < 0:
		// `Int.from_missing()`: no operand is a `self`, so the qualifier
		// or the call's result names the receiver.
		r, ok := bl.stdIfaceDefaultOwnerSelf(t, f, owner, iface)
		if !ok {
			return declined("a stdlib interface default with no receiver operand: " + f.key)
		}
		recv = r
	case f.selfIdx >= len(args.kinds):
		return declined("a stdlib interface default with no receiver operand: " + f.key)
	default:
		recv = args.kinds[f.selfIdx]
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
	inst, why := bl.g.stdInsts.instantiate(f, []string{stdIfaceDefaultSelfParam}, []kind{recv}, bl.g)
	if inst == nil {
		return declined(why)
	}
	prevCall, prevArgs := bl.stdInstPreCall, bl.stdInstPreArgs
	bl.stdInstPreCall, bl.stdInstPreArgs = t, args
	defer func() { bl.stdInstPreCall, bl.stdInstPreArgs = prevCall, prevArgs }()
	v, k, mobile, lowered := bl.stdInstCall(t, inst)
	return v, k, mobile, lowered, true
}

// stdIfaceDefaultAtKind lowers `T.method(args)`, a call the checker
// resolved to a std interface's function, with T bound to k and no impl of
// k writing method: the default instantiated at k
// (`T.from_missing()` at a `List<Int>` or a derived struct). handled is
// false when the call names no such default.
func (bl *irScalarBuilder) stdIfaceDefaultAtKind(t *ast.Call, args irQualArgs, k kind, method string) (ir.Temp, kind, bool, bool, bool) {
	prev := bl.defaultRecv
	bl.defaultRecv = k
	defer func() { bl.defaultRecv = prev }()
	return bl.stdIfaceDefaultCall(t, args, "", method)
}

// stdIfaceDefaultOwnerSelf is the receiver of a call of default f, which
// takes no `self` operand: the instance a generic qualifier named
// (`List<Int>.from_missing()`, carried as instKind), the type the
// qualifier names (`Int.from_missing()`, or a type parameter's binding),
// the type argument of an interface-qualified call
// (`FromJson.from_missing<Int>()`), or else the `self` the checked result
// holds when the default returns `self` or `Maybe<self>`.
func (bl *irScalarBuilder) stdIfaceDefaultOwnerSelf(t *ast.Call, f *stdFunc, owner, iface string) (kind, bool) {
	// kindInvalid: sentinel — kindInvalid marks instKind as unbound; no operand is judged.
	if bl.instCall == t && bl.instKind != kindInvalid {
		return bl.instKind, true
	}
	if owner != iface {
		// kindInvalid: lookup — asks whether the qualifier names a type; a miss tries the next source, and the caller declines with its own reason.
		if k := bl.g.qualifierKind(owner); k != kindInvalid {
			return k, true
		}
	}
	if len(t.TypeArgs) == 1 {
		// kindInvalid: lookup — asks whether the type argument names a type; a miss tries the next source, and the caller declines with its own reason.
		if k := bl.g.typeOf(t.TypeArgs[0]); k != kindInvalid {
			return k, true
		}
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || f.decl.ReturnTypeExpr == nil {
		return kindInvalid, false
	}
	result := bl.g.project(ft.Return)
	switch ret := f.decl.ReturnTypeExpr.(type) {
	case *ast.SelfType:
		// kindInvalid: lookup — the checked result's self; a miss makes the caller decline with its own reason.
		return result, result != kindInvalid
	case *ast.GenericType:
		if _, self := ret.Params[0].(*ast.SelfType); self && len(ret.Params) == 1 && ret.Name == "Maybe" &&
			irMaybeKind(result) && len(result.def.preludeArgs) == 1 {
			return result.def.preludeArgs[0], true
		}
	}
	return kindInvalid, false
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
